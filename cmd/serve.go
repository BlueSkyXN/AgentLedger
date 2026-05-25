package cmd

import (
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/BlueSkyXN/AgentLedger/internal/config"
	"github.com/BlueSkyXN/AgentLedger/internal/control"
	"github.com/BlueSkyXN/AgentLedger/internal/db"
	"github.com/spf13/cobra"
)

var serveAddr string
var serveStaticDir string

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the local read-only web panel",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := validateLoopbackAddr(serveAddr); err != nil {
			return err
		}

		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		database, err := db.Open(cfg.DBPath())
		if err != nil {
			return fmt.Errorf("failed to open database: %w", err)
		}
		defer database.Close()

		server := control.NewServer(cfg, database, control.Options{StaticDir: serveStaticDir})
		fmt.Printf("AgentLedger panel: http://%s\n", serveAddr)
		fmt.Printf("Panel assets: %s\n", server.AssetMode())
		return http.ListenAndServe(serveAddr, server.Handler())
	},
}

func init() {
	serveCmd.Flags().StringVar(&serveAddr, "addr", "127.0.0.1:8765", "Loopback address for the local web panel")
	serveCmd.Flags().StringVar(&serveStaticDir, "static-dir", "web/dist", "Built web panel directory")
}

func validateLoopbackAddr(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid --addr %q: %w", addr, err)
	}
	if strings.TrimSpace(port) == "" {
		return fmt.Errorf("invalid --addr %q: missing port", addr)
	}
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("serve only supports loopback hosts in this release; got %q", host)
}
