package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
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
	"github.com/ravenmk2/wispjar/internal/attachment"
	"github.com/ravenmk2/wispjar/internal/auth"
	"github.com/ravenmk2/wispjar/internal/config"
	"github.com/ravenmk2/wispjar/internal/gateway"
	"github.com/ravenmk2/wispjar/internal/gateway/ws"
	"github.com/ravenmk2/wispjar/internal/provider"
	"github.com/ravenmk2/wispjar/internal/runtime"
	"github.com/ravenmk2/wispjar/internal/sandbox"
	"github.com/ravenmk2/wispjar/internal/session"
	"github.com/ravenmk2/wispjar/internal/store"
	"github.com/ravenmk2/wispjar/internal/usage"
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
			sandbox.NewCachedSettingsSource(sandbox.NewDBSettingsSource(db), 30*time.Second),
		)
		sandboxSvc.SetSessionChecker(func(ctx context.Context, sessionID string) bool {
			var n int64
			if err := db.WithContext(ctx).Model(&store.Session{}).
				Where("id = ? AND status <> ?", sessionID, session.StatusDeleted).
				Count(&n).Error; err != nil {
				log.Printf("sandbox: session exists check %s: %v", sessionID, err)
				return false
			}
			return n > 0
		})
		sandboxSvc.Start(context.Background())
	}

	agentSvc := agent.NewService(db)
	sessionSvc := session.NewService(db, agentSvc)
	attachmentSvc := attachment.NewService(db, cfg.Storage.DataDir)
	eventStore := session.NewStore(db)
	eventStore.AfterAppend = usage.NewRecorder(db).OnEvents
	repo := runtime.NewSessionRepo(db)
	bus := runtime.NewBus()
	orch := runtime.NewOrchestrator(runtime.Deps{
		Events:   eventStore,
		Sessions: repo,
		Models:   provider.NewChatModelFactory(db, cfg.MasterKey()),
		Sandbox:  sandboxSvc,
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
		ProviderType: func(ctx context.Context, modelID string) string {
			var typ string
			if err := db.WithContext(ctx).Model(&store.Model{}).
				Select("model_provider.type").
				Joins("JOIN model_provider ON model_provider.id = model.provider_id").
				Where("model.id = ?", modelID).
				Scan(&typ).Error; err != nil {
				return ""
			}
			return typ
		},
		Attachments: attachmentSvc,
		ImageSaver:  attachmentSvc,
		Vision: func(ctx context.Context, modelID string) bool {
			var m store.Model
			if err := db.WithContext(ctx).Select("capabilities").First(&m, "id = ?", modelID).Error; err != nil {
				return false
			}
			caps := provider.Capabilities{}
			_ = json.Unmarshal(m.Capabilities, &caps)
			return caps.Vision
		},
	})
	if sandboxSvc != nil {
		sandboxSvc.OnEvent(runtime.SandboxEventHook(repo, eventStore, bus))
	}
	if err := orch.RecoverPendingApprovals(context.Background()); err != nil {
		log.Printf("recover pending approvals: %v", err)
	}

	runtimeH := runtime.NewHandler(orch, sessionSvc, eventStore, repo, sandboxSvc, bus,
		func(ctx context.Context, modelID string) (string, string, bool, error) {
			var m store.Model
			if err := db.WithContext(ctx).First(&m, "id = ?", modelID).Error; err != nil {
				return "", "", false, err
			}
			caps := provider.Capabilities{}
			_ = json.Unmarshal(m.Capabilities, &caps)
			return m.ModelCode, m.ProviderID, caps.Vision, nil
		}, cfg.Storage.DataDir, attachmentSvc)
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
