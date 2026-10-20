// wispjar-migrate is an explicit maintenance CLI. It never runs SQL migrations
// or the legacy reaper; without -apply it performs only inventory.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/ravenmk2/wispjar/internal/computer"
	"github.com/ravenmk2/wispjar/internal/config"
	"github.com/ravenmk2/wispjar/internal/sandbox"
	"github.com/ravenmk2/wispjar/internal/store"
	"github.com/ravenmk2/wispjar/internal/volume"
	"github.com/ravenmk2/wispjar/internal/workspacemigration"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	configPath := flag.String("config", "config.yaml", "deployment configuration")
	sessionIDs := flag.String("sessions", "", "explicit comma-separated session IDs")
	apply := flag.Bool("apply", false, "perform migration; otherwise inventory only")
	confirm := flag.Bool("confirm-stopped", false, "confirm legacy and previous migration commands have stopped")
	tempDir := flag.String("temp-dir", "", "local directory for bounded migration archives (default OS temp)")
	flag.Parse()
	if strings.TrimSpace(*sessionIDs) == "" {
		return fmt.Errorf("-sessions is required; implicit all-session migration is not supported")
	}
	ids := strings.Split(*sessionIDs, ",")
	for i := range ids {
		ids[i] = strings.TrimSpace(ids[i])
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if cfg.Database.DSN == "" || cfg.OpenSandbox.LifecycleURL == "" {
		return fmt.Errorf("database and OpenSandbox configuration are required")
	}
	db, err := store.Open(cfg.Database.DSN)
	if err != nil {
		return err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	legacy := workspacemigration.NewSDKLegacy(cfg.OpenSandbox.LifecycleURL, cfg.OpenSandbox.APIKey, cfg.OpenSandbox.UseServerProxy)
	var targets workspacemigration.Targets
	if *apply {
		if !*confirm {
			return workspacemigration.ErrConfirmationRequired
		}
		repo := volume.NewGORMRepository(db)
		installation, err := repo.InstallationID(ctx)
		if err != nil {
			return err
		}
		provider, err := volume.NewDocker(cfg.Storage.Volumes.DockerEndpoint)
		if err != nil {
			return err
		}
		volumes, err := volume.NewService(repo, provider, volume.Options{BackendID: cfg.Storage.Volumes.BackendID, Namespace: cfg.Storage.Volumes.Namespace, NamePattern: cfg.Storage.Volumes.NamePattern})
		if err != nil {
			return err
		}
		backend := sandbox.NewComputerBackend(sandbox.NewClient(cfg.OpenSandbox.LifecycleURL, cfg.OpenSandbox.APIKey, cfg.OpenSandbox.UseServerProxy))
		computers := computer.NewService(computer.NewGORMRepository(db), volumes, backend, installation, cfg.Storage.Volumes.BackendID, sandbox.NewDBSettingsSource(db))
		targets = workspacemigration.NewComputerTargets(db, computers, legacy)
	}
	service := workspacemigration.NewService(db, legacy, targets, *tempDir)
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if !*apply {
		inventory, err := service.Inventory(ctx, ids)
		if err != nil {
			return err
		}
		return encoder.Encode(inventory)
	}
	var failures []string
	for _, sessionID := range ids {
		op, err := service.Migrate(ctx, workspacemigration.Request{SessionID: sessionID, ConfirmCommandsStopped: *confirm})
		output := map[string]any{"session_id": sessionID, "operation": op}
		if err != nil {
			output["error"] = err.Error()
			failures = append(failures, sessionID)
		}
		if err := encoder.Encode(output); err != nil {
			return err
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("migration failed or blocked for %s; inspect operation state and retry only after confirming remote commands stopped", strings.Join(failures, ","))
	}
	return nil
}
