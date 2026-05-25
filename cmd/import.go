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

					normalized, modelProvider, _ := adapters.NormalizeModelName(rec.Model)
					provider := rec.Provider
					if provider == "" || provider == "unknown" {
						provider = modelProvider
					}

					event := &model.UsageEvent{
						EventID:             fp,
						DedupeKey:           fp,
						DedupeStrategy:      string(strategy),
						Channel:             rec.Agent,
						Provider:            provider,
						ModelRaw:            rec.Model,
						ModelNormalized:     normalized,
						TimestampMs:         rec.TimestampMs,
						SessionID:           rec.SessionID,
						ProjectPath:         rec.ProjectPath,
						MessageID:           rec.MessageID,
						RequestID:           rec.RequestID,
						SourceFile:          rec.SourceFile,
						LineNumber:          rec.LineNumber,
						RawSHA256:           rec.RawSHA256,
						InputTokens:         rec.InputTokens,
						OutputTokens:        rec.OutputTokens,
						CacheCreationTokens: rec.CacheCreationTokens,
						CacheReadTokens:     rec.CacheReadTokens,
						ReasoningTokens:     rec.ReasoningTokens,
						TotalTokens:         rec.TotalTokens,
						RecordedCostUSD:     rec.CostUSD,
						RawUsageJSON:        rec.RawJSON,
						ImportedAtMs:        nowMs,
						UpdatedAtMs:         nowMs,
					}

					if event.TotalTokens == 0 {
						event.TotalTokens = event.TotalTokensComputed()
					}
					applyTimingFields(event, rec)

					result, err := database.UpsertEvent(event)
					if err != nil {
						fmt.Fprintf(os.Stderr, "Warning: insert error: %v\n", err)
						continue
					}
					switch result {
					case "inserted":
						totalAdded++
					case "updated":
						totalUpdated++
					default:
						totalSkipped++
					}
				}
			}
		}

		if err := database.FinishImportRun(runID, totalFiles, totalAdded, totalUpdated, totalSkipped); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to record import run finish: %v\n", err)
		}

		fmt.Printf("Import complete:\n")
		fmt.Printf("  Files processed: %d\n", totalFiles)
		fmt.Printf("  Events added:    %d\n", totalAdded)
		fmt.Printf("  Events updated:  %d\n", totalUpdated)
		fmt.Printf("  Events skipped:  %d (duplicates)\n", totalSkipped)
		return nil
	},
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
