package cmd

import (
	"errors"
	"fmt"
	"io"

	"github.com/BlueSkyXN/AgentLedger/internal/config"
	"github.com/BlueSkyXN/AgentLedger/internal/db"
	"github.com/spf13/cobra"
)

var (
	compactRawDryRun bool
	compactRawApply  bool
)

var compactRawCmd = &cobra.Command{
	Use:   "compact-raw",
	Short: "Compact stored raw usage into minimal usage evidence",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return executeCompactRaw(compactRawDryRun, compactRawApply, cmd.OutOrStdout())
	},
}

func init() {
	compactRawCmd.Flags().BoolVar(&compactRawDryRun, "dry-run", false, "inspect compactable evidence without writing")
	compactRawCmd.Flags().BoolVar(&compactRawApply, "apply", false, "compact recognized evidence in resumable batches")
}

func executeCompactRaw(dryRun, apply bool, out io.Writer) error {
	if dryRun == apply {
		return errors.New("exactly one of --dry-run or --apply is required")
	}

	var (
		cfg *config.Config
		err error
	)
	if dryRun {
		cfg, err = config.LoadReadOnly()
	} else {
		cfg, err = config.Load()
	}
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	if err := cfg.ValidateUsageEvidenceWritePolicy(); err != nil {
		return err
	}

	opener := db.OpenReadOnlyV2
	if apply {
		opener = db.OpenReadWriteV2
	}
	database, err := opener(cfg.DBPath())
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer database.Close()

	if dryRun {
		stats, err := database.InspectRawEvidence()
		printRawEvidenceStats(out, "Raw evidence compaction dry-run", stats, false)
		if err != nil {
			return fmt.Errorf("inspect raw usage evidence: %w", err)
		}
		return nil
	}

	stats, err := database.CompactRawEvidence()
	printRawEvidenceStats(out, "Raw evidence compaction apply", stats, true)
	if err != nil {
		return fmt.Errorf("compact raw usage evidence: %w", err)
	}
	return nil
}

func printRawEvidenceStats(out io.Writer, title string, stats db.RawEvidenceStats, applied bool) {
	reduction := stats.RawBytesBefore - stats.RawBytesAfter
	fmt.Fprintf(out, "%s:\n", title)
	fmt.Fprintf(out, "  Candidates:          %d\n", stats.Candidates)
	fmt.Fprintf(out, "  Already compact:     %d\n", stats.AlreadyCompacted)
	fmt.Fprintf(out, "  Empty:               %d\n", stats.Empty)
	fmt.Fprintf(out, "  Unknown preserved:   %d\n", stats.UnknownPreserved)
	fmt.Fprintf(out, "  Identity protected:  %d\n", stats.IdentityProtected)
	fmt.Fprintf(out, "  Logical bytes before: %d\n", stats.RawBytesBefore)
	fmt.Fprintf(out, "  Logical bytes after:  %d\n", stats.RawBytesAfter)
	fmt.Fprintf(out, "  Logical byte change:  %d\n", reduction)
	if applied {
		fmt.Fprintf(out, "  Rows updated:         %d\n", stats.Updated)
		fmt.Fprintf(out, "  Batches completed:    %d\n", stats.BatchesCompleted)
		fmt.Fprintf(out, "  Remaining candidates: %d\n", stats.RemainingCandidates)
	}
}
