package cmd

import (
	"fmt"

	"github.com/BlueSkyXN/AgentLedger/internal/config"
	"github.com/BlueSkyXN/AgentLedger/internal/db"
	"github.com/BlueSkyXN/AgentLedger/internal/model"
	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize database and config",
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

		dev, err := model.CurrentDevice()
		if err != nil {
			return fmt.Errorf("failed to get device info: %w", err)
		}

		if err := database.UpsertDevice(dev); err != nil {
			return fmt.Errorf("failed to register device: %w", err)
		}

		fmt.Println("✓ Config created at:", config.ConfigPath())
		fmt.Println("✓ Database created at:", cfg.DBPath())
		fmt.Printf("✓ Device registered: %s (%s/%s)\n", dev.DeviceID, dev.OS, dev.Arch)
		return nil
	},
}
