package cmd

import (
	"crypto/rand"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/BlueSkyXN/AgentLedger/internal/adapters"
	"github.com/BlueSkyXN/AgentLedger/internal/config"
	"github.com/BlueSkyXN/AgentLedger/internal/db"
	"github.com/BlueSkyXN/AgentLedger/internal/fingerprint"
	"github.com/BlueSkyXN/AgentLedger/internal/model"
	"github.com/oklog/ulid/v2"
	"github.com/spf13/cobra"
)

const recentFileStabilityDelay = 100 * time.Millisecond

var importCmd = &cobra.Command{
	Use:   "import",
	Short: "Import usage data from local agent logs",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		database, err := db.Open(cfg.DBPath())
		if err != nil {
			return fmt.Errorf("failed to open database: %w", err)
		}
		defer database.Close()

		entropy := ulid.Monotonic(rand.Reader, 0)
		runID := ulid.MustNew(ulid.Timestamp(time.Now()), entropy).String()
		if err := database.StartImportRun(runID); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to record import run start: %v\n", err)
		}

		gracePeriod := time.Duration(cfg.Import.GracingMinutes) * time.Minute
		cutoff := time.Now().Add(-gracePeriod)

		totalFiles := 0
		totalAdded := 0
		totalUpdated := 0
		totalSkipped := 0
		var warnings []string

		allAdapters := adapters.AllAdapters()
		agentConfigs := map[string]*config.AgentConfig{
			"claude":  &cfg.Agents.Claude,
			"codex":   &cfg.Agents.Codex,
			"gemini":  &cfg.Agents.Gemini,
			"copilot": &cfg.Agents.Copilot,
		}

		for _, adapter := range allAdapters {
			agentCfg, ok := agentConfigs[adapter.Name()]
			if !ok || !agentCfg.Enabled {
				continue
			}
			adapter = configureImportAdapter(adapter, agentCfg)

			files, err := adapter.Discover(agentCfg.Paths)
			if err != nil {
				warning := fmt.Sprintf("%s discover failed: %v", adapter.Name(), err)
				warnings = append(warnings, warning)
				fmt.Fprintf(os.Stderr, "Warning: %s\n", warning)
				continue
			}

			if postProcessor, ok := adapter.(adapters.RecordPostProcessor); ok {
				records := make([]*fingerprint.ParsedRecord, 0)
				for _, filePath := range files {
					parsed, processed, warning := parseImportFile(adapter, filePath, cutoff)
					if warning != "" {
						warnings = append(warnings, warning)
					}
					if !processed {
						continue
					}
					totalFiles++
					records = append(records, parsed...)
				}
				records = postProcessor.PostProcessRecords(records)
				added, updated, skipped, recordWarnings := importParsedRecords(database, adapter.Name(), records)
				warnings = append(warnings, recordWarnings...)
				totalAdded += added
				totalUpdated += updated
				totalSkipped += skipped
				continue
			}

			for _, filePath := range files {
				records, processed, warning := parseImportFile(adapter, filePath, cutoff)
				if warning != "" {
					warnings = append(warnings, warning)
				}
				if !processed {
					continue
				}
				totalFiles++
				added, updated, skipped, recordWarnings := importParsedRecords(database, adapter.Name(), records)
				warnings = append(warnings, recordWarnings...)
				totalAdded += added
				totalUpdated += updated
				totalSkipped += skipped
			}
		}

		status := "completed"
		errorSummary := ""
		if len(warnings) > 0 {
			status = "completed_with_warnings"
			errorSummary = summarizeImportWarnings(warnings)
		}
		if err := database.FinishImportRunWithStatus(runID, totalFiles, totalAdded, totalUpdated, totalSkipped, status, errorSummary); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to record import run finish: %v\n", err)
		}

		fmt.Printf("Import complete:\n")
		fmt.Printf("  Files processed: %d\n", totalFiles)
		fmt.Printf("  Events added:    %d\n", totalAdded)
		fmt.Printf("  Events updated:  %d\n", totalUpdated)
		fmt.Printf("  Events skipped:  %d (duplicates)\n", totalSkipped)
		if len(warnings) > 0 {
			fmt.Printf("  Warnings:        %d\n", len(warnings))
		}
		return nil
	},
}

