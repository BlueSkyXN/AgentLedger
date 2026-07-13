package cmd

import (
	"fmt"

	"github.com/BlueSkyXN/AgentLedger/internal/config"
	"github.com/BlueSkyXN/AgentLedger/internal/db"
)

func openReadOnlyConfiguredDatabase() (*config.Config, *db.Database, error) {
	cfg, err := config.LoadReadOnly()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load config: %w", err)
	}

	database, err := db.OpenReadOnly(cfg.DBPath())
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open database: %w", err)
	}
	return cfg, database, nil
}
