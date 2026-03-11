package cmd

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kexi/pypi-mirror/config"
	"github.com/kexi/pypi-mirror/db"
	"github.com/kexi/pypi-mirror/syncer"
	"github.com/spf13/cobra"
)

var syncConfigFile string

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Sync PyPI index and manage package cache",
	RunE:  runSync,
}

func init() {
	syncCmd.Flags().StringVarP(&syncConfigFile, "config", "c", "config.yaml", "Path to config file")
}

func runSync(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load(syncConfigFile)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	database, err := db.Open(cfg.Database.DSN)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer database.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("received signal, cancelling sync...")
		cancel()
	}()

	start := time.Now()

	// Phase 1: Index sync
	log.Println("starting index sync...")
	s, err := syncer.NewSyncer(cfg, database)
	if err != nil {
		return fmt.Errorf("create syncer: %w", err)
	}
	if err := s.SyncIndex(ctx); err != nil {
		return fmt.Errorf("index sync: %w", err)
	}
	log.Printf("index sync completed in %s", time.Since(start).Round(time.Second))

	// Phase 2: Cache management
	log.Println("starting cache management...")
	cm, err := syncer.NewCacheManager(cfg, database)
	if err != nil {
		return fmt.Errorf("create cache manager: %w", err)
	}
	if err := cm.Run(ctx); err != nil {
		return fmt.Errorf("cache management: %w", err)
	}

	log.Printf("sync completed in %s", time.Since(start).Round(time.Second))
	return nil
}
