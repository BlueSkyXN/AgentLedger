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
		totalRejected := 0
		var warnings []string
		var warningSourcePaths []string
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
			for _, sourcePath := range agentCfg.Paths {
				warningSourcePaths = append(warningSourcePaths, config.ExpandHome(sourcePath))
			}

			files, err := adapter.Discover(agentCfg.Paths)
			if err != nil {
				warning := fmt.Sprintf("%s discover failed: %v", adapter.Name(), err)
				warnings = append(warnings, warning)
				fmt.Fprintf(os.Stderr, "Warning: %s\n", warning)
				continue
			}
			warningSourcePaths = append(warningSourcePaths, files...)

			result := importAdapterFiles(database, adapter, files, cutoff)
			totalFiles += result.files
			totalAdded += result.added
			totalUpdated += result.updated
			totalSkipped += result.skipped
			totalRejected += result.rejected
			warnings = append(warnings, result.warnings...)
			collectImportDiagnostics(importDiagnostics, adapter)
		}

		status := "completed"
		errorSummary := ""
		if len(warnings) > 0 {
			status = "completed_with_warnings"
			errorSummary = summarizeImportWarnings(sanitizeImportWarnings(warnings, warningSourcePaths))
		}
		if err := database.FinishImportRunWithStatus(runID, totalFiles, totalAdded, totalUpdated, totalSkipped, totalRejected, status, errorSummary); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to record import run finish: %v\n", err)
		}

		fmt.Printf("Import complete:\n")
		fmt.Printf("  Files processed: %d\n", totalFiles)
		fmt.Printf("  Events added:    %d\n", totalAdded)
		fmt.Printf("  Events updated:  %d\n", totalUpdated)
		fmt.Printf("  Events skipped:  %d (duplicates)\n", totalSkipped)
		fmt.Printf("  Events rejected: %d\n", totalRejected)
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
	rejected int
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
		added, updated, skipped, rejected, recordWarnings := importParsedRecords(database, adapter.Name(), records)
		result.added += added
		result.updated += updated
		result.skipped += skipped
		result.rejected += rejected
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
		added, updated, skipped, rejected, recordWarnings := importParsedRecords(database, adapter.Name(), records)
		result.added += added
		result.updated += updated
		result.skipped += skipped
		result.rejected += rejected
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
		if strings.TrimSpace(path) == "" {
			continue
		}
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

