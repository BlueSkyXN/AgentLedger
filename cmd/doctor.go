package cmd

import (
	"fmt"
	"os"

	"github.com/BlueSkyXN/AgentLedger/internal/adapters"
	"github.com/BlueSkyXN/AgentLedger/internal/config"
	"github.com/spf13/cobra"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Run diagnostics",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		fmt.Println("AgentLedger Doctor")
		fmt.Println("==================")
		fmt.Printf("Config path:   %s\n", config.ConfigPath())
		fmt.Printf("Database path: %s\n", cfg.DBPath())

		_, dbErr := os.Stat(cfg.DBPath())
		fmt.Printf("Database exists: %v\n", dbErr == nil)

		fmt.Println("\nConfigured agents:")

		agentConfigs := map[string]*config.AgentConfig{
			"claude": &cfg.Agents.Claude,
			"codex":  &cfg.Agents.Codex,
			"gemini": &cfg.Agents.Gemini,
			"qwen":   &cfg.Agents.Qwen,
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
