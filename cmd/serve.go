package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kexi/pypi-mirror/config"
	"github.com/kexi/pypi-mirror/db"
	"github.com/kexi/pypi-mirror/logging"
	"github.com/kexi/pypi-mirror/server"
	"github.com/spf13/cobra"
)

var serveConfigFile string

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the PyPI mirror HTTP server",
	RunE:  runServe,
}

func init() {
	serveCmd.Flags().StringVarP(&serveConfigFile, "config", "c", "config.yaml", "Path to config file")
}

func runServe(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load(serveConfigFile)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	database, err := db.Open(cfg.Database.DSN)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer database.Close()

	var logger *logging.AccessLogger
	if cfg.Log.Path != "" {
		if err := os.MkdirAll(dirOf(cfg.Log.Path), 0755); err != nil {
			return fmt.Errorf("create log dir: %w", err)
		}
		logger, err = logging.NewAccessLogger(cfg.Log.Path, cfg.Log.Format)
		if err != nil {
			return fmt.Errorf("open log: %w", err)
		}
		defer logger.Close()
		logging.SetupLogReopen(logger)
	}

	srv, err := server.New(cfg, database, logger)
	if err != nil {
		return fmt.Errorf("create server: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Fprintln(os.Stderr, "shutting down...")
		cancel()
		// Give a grace period
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}()

	return srv.Run(ctx)
}

func dirOf(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[:i]
		}
	}
	return "."
}
