package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/BlueSkyXN/AgentLedger/internal/adapters"
	"github.com/BlueSkyXN/AgentLedger/internal/config"
	"github.com/spf13/cobra"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor [agent]",
	Short: "Run diagnostics",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}
		if len(args) == 1 && strings.EqualFold(args[0], "codex") {
			return runCodexDoctor(cfg)
		}

		fmt.Println("AgentLedger Doctor")
		fmt.Println("==================")
		fmt.Printf("Config path:   %s\n", config.ConfigPath())
		fmt.Printf("Database path: %s\n", cfg.DBPath())

		_, dbErr := os.Stat(cfg.DBPath())
		fmt.Printf("Database exists: %v\n", dbErr == nil)

		fmt.Println("\nConfigured agents:")

		agentConfigs := map[string]*config.AgentConfig{
			"claude":  &cfg.Agents.Claude,
			"codex":   &cfg.Agents.Codex,
			"copilot": &cfg.Agents.Copilot,
			"gemini":  &cfg.Agents.Gemini,
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
	totalCoverage, ttftCoverage, tpsCoverage := configured.TimingCoverage()

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
	fmt.Printf("  task timing:       %d\n", diag.TaskCompleteWithTiming)
	fmt.Printf("  task TTFT:         %d\n", diag.TaskCompleteWithTTFT)

	fmt.Println("\nParsed usage, configured policy:")
	printCodexRecordStats("configured", configured)
	fmt.Printf("  timing coverage: total=%.2f%% ttft=%.2f%% tps=%.2f%%\n", totalCoverage*100, ttftCoverage*100, tpsCoverage*100)

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

func printCodexRecordStats(label string, stats adapters.CodexRecordStats) {
	fmt.Printf("  %s: events=%d total=%d input=%d raw_input=%d cache_read=%d output=%d reasoning=%d timing=%d ttft=%d tps=%d\n",
		label,
		stats.Events,
		stats.TotalTokens,
		stats.InputTokens,
		stats.RawInputTokens,
		stats.CacheReadTokens,
		stats.OutputTokens,
		stats.ReasoningTokens,
		stats.TotalDurationEvents,
		stats.TTFTEvents,
		stats.OutputTPSEvents,
	)
}
