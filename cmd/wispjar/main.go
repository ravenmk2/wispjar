package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cloudwego/eino/adk"

	"github.com/ravenmk2/wispjar/internal/agent"
	"github.com/ravenmk2/wispjar/internal/auth"
	"github.com/ravenmk2/wispjar/internal/config"
	"github.com/ravenmk2/wispjar/internal/gateway"
	"github.com/ravenmk2/wispjar/internal/gateway/ws"
	"github.com/ravenmk2/wispjar/internal/provider"
	"github.com/ravenmk2/wispjar/internal/runtime"
	"github.com/ravenmk2/wispjar/internal/sandbox"
	"github.com/ravenmk2/wispjar/internal/session"
	"github.com/ravenmk2/wispjar/internal/store"
)

var version = "dev" // default when not injected; release builds set it via -ldflags -X main.version

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	if cfg.Database.DSN == "" {
		log.Fatal("database.dsn is required (set it in the config file)")
	}

	jwtSecret := cfg.Auth.JWTSecret
	if jwtSecret == "" {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			log.Fatalf("generate jwt secret: %v", err)
		}
		jwtSecret = hex.EncodeToString(b)
		log.Printf("auth.jwt_secret not set; generated a random secret (existing tokens are invalidated on restart)")
	}

	db, err := store.Open(cfg.Database.DSN)
	if err != nil {
		log.Fatalf("connect database: %v", err)
	}
	if err := store.Migrate(db); err != nil {
		log.Fatalf("migrate: %v", err)
	}
	if err := store.BootstrapAdmin(db, cfg.AdminPassword); err != nil {
		log.Fatalf("bootstrap admin: %v", err)
	}

	authSvc := auth.NewService(db, jwtSecret, cfg.TokenTTL())

	var sandboxSvc *sandbox.Service
	if cfg.OpenSandbox.LifecycleURL != "" {
		sandboxSvc = sandbox.NewService(
			sandbox.NewClient(cfg.OpenSandbox.LifecycleURL, cfg.OpenSandbox.APIKey),
			cfg.OpenSandbox.IdleTTLSeconds,
		)
		sandboxSvc.Start(context.Background())
	}

	sandboxSettings, err := sandbox.LoadSettings(context.Background(), db)
	if err != nil {
		log.Fatalf("load sandbox settings: %v", err)
	}

	agentSvc := agent.NewService(db)
	sessionSvc := session.NewService(db, agentSvc)
	eventStore := session.NewStore(db)
	repo := runtime.NewSessionRepo(db)
	bus := runtime.NewBus()
	orch := runtime.NewOrchestrator(runtime.Deps{
		Events:          eventStore,
		Sessions:        repo,
		Models:          provider.NewChatModelFactory(db, cfg.MasterKey()),
		Sandbox:         sandboxSvc,
		SandboxSettings: sandboxSettings,
		Checkpoints: func(sessionID string) adk.CheckPointStore {
			return runtime.NewCheckpointStore(db, sessionID)
		},
		Bus:     bus,
		DataDir: cfg.Storage.DataDir,
		ContextSize: func(ctx context.Context, modelID string) int {
			var m store.Model
			if err := db.WithContext(ctx).Select("context_size").First(&m, "id = ?", modelID).Error; err != nil {
				return 0
			}
			return m.ContextSize
		},
	})
	if sandboxSvc != nil {
		sandboxSvc.OnEvent(runtime.SandboxEventHook(repo, eventStore, bus))
	}
	if err := orch.RecoverPendingApprovals(context.Background()); err != nil {
		log.Printf("recover pending approvals: %v", err)
	}

	runtimeH := runtime.NewHandler(orch, sessionSvc, eventStore, repo, sandboxSvc, sandboxSettings, bus,
		func(ctx context.Context, modelID string) (string, string, error) {
			var m store.Model
			if err := db.WithContext(ctx).First(&m, "id = ?", modelID).Error; err != nil {
				return "", "", err
			}
			return m.ModelCode, m.ProviderID, nil
		})
	wsServer := ws.NewServer(ws.Deps{
		JWTSecret:      []byte(jwtSecret),
		Store:          ws.NewGormStore(db),
		Orchestrator:   orch,
		Bus:            bus,
		AllowedOrigins: cfg.Server.AllowedOrigins,
	})
	router := gateway.NewRouter(gateway.Deps{
		Config: cfg, DB: db, Auth: authSvc, Runtime: runtimeH, WS: wsServer,
		WSBus: bus, RuntimeSessions: repo,
	})

	srv := &http.Server{
		Addr:              cfg.Server.Addr,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("wispjar %s listening on %s", version, cfg.Server.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("shutdown: %v", err)
	}
	if sandboxSvc != nil {
		sandboxSvc.Stop()
	}
	log.Println("wispjar stopped")
}
