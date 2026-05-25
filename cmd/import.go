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

		dev, err := model.CurrentDevice()
		if err != nil {
			return fmt.Errorf("failed to get device info: %w", err)
		}
		if err := database.UpsertDevice(dev); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to register device: %v\n", err)
		}

		entropy := ulid.Monotonic(rand.Reader, 0)
		runID := ulid.MustNew(ulid.Timestamp(time.Now()), entropy).String()
		if err := database.StartImportRun(runID, dev.DeviceID); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to record import run start: %v\n", err)
		}

		gracePeriod := time.Duration(cfg.Import.GracingMinutes) * time.Minute
		cutoff := time.Now().Add(-gracePeriod)

		totalFiles := 0
		totalAdded := 0
		totalSkipped := 0

		allAdapters := adapters.AllAdapters()
		agentConfigs := map[string]*config.AgentConfig{
			"claude": &cfg.Agents.Claude,
			"codex":  &cfg.Agents.Codex,
			"gemini": &cfg.Agents.Gemini,
			"qwen":   &cfg.Agents.Qwen,
		}

		for _, adapter := range allAdapters {
			agentCfg, ok := agentConfigs[adapter.Name()]
			if !ok || !agentCfg.Enabled {
				continue
			}

			files, err := adapter.Discover(agentCfg.Paths)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: %s discover failed: %v\n", adapter.Name(), err)
				continue
			}

			for _, filePath := range files {
				info, err := os.Stat(filePath)
				if err != nil {
					continue
				}
				if info.ModTime().After(cutoff) {
					continue
				}

				totalFiles++
				records, err := adapter.ParseFile(filePath)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Warning: failed to parse %s: %v\n", filePath, err)
					continue
				}

				for _, rec := range records {
					fp, strategy := fingerprint.Compute(rec)
					nowMs := time.Now().UnixMilli()

					normalized, provider, family := adapters.NormalizeModelName(rec.Model)

					event := &model.UsageEvent{
						EventFingerprint:    fp,
						DedupeKey:           fp,
						FingerprintStrategy: string(strategy),
						OriginDeviceID:      dev.DeviceID,
						FirstSeenDeviceID:   dev.DeviceID,
						LastSeenDeviceID:    dev.DeviceID,
						Agent:               rec.Agent,
						Provider:            rec.Provider,
						SourceChannel:       "local",
						SourceKind:          "log",
						ModelRaw:            rec.Model,
						ModelNormalized:     normalized,
						ModelProvider:       provider,
						ModelFamily:         family,
						TimestampMs:         rec.TimestampMs,
						SessionID:           rec.SessionID,
						MessageID:           rec.MessageID,
						RequestID:           rec.RequestID,
						InputTokens:         rec.InputTokens,
						OutputTokens:        rec.OutputTokens,
						CacheCreationTokens: rec.CacheCreationTokens,
						CacheReadTokens:     rec.CacheReadTokens,
						ReasoningTokens:     rec.ReasoningTokens,
						TotalTokens:         rec.TotalTokens,
						CostUSD:             rec.CostUSD,
						RawUsageJSON:        rec.RawJSON,
						RawSHA256:           rec.RawSHA256,
						CreatedAtMs:         nowMs,
						UpdatedAtMs:         nowMs,
					}

					if event.TotalTokens == 0 {
						event.TotalTokens = event.InputTokens + event.OutputTokens + event.CacheCreationTokens + event.CacheReadTokens + event.ReasoningTokens
					}

					inserted, err := database.InsertEvent(event)
					if err != nil {
						fmt.Fprintf(os.Stderr, "Warning: insert error: %v\n", err)
						continue
					}
					if inserted {
						totalAdded++
					} else {
						totalSkipped++
					}
				}
			}
		}

		if err := database.FinishImportRun(runID, totalFiles, totalAdded, totalSkipped); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to record import run finish: %v\n", err)
		}

		fmt.Printf("Import complete:\n")
		fmt.Printf("  Files processed: %d\n", totalFiles)
		fmt.Printf("  Events added:    %d\n", totalAdded)
		fmt.Printf("  Events skipped:  %d (duplicates)\n", totalSkipped)
		return nil
	},
}
