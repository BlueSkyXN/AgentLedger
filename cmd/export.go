package cmd

import (
	"fmt"
	"io"
	"os"

	"github.com/BlueSkyXN/AgentLedger/internal/config"
	"github.com/spf13/cobra"
)

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export database to portable .aldb file",
	RunE: func(cmd *cobra.Command, args []string) error {
		output, _ := cmd.Flags().GetString("output")
		if output == "" {
			output = "agent-ledger-export.aldb"
		}

		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		src, err := os.Open(cfg.DBPath())
		if err != nil {
			return fmt.Errorf("failed to open database: %w", err)
		}
		defer src.Close()

		dst, err := os.Create(output)
		if err != nil {
			return fmt.Errorf("failed to create output file: %w", err)
		}
		defer dst.Close()

		n, err := io.Copy(dst, src)
		if err != nil {
			return fmt.Errorf("failed to copy database: %w", err)
		}

		fmt.Printf("✓ Exported %d bytes to %s\n", n, output)
		return nil
	},
}

func init() {
	exportCmd.Flags().StringP("output", "o", "", "Output file path")
}