func parseImportFile(adapter adapters.Adapter, filePath string, cutoff time.Time) ([]*fingerprint.ParsedRecord, bool, string) {
	info, err := os.Stat(filePath)
	if err != nil {
		warning := fmt.Sprintf("failed to stat %s: %v", filePath, err)
		fmt.Fprintf(os.Stderr, "Warning: %s\n", warning)
		return nil, false, warning
	}
	if info.ModTime().After(cutoff) {
		stable, warning := recentFileIsStable(filePath, info)
		if warning != "" {
			fmt.Fprintf(os.Stderr, "Warning: %s\n", warning)
			return nil, false, warning
		}
		if !stable {
			return nil, false, ""
		}
	}

	records, err := adapter.ParseFile(filePath)
	if err != nil {
		warning := fmt.Sprintf("failed to parse %s: %v", filePath, err)
		fmt.Fprintf(os.Stderr, "Warning: %s\n", warning)
		return nil, true, warning
	}
	return records, true, ""
}

func recentFileIsStable(filePath string, before os.FileInfo) (bool, string) {
	time.Sleep(recentFileStabilityDelay)
	after, err := os.Stat(filePath)
	if err != nil {
		return false, fmt.Sprintf("failed to restat %s: %v", filePath, err)
	}
	return before.Size() == after.Size() && before.ModTime().Equal(after.ModTime()), ""
}

func importParsedRecords(database *db.Database, adapterName string, records []*fingerprint.ParsedRecord) (int, int, int, []string) {
	added := 0
	updated := 0
	skipped := 0
	var warnings []string
	for _, rec := range records {
		fp, strategy := fingerprint.Compute(rec)
		nowMs := time.Now().UnixMilli()

		normalized, modelProvider, _ := adapters.NormalizeModelName(rec.Model)
		provider := rec.Provider
		if provider == "" || provider == "unknown" {
			provider = modelProvider
		}
		sourceAgent := rec.Agent
		if sourceAgent == "" {
			sourceAgent = adapterName
		}
		observability := rec.ObservabilityLevel
		if observability == "" {
			observability = defaultObservability(sourceAgent)
		}
		accountingMethod := rec.TokenAccountingMethod
		if accountingMethod == "" {
			accountingMethod = defaultAccountingMethod(sourceAgent)
		}
		sourceProduct := rec.SourceProduct
		if sourceProduct == "" {
			sourceProduct = sourceProductForAgent(sourceAgent)
		}

		event := &model.UsageEvent{
			EventID:               fp,
			DedupeKey:             fp,
			DedupeStrategy:        string(strategy),
			Channel:               sourceAgent,
			Provider:              provider,
			ModelRaw:              rec.Model,
			ModelNormalized:       normalized,
			SourceAgent:           sourceAgent,
			SourceProduct:         sourceProduct,
			ObservabilityLevel:    observability,
			ModelIsFallback:       rec.ModelIsFallback,
			SourceTotalTokens:     rec.SourceTotalTokens,
			RawInputTokens:        rec.RawInputTokens,
			TokenAccountingMethod: accountingMethod,
			AccountingProfile:     rec.AccountingProfile,
			TimestampMs:           rec.TimestampMs,
			SessionID:             rec.SessionID,
			SessionPathID:         rec.SessionPathID,
			TurnID:                rec.TurnID,
			ProjectPath:           rec.ProjectPath,
			MessageID:             rec.MessageID,
			RequestID:             rec.RequestID,
			SourceFile:            rec.SourceFile,
			LineNumber:            rec.LineNumber,
			RawSHA256:             rec.RawSHA256,
			InputTokens:           rec.InputTokens,
			OutputTokens:          rec.OutputTokens,
			CacheCreationTokens:   rec.CacheCreationTokens,
			CacheReadTokens:       rec.CacheReadTokens,
			ReasoningTokens:       rec.ReasoningTokens,
			TotalTokens:           rec.TotalTokens,
			RecordedCostUSD:       rec.CostUSD,
			RawUsageJSON:          rec.RawJSON,
			ImportedAtMs:          nowMs,
			UpdatedAtMs:           nowMs,
		}

		if event.TotalTokens == 0 {
			event.TotalTokens = event.TotalTokensComputed()
		}
		applyTimingFields(event, rec)

		result, err := database.UpsertEvent(event)
		if err != nil {
			warning := fmt.Sprintf("insert error for %s:%d: %v", rec.SourceFile, rec.LineNumber, err)
			warnings = append(warnings, warning)
			fmt.Fprintf(os.Stderr, "Warning: %s\n", warning)
			continue
		}
		switch result {
		case "inserted":
			added++
		case "updated":
			updated++
		default:
			skipped++
		}
	}
	return added, updated, skipped, warnings
}

