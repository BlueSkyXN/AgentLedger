package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var verifyCmd = &cobra.Command{
	Use:   "verify",
	Short: "Verify database integrity",
	RunE: func(cmd *cobra.Command, args []string) error {
		_, database, err := openReadOnlyConfiguredDatabase()
		if err != nil {
			return err
		}
		defer database.Close()

		var result string
		err = database.Conn().QueryRow("PRAGMA integrity_check").Scan(&result)
		if err != nil {
			return fmt.Errorf("integrity check failed: %w", err)
		}

		if result == "ok" {
			fmt.Println("✓ Database integrity check passed")
			return nil
		}
		return fmt.Errorf("database integrity check failed: %s", result)
	},
}
