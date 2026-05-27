package cmd

import (
	"crypto/rand"
	"fmt"
	"os"
	"time"

	"github.com/BlueSkyXN/AgentLedger/internal/adapters"
	"github.com/BlueSkyXN/AgentLedger/internal/config"
	"github.com/BlueSkyXN/AgentLedger/internal/db"
	"github.com/BlueSkyXN/AgentLedger/internal/fingerprint"
	"github.com/BlueSkyXN/AgentLedger/internal/model"
	"github.com/oklog/ulid/v2"
	"github.com/spf13/cobra"
)

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

		allAdapters := adapters.AllAdapters()
		agentConfigs := map[string]*config.AgentConfig{
			"claude":  &cfg.Agents.Claude,
			"codex":   &cfg.Agents.Codex,
			"gemini":  &cfg.Agents.Gemini,
			"copilot": &cfg.Agents.Copilot,
			"qwen":    &cfg.Agents.Qwen,
		}

		for _, adapter := range allAdapters {
			agentCfg, ok := agentConfigs[adapter.Name()]
			if !ok || !agentCfg.Enabled {
				continue
			}
			adapter = configureImportAdapter(adapter, agentCfg)

			files, err := adapter.Discover(agentCfg.Paths)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: %s discover failed: %v\n", adapter.Name(), err)
				continue
			}

			if postProcessor, ok := adapter.(adapters.RecordPostProcessor); ok {
				records := make([]*fingerprint.ParsedRecord, 0)
				for _, filePath := range files {
					parsed, processed := parseImportFile(adapter, filePath, cutoff)
					if !processed {
						continue
					}
					totalFiles++
					records = append(records, parsed...)
				}
				records = postProcessor.PostProcessRecords(records)
				added, updated, skipped := importParsedRecords(database, adapter.Name(), records)
				totalAdded += added
				totalUpdated += updated
				totalSkipped += skipped
				continue
			}

			for _, filePath := range files {
				records, processed := parseImportFile(adapter, filePath, cutoff)
				if !processed {
					continue
				}
				totalFiles++
				added, updated, skipped := importParsedRecords(database, adapter.Name(), records)
				totalAdded += added
				totalUpdated += updated
				totalSkipped += skipped
			}
		}

		if err := database.FinishImportRun(runID, totalFiles, totalAdded, totalUpdated, totalSkipped); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to record import run finish: %v\n", err)
		}

		fmt.Printf("Import complete:\n")
		fmt.Printf("  Files processed: %d\n", totalFiles)
		fmt.Printf("  Events added:    %d\n", totalAdded)
		fmt.Printf("  Events updated:  %d\n", totalUpdated)
		fmt.Printf("  Events skipped:  %d (duplicates)\n", totalSkipped)
		return nil
	},
}

func parseImportFile(adapter adapters.Adapter, filePath string, cutoff time.Time) ([]*fingerprint.ParsedRecord, bool) {
	info, err := os.Stat(filePath)
	if err != nil {
		return nil, false
	}
	if info.ModTime().After(cutoff) {
		return nil, false
	}

	records, err := adapter.ParseFile(filePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to parse %s: %v\n", filePath, err)
		return nil, true
	}
	return records, true
}

func importParsedRecords(database *db.Database, adapterName string, records []*fingerprint.ParsedRecord) (int, int, int) {
	added := 0
	updated := 0
	skipped := 0
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
			fmt.Fprintf(os.Stderr, "Warning: insert error: %v\n", err)
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
	return added, updated, skipped
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
	case "qwen":
		return "qwen-cli"
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