func configureImportAdapter(adapter adapters.Adapter, agentCfg *config.AgentConfig) adapters.Adapter {
	if adapter.Name() == "codex" {
		return adapters.NewCodexAdapterWithOptions(adapters.CodexOptions{
			DuplicatePolicy: agentCfg.DuplicatePolicy,
		})
	}
	return adapter
}

func sourceProductForAgent(agent string) string {
	switch agent {
	case "claude":
		return "claude-code"
	case "codex":
		return "codex-cli"
	case "copilot":
		return "copilot-otel"
	default:
		return agent
	}
}

func defaultObservability(agent string) string {
	switch agent {
	case "claude", "codex", "copilot":
		return "full"
	default:
		return "unknown"
	}
}

func defaultAccountingMethod(agent string) string {
	switch agent {
	case "claude":
		return model.AccClaudeUsageSum
	default:
		return ""
	}
}

func summarizeImportWarnings(warnings []string) string {
	const maxWarnings = 5
	const maxLen = 2000
	if len(warnings) == 0 {
		return ""
	}
	limit := len(warnings)
	if limit > maxWarnings {
		limit = maxWarnings
	}
	summary := fmt.Sprintf("%d warning(s): %s", len(warnings), strings.Join(warnings[:limit], "; "))
	if len(warnings) > limit {
		summary += fmt.Sprintf("; ... %d more", len(warnings)-limit)
	}
	if len(summary) > maxLen {
		return summary[:maxLen]
	}
	return summary
}

func applyTimingFields(event *model.UsageEvent, rec *fingerprint.ParsedRecord) {
	event.RequestStartedAtMs = positiveInt64Ptr(rec.RequestStartedAtMs)
	event.FirstTokenAtMs = positiveInt64Ptr(rec.FirstTokenAtMs)
	event.CompletedAtMs = positiveInt64Ptr(rec.CompletedAtMs)
	event.TotalDurationMs = positiveInt64Ptr(rec.TotalDurationMs)
	event.TTFTMs = positiveInt64Ptr(rec.TTFTMs)
	event.OutputDurationMs = positiveInt64Ptr(rec.OutputDurationMs)

	if event.TTFTMs == nil && event.RequestStartedAtMs != nil && event.FirstTokenAtMs != nil {
		if value := *event.FirstTokenAtMs - *event.RequestStartedAtMs; value >= 0 {
			event.TTFTMs = &value
		}
	}
	if event.OutputDurationMs == nil && event.FirstTokenAtMs != nil && event.CompletedAtMs != nil {
		if value := *event.CompletedAtMs - *event.FirstTokenAtMs; value > 0 {
			event.OutputDurationMs = &value
		}
	}
	if event.TotalDurationMs == nil && event.RequestStartedAtMs != nil && event.CompletedAtMs != nil {
		if value := *event.CompletedAtMs - *event.RequestStartedAtMs; value >= 0 {
			event.TotalDurationMs = &value
		}
	}
	if event.RequestStartedAtMs == nil && event.CompletedAtMs != nil && event.TotalDurationMs != nil {
		if value := *event.CompletedAtMs - *event.TotalDurationMs; value > 0 {
			event.RequestStartedAtMs = &value
		}
	}
	if event.FirstTokenAtMs == nil && event.RequestStartedAtMs != nil && event.TTFTMs != nil {
		if value := *event.RequestStartedAtMs + *event.TTFTMs; value > 0 {
			event.FirstTokenAtMs = &value
		}
	}
	if event.CompletedAtMs == nil && event.RequestStartedAtMs != nil && event.TotalDurationMs != nil {
		if value := *event.RequestStartedAtMs + *event.TotalDurationMs; value > 0 {
			event.CompletedAtMs = &value
		}
	}
	if event.OutputDurationMs == nil && event.TotalDurationMs != nil && event.TTFTMs != nil {
		if value := *event.TotalDurationMs - *event.TTFTMs; value > 0 {
			event.OutputDurationMs = &value
		}
	}
	if event.OutputDurationMs == nil && event.FirstTokenAtMs != nil && event.CompletedAtMs != nil {
		if value := *event.CompletedAtMs - *event.FirstTokenAtMs; value > 0 {
			event.OutputDurationMs = &value
		}
	}
	if event.OutputTPS == nil && event.OutputDurationMs != nil && *event.OutputDurationMs > 0 && event.OutputTokens > 0 {
		value := float64(event.OutputTokens) / (float64(*event.OutputDurationMs) / 1000.0)
		event.OutputTPS = &value
	}
}

func positiveInt64Ptr(value int64) *int64 {
	if value <= 0 {
		return nil
	}
	return &value
}