func importParsedRecords(database *db.Database, adapterName string, records []*fingerprint.ParsedRecord) (int, int, int, int, []string) {
	added := 0
	updated := 0
	skipped := 0
	rejected := 0
	var warnings []string
	for _, rec := range records {
		normalized, modelProvider, _ := adapters.NormalizeModelName(rec.Model)
		if rec.ModelNormalized != "" {
			normalized = rec.ModelNormalized
		}
		if strings.TrimSpace(normalized) == "" {
			normalized = "unknown"
		}
		modelResolution := rec.ModelResolution
		modelIsFallback := rec.ModelIsFallback || strings.EqualFold(strings.TrimSpace(normalized), "unknown")
		if modelResolution == "" {
			switch {
			case modelIsFallback:
				modelResolution = model.ModelResolutionUnknown
			default:
				modelResolution = model.ModelResolutionDirectEvent
			}
		}
		provider := rec.Provider
		if provider == "" || provider == "unknown" {
			provider = modelProvider
		}
		channel := rec.Agent
		if channel == "" {
			channel = adapterName
		}
		observability := rec.ObservabilityLevel
		if observability == "" {
			observability = defaultObservability(channel)
		}
		accountingMethod := rec.TokenAccountingMethod
		if accountingMethod == "" {
			accountingMethod = defaultAccountingMethod(channel)
		}
		sourceProduct := rec.SourceProduct
		if sourceProduct == "" {
			sourceProduct = sourceProductForAgent(channel)
		}
		identityScope := normalizeIdentityScope(rec.IdentityScope)

		// Identity content must be computed from the same normalized facts that
		// are persisted. Adapters may intentionally leave a derived total unset.
		rec.Agent = channel
		rec.SourceProduct = sourceProduct
		rec.Provider = provider
		rec.ModelNormalized = normalized
		rec.ModelResolution = modelResolution
		rec.ModelIsFallback = modelIsFallback
		rec.TokenAccountingMethod = accountingMethod
		rec.ObservabilityLevel = observability
		rec.IdentityScope = identityScope
		if rec.TotalTokens == 0 {
			rec.TotalTokens = totalForAccountingProfile(&model.UsageEvent{
				InputTokens: rec.InputTokens, OutputTokens: rec.OutputTokens,
				ReasoningTokens: rec.ReasoningTokens, CacheCreationTokens: rec.CacheCreationTokens,
				CacheReadTokens: rec.CacheReadTokens, TokenAccountingMethod: accountingMethod,
			})
		}
		sessionKey, eventID, strategy, contentSHA256, identityErr := fingerprint.ComputeIdentity(rec)
		if identityErr != nil {
			rejected++
			warning := fmt.Sprintf("%s record rejected: %s", adapterName, fingerprint.IdentityErrorCode(identityErr))
			warnings = append(warnings, warning)
			fmt.Fprintf(os.Stderr, "Warning: %s\n", warning)
			continue
		}
		nowMs := time.Now().UnixMilli()

		event := &model.UsageEvent{
			EventID:               eventID,
			IdentityVersion:       model.IdentityVersion,
			IdentityStrategy:      string(strategy),
			IdentityScope:         identityScope,
			ContentSHA256:         contentSHA256,
			ParserVersion:         rec.ParserVersion,
			EventGranularity:      rec.Granularity,
			Channel:               channel,
			SourceProduct:         sourceProduct,
			Provider:              provider,
			ModelRaw:              rec.Model,
			ModelNormalized:       normalized,
			ModelResolution:       modelResolution,
			ObservabilityLevel:    observability,
			ModelIsFallback:       modelIsFallback,
			SourceTotalTokens:     rec.SourceTotalTokens,
			RawInputTokens:        rec.RawInputTokens,
			TokenAccountingMethod: accountingMethod,
			AccountingProfile:     rec.AccountingProfile,
			TimestampMs:           rec.TimestampMs,
			SessionKey:            sessionKey,
			SessionID:             firstNonEmpty(rec.NativeSessionID, rec.SessionID),
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
			ImportedAtMs:          nowMs,
			UpdatedAtMs:           nowMs,
		}

		result, err := database.UpsertEvent(event)
		if err != nil {
			if db.IsRejectError(err) || result == db.ReconcileRejected {
				rejected++
				warning := fmt.Sprintf("%s record rejected: %v", adapterName, err)
				warnings = append(warnings, warning)
				fmt.Fprintf(os.Stderr, "Warning: %s\n", warning)
				continue
			}
			warning := fmt.Sprintf("%s database write failed: %v", adapterName, err)
			warnings = append(warnings, warning)
			fmt.Fprintf(os.Stderr, "Warning: %s\n", warning)
			continue
		}
		switch result {
		case db.ReconcileInserted:
			added++
		case db.ReconcileUpdated:
			updated++
		case db.ReconcileRejected:
			rejected++
		default:
			skipped++
		}
	}
	return added, updated, skipped, rejected, warnings
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
	case "gemini":
		return "gemini-cli"
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

func sanitizeImportWarnings(warnings, sourcePaths []string) []string {
	sanitized := make([]string, 0, len(warnings))
	for _, warning := range warnings {
		sanitized = append(sanitized, sanitizeImportError(errors.New(warning), sourcePaths))
	}
	return sanitized
}

func totalForAccountingProfile(event *model.UsageEvent) int64 {
	switch event.TokenAccountingMethod {
	case model.AccCodexLastTokenUsage, model.AccCodexTotalDelta, model.AccCodexHeadlessUsage:
		return event.InputTokens + event.CacheCreationTokens + event.CacheReadTokens + maxInt64(event.OutputTokens, event.ReasoningTokens)
	case model.AccWorkBuddyRawUsage:
		return event.InputTokens + event.OutputTokens + event.CacheCreationTokens + event.CacheReadTokens
	default:
		return event.InputTokens + event.OutputTokens + event.ReasoningTokens + event.CacheCreationTokens + event.CacheReadTokens
	}
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}

func normalizeIdentityScope(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "global", "source", "account":
		return "global"
	default:
		return "session"
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
