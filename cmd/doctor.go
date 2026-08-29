package cmd

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/BlueSkyXN/AgentLedger/internal/adapters"
	"github.com/BlueSkyXN/AgentLedger/internal/config"
	"github.com/BlueSkyXN/AgentLedger/internal/fingerprint"
	"github.com/spf13/cobra"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor [agent]",
	Short: "Run diagnostics",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.LoadReadOnly()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}
		if len(args) == 1 {
			switch strings.ToLower(args[0]) {
			case "codex":
				return runCodexDoctor(cfg)
			case "cursor":
				return runCursorDoctor(cfg)
			}
		}

		fmt.Println("AgentLedger Doctor")
		fmt.Println("==================")
		fmt.Printf("Config path:   %s\n", config.ConfigPath())
		fmt.Printf("Database path: %s\n", cfg.DBPath())

		_, dbErr := os.Stat(cfg.DBPath())
		fmt.Printf("Database exists: %v\n", dbErr == nil)

		fmt.Println("\nConfigured agents:")

		agentConfigs := map[string]*config.AgentConfig{
			"claude":    &cfg.Agents.Claude,
			"codex":     &cfg.Agents.Codex,
			"cursor":    &cfg.Agents.Cursor,
			"copilot":   &cfg.Agents.Copilot,
			"gemini":    &cfg.Agents.Gemini,
			"workbuddy": &cfg.Agents.WorkBuddy,
		}

		allAdapters := adapters.AllAdapters()
		for _, adapter := range allAdapters {
			agentCfg, ok := agentConfigs[adapter.Name()]
			if !ok || !agentCfg.Enabled {
				fmt.Printf("  %s - disabled\n", adapter.Name())
				continue
			}
			files, _ := adapter.Discover(agentCfg.Paths)
			fmt.Printf("  %s - %d files found\n", adapter.Name(), len(files))
		}

		return nil
	},
}

func runCodexDoctor(cfg *config.Config) error {
	diag, err := adapters.AnalyzeCodex(cfg.Agents.Codex.Paths, cfg.Agents.Codex.DuplicatePolicy)
	if err != nil {
		return err
	}
	configured := diag.ConfiguredStats()

	fmt.Println("AgentLedger Doctor - Codex")
	fmt.Println("==========================")
	fmt.Printf("Configured paths: %s\n", strings.Join(diag.Paths, ", "))
	fmt.Printf("Duplicate policy: %s\n", diag.DuplicatePolicy)
	fmt.Printf("Files found:      %d\n", diag.Files)
	fmt.Printf("JSONL lines:      %d\n", diag.Lines)
	fmt.Printf("Bad JSON lines:   %d\n", diag.BadJSON)

	fmt.Println("\nRaw Codex events:")
	fmt.Printf("  token_count:       %d\n", diag.TokenCountEvents)
	fmt.Printf("  last_token_usage:  %d\n", diag.LastTokenUsageEvents)
	fmt.Printf("  total_token_usage: %d\n", diag.TotalTokenUsageEvents)
	fmt.Printf("  both last+total:   %d\n", diag.LastAndTotalUsageEvents)
	fmt.Printf("  total-only:        %d\n", diag.TotalOnlyUsageEvents)
	fmt.Printf("  all-zero usage:    %d\n", diag.AllZeroUsageEvents)
	fmt.Printf("  task_complete:     %d\n", diag.TaskCompleteEvents)
	fmt.Printf("  task turn IDs:     %d\n", diag.TaskCompleteWithTurnID)

	printCodexReplayDiagnostics(diag.ReplayDiagnostics)

	fmt.Println("\nParsed usage, configured policy:")
	printCodexRecordStats("configured", configured)

	fmt.Println("\nPolicy comparison:")
	printCodexRecordStats("ledger", diag.LedgerStats)
	printCodexRecordStats("ccusage_compatible", diag.CCUsageCompatibleStats)
	fmt.Printf("  ccusage delta over ledger: events=%d tokens=%d\n", diag.DuplicateDeltaEvents(), diag.DuplicateDeltaTokens())

	fmt.Println("\nTop models:")
	for _, item := range configured.TopModels(10) {
		fmt.Printf("  %s: %d events\n", item.Model, item.Count)
	}
	return nil
}

