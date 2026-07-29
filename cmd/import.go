package cmd

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
		if err := cfg.ValidateUsageEvidenceWritePolicy(); err != nil {
			return err
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
		importDiagnostics := map[string]adapters.ImportDiagnostic{}

		allAdapters := adapters.AllAdapters()
		agentConfigs := map[string]*config.AgentConfig{
			"claude":    &cfg.Agents.Claude,
			"codex":     &cfg.Agents.Codex,
			"gemini":    &cfg.Agents.Gemini,
			"copilot":   &cfg.Agents.Copilot,
			"workbuddy": &cfg.Agents.WorkBuddy,
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

			result := importAdapterFiles(database, adapter, files, cutoff)
			totalFiles += result.files
			totalAdded += result.added
			totalUpdated += result.updated
			totalSkipped += result.skipped
			warnings = append(warnings, result.warnings...)
			collectImportDiagnostics(importDiagnostics, adapter)
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
		printImportDiagnostics(importDiagnostics)
		if len(warnings) > 0 {
			fmt.Printf("  Warnings:        %d\n", len(warnings))
		}
		return nil
	},
}

type importAdapterResult struct {
	files    int
	added    int
	updated  int
	skipped  int
	warnings []string
}

func importAdapterFiles(database *db.Database, adapter adapters.Adapter, files []string, cutoff time.Time) importAdapterResult {
	result := importAdapterResult{}
	stableFiles, stabilityWarnings := stableImportFiles(files, cutoff)
	result.warnings = append(result.warnings, stabilityWarnings...)
	if preparer, ok := adapter.(adapters.FileSetPreparer); ok {
		if err := preparer.PrepareFileSet(stableFiles); err != nil {
			warning := fmt.Sprintf("%s file-set preparation failed; adapter skipped: %s", adapter.Name(), sanitizeImportError(err, stableFiles))
			result.warnings = append(result.warnings, warning)
			fmt.Fprintf(os.Stderr, "Warning: %s\n", warning)
			return result
		}
	}

	if postProcessor, ok := adapter.(adapters.RecordPostProcessor); ok {
		records := make([]*fingerprint.ParsedRecord, 0)
		for _, filePath := range stableFiles {
			parsed, processed, warning := parseStableImportFile(adapter, filePath)
			if warning != "" {
				result.warnings = append(result.warnings, warning)
			}
			if !processed {
				continue
			}
			result.files++
			records = append(records, parsed...)
		}
		records = postProcessor.PostProcessRecords(records)
		added, updated, skipped, recordWarnings := importParsedRecords(database, adapter.Name(), records)
		result.added += added
		result.updated += updated
		result.skipped += skipped
		result.warnings = append(result.warnings, recordWarnings...)
		return result
	}

	for _, filePath := range stableFiles {
		records, processed, warning := parseStableImportFile(adapter, filePath)
		if warning != "" {
			result.warnings = append(result.warnings, warning)
		}
		if !processed {
			continue
		}
		result.files++
		added, updated, skipped, recordWarnings := importParsedRecords(database, adapter.Name(), records)
		result.added += added
		result.updated += updated
		result.skipped += skipped
		result.warnings = append(result.warnings, recordWarnings...)
	}
	return result
}

func parseImportFile(adapter adapters.Adapter, filePath string, cutoff time.Time) ([]*fingerprint.ParsedRecord, bool, string) {
	stableFiles, warnings := stableImportFiles([]string{filePath}, cutoff)
	if len(warnings) > 0 {
		return nil, false, warnings[0]
	}
	if len(stableFiles) == 0 {
		return nil, false, ""
	}
	return parseStableImportFile(adapter, stableFiles[0])
}

func stableImportFiles(files []string, cutoff time.Time) ([]string, []string) {
	stableFiles := make([]string, 0, len(files))
	var warnings []string
	for _, filePath := range files {
		info, err := os.Stat(filePath)
		if err != nil {
			warning := fmt.Sprintf("failed to stat %s: %v", filePath, err)
			warnings = append(warnings, warning)
			fmt.Fprintf(os.Stderr, "Warning: %s\n", warning)
			continue
		}
		if info.ModTime().After(cutoff) {
			stable, warning := recentFileIsStable(filePath, info)
			if warning != "" {
				warnings = append(warnings, warning)
				fmt.Fprintf(os.Stderr, "Warning: %s\n", warning)
				continue
			}
			if !stable {
				continue
			}
		}
		stableFiles = append(stableFiles, filePath)
	}
	return stableFiles, warnings
}

