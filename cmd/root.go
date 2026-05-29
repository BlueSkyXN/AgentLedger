package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "agent-ledger",
	Short: "A local-first usage analytics tool for AI coding agents",
	Long: `AgentLedger imports usage data from multiple AI agents (Claude Code, Codex, Gemini, GitHub Copilot),
stores it in a simple SQLite analytics schema with deterministic deduplication,
and provides token, timing, TTFT, and output TPS reports.`,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(importCmd)
	rootCmd.AddCommand(exportCmd)
	rootCmd.AddCommand(mergeCmd)
	rootCmd.AddCommand(reportCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(doctorCmd)
	rootCmd.AddCommand(verifyCmd)
	rootCmd.AddCommand(vacuumCmd)
	rootCmd.AddCommand(serveCmd)
}