type cursorDoctorModel struct {
	model  string
	events int
	tokens int64
}

func runCursorDoctor(cfg *config.Config) error {
	adapter := adapters.NewCursorAdapter()
	files, err := adapter.Discover(cfg.Agents.Cursor.Paths)
	if err != nil {
		return err
	}

	var records []*fingerprint.ParsedRecord
	var warnings []string
	for _, path := range files {
		parsed, parseWarnings, err := adapter.ParseFileWithWarnings(path)
		if err != nil {
			return err
		}
		records = append(records, parsed...)
		warnings = append(warnings, parseWarnings...)
	}
	rawEvents := len(records)
	records = adapter.PostProcessRecords(records)

	models := make(map[string]*cursorDoctorModel)
	var inputTokens, cacheReadTokens, cacheWriteTokens, outputTokens, totalTokens int64
	for _, record := range records {
		inputTokens += record.InputTokens
		cacheReadTokens += record.CacheReadTokens
		cacheWriteTokens += record.CacheCreationTokens
		outputTokens += record.OutputTokens
		totalTokens += record.TotalTokens
		item := models[record.ModelNormalized]
		if item == nil {
			item = &cursorDoctorModel{model: record.ModelNormalized}
			models[record.ModelNormalized] = item
		}
		item.events++
		item.tokens += record.TotalTokens
	}
	modelList := make([]cursorDoctorModel, 0, len(models))
	for _, item := range models {
		modelList = append(modelList, *item)
	}
	sort.Slice(modelList, func(i, j int) bool {
		if modelList[i].tokens != modelList[j].tokens {
			return modelList[i].tokens > modelList[j].tokens
		}
		return modelList[i].model < modelList[j].model
	})

	fmt.Println("AgentLedger Doctor - Cursor")
	fmt.Println("===========================")
	fmt.Printf("Configured paths:    %s\n", strings.Join(cfg.Agents.Cursor.Paths, ", "))
	fmt.Printf("Agent Exec files:    %d\n", len(files))
	fmt.Printf("Non-zero usage rows: %d\n", rawEvents)
	fmt.Printf("Deduped events:      %d\n", len(records))
	for _, diagnostic := range adapter.ImportDiagnostics() {
		if diagnostic.Events > 0 {
			fmt.Printf("Semantic duplicates: %d\n", diagnostic.Events)
		}
	}
	fmt.Printf("Parse warnings:      %d\n", len(warnings))
	fmt.Println("\nCanonical tokens:")
	fmt.Printf("  input:       %d\n", inputTokens)
	fmt.Printf("  cache read:  %d\n", cacheReadTokens)
	fmt.Printf("  cache write: %d\n", cacheWriteTokens)
	fmt.Printf("  output:      %d\n", outputTokens)
	fmt.Printf("  total:       %d\n", totalTokens)
	if len(warnings) > 0 {
		fmt.Println("\nWarnings:")
		for _, warning := range warnings {
			fmt.Printf("  %s\n", warning)
		}
	}
	fmt.Println("\nTop models:")
	for index, item := range modelList {
		if index == 10 {
			break
		}
		fmt.Printf("  %s: events=%d tokens=%d\n", item.model, item.events, item.tokens)
	}
	return nil
}

func printCodexReplayDiagnostics(diagnostics []adapters.ImportDiagnostic) {
	fmt.Println("\nCodex fork replay:")
	for _, diagnostic := range diagnostics {
		fmt.Printf("  %s\n", formatImportDiagnostic(diagnostic))
	}
}

func printCodexRecordStats(label string, stats adapters.CodexRecordStats) {
	fmt.Printf("  %s: events=%d total=%d input=%d raw_input=%d cache_read=%d output=%d reasoning=%d\n",
		label,
		stats.Events,
		stats.TotalTokens,
		stats.InputTokens,
		stats.RawInputTokens,
		stats.CacheReadTokens,
		stats.OutputTokens,
		stats.ReasoningTokens,
	)
}