func parseStableImportFile(adapter adapters.Adapter, filePath string) ([]*fingerprint.ParsedRecord, bool, string) {
	var records []*fingerprint.ParsedRecord
	var parseWarnings []string
	var err error
	if warningAdapter, ok := adapter.(adapters.ParseWarningAdapter); ok {
		records, parseWarnings, err = warningAdapter.ParseFileWithWarnings(filePath)
	} else {
		records, err = adapter.ParseFile(filePath)
	}
	if err != nil {
		warning := ""
		if isQuarantinedImportError(err) {
			warning = fmt.Sprintf("%s replay child quarantined: %v", adapter.Name(), err)
		} else {
			warning = fmt.Sprintf("failed to parse %s: %v", filePath, err)
		}
		fmt.Fprintf(os.Stderr, "Warning: %s\n", warning)
		return nil, true, warning
	}
	if len(parseWarnings) > 0 {
		warning := fmt.Sprintf("%s parse warning for %s: %s", adapter.Name(), filePath, strings.Join(parseWarnings, "; "))
		fmt.Fprintf(os.Stderr, "Warning: %s\n", warning)
		return records, true, warning
	}
	return records, true, ""
}

func sanitizeImportError(err error, sourcePaths []string) string {
	if err == nil {
		return "unknown error"
	}
	message := err.Error()
	replacements := make([]string, 0, len(sourcePaths)*2)
	for _, path := range sourcePaths {
		replacements = append(replacements, path)
		if canonical, canonicalErr := filepath.Abs(filepath.Clean(path)); canonicalErr == nil {
			replacements = append(replacements, canonical)
		}
	}
	sort.Slice(replacements, func(i, j int) bool { return len(replacements[i]) > len(replacements[j]) })
	for _, path := range replacements {
		message = strings.ReplaceAll(message, path, "[source]")
	}
	return message
}

func isQuarantinedImportError(err error) bool {
	var quarantine interface{ Quarantined() bool }
	return errors.As(err, &quarantine) && quarantine.Quarantined()
}

func collectImportDiagnostics(target map[string]adapters.ImportDiagnostic, adapter adapters.Adapter) {
	provider, ok := adapter.(adapters.ImportDiagnosticsProvider)
	if !ok {
		return
	}
	for _, diagnostic := range provider.ImportDiagnostics() {
		current := target[diagnostic.Code]
		current.Code = diagnostic.Code
		if current.Unit == "" {
			current.Unit = diagnostic.Unit
		}
		current.Count += diagnostic.Count
		current.Events += diagnostic.Events
		current.Tokens += diagnostic.Tokens
		target[diagnostic.Code] = current
	}
}

func printImportDiagnostics(diagnostics map[string]adapters.ImportDiagnostic) {
	codes := make([]string, 0, len(diagnostics))
	for code, diagnostic := range diagnostics {
		if diagnostic.Count != 0 || diagnostic.Events != 0 || diagnostic.Tokens != 0 {
			codes = append(codes, code)
		}
	}
	if len(codes) == 0 {
		return
	}
	sort.Strings(codes)
	fmt.Println("  Source diagnostics:")
	for _, code := range codes {
		fmt.Printf("    %s\n", formatImportDiagnostic(diagnostics[code]))
	}
}

func formatImportDiagnostic(diagnostic adapters.ImportDiagnostic) string {
	parts := []string{diagnostic.Code + ":"}
	switch diagnostic.Unit {
	case adapters.ImportDiagnosticUnitCount:
		parts = append(parts, fmt.Sprintf("count=%d", diagnostic.Count))
	case adapters.ImportDiagnosticUnitEvents:
		parts = append(parts, fmt.Sprintf("events=%d", diagnostic.Events))
	case adapters.ImportDiagnosticUnitTokens:
		parts = append(parts, fmt.Sprintf("tokens=%d", diagnostic.Tokens))
	case adapters.ImportDiagnosticUnitUsage:
		parts = append(parts,
			fmt.Sprintf("events=%d", diagnostic.Events),
			fmt.Sprintf("tokens=%d", diagnostic.Tokens),
		)
	default:
		if diagnostic.Count != 0 {
			parts = append(parts, fmt.Sprintf("count=%d", diagnostic.Count))
		}
		if diagnostic.Events != 0 {
			parts = append(parts, fmt.Sprintf("events=%d", diagnostic.Events))
		}
		if diagnostic.Tokens != 0 {
			parts = append(parts, fmt.Sprintf("tokens=%d", diagnostic.Tokens))
		}
	}
	return strings.Join(parts, " ")
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
		if rec.ModelNormalized != "" {
			normalized = rec.ModelNormalized
		}
		modelResolution := rec.ModelResolution
		if modelResolution == "" {
			switch {
			case rec.ModelIsFallback || strings.EqualFold(strings.TrimSpace(normalized), "unknown") || strings.TrimSpace(normalized) == "":
				modelResolution = model.ModelResolutionUnknown
			default:
				modelResolution = model.ModelResolutionDirectEvent
			}
		}
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
			ModelResolution:       modelResolution,
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
			RequestCount:          rec.RequestCount,
			RecordedCostUSD:       rec.CostUSD,
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
	case "workbuddy":
		return "workbuddy"
	default:
		return agent
	}
}

func defaultObservability(agent string) string {
	switch agent {
	case "claude", "codex", "copilot", "workbuddy":
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
