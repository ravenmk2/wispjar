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
	"github.com/ravenmk2/wispjar/internal/computer"
	"github.com/ravenmk2/wispjar/internal/config"
	"github.com/ravenmk2/wispjar/internal/gateway"
	"github.com/ravenmk2/wispjar/internal/gateway/ws"
	"github.com/ravenmk2/wispjar/internal/provider"
	"github.com/ravenmk2/wispjar/internal/runtime"
	"github.com/ravenmk2/wispjar/internal/sandbox"
	"github.com/ravenmk2/wispjar/internal/session"
	"github.com/ravenmk2/wispjar/internal/skill"
	"github.com/ravenmk2/wispjar/internal/store"
	"github.com/ravenmk2/wispjar/internal/usage"
	"github.com/ravenmk2/wispjar/internal/volume"
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

	settingsSource := sandbox.NewCachedSettingsSource(sandbox.NewDBSettingsSource(db), 30*time.Second)
	var computerBackend computer.Backend
	if cfg.OpenSandbox.LifecycleURL != "" {
		computerBackend = sandbox.NewComputerBackend(sandbox.NewClient(cfg.OpenSandbox.LifecycleURL, cfg.OpenSandbox.APIKey, cfg.OpenSandbox.UseServerProxy))
	}
	volumeRepo := volume.NewGORMRepository(db)
	installationID, err := volumeRepo.InstallationID(context.Background())
	if err != nil {
		log.Fatalf("installation identity: %v", err)
	}
	var volumeService *volume.Service
	if cfg.Storage.Volumes.DockerEndpoint != "" {
		provider, err := volume.NewDocker(cfg.Storage.Volumes.DockerEndpoint)
		if err != nil {
			log.Fatalf("volume provider: %v", err)
		}
		volumeService, err = volume.NewService(volumeRepo, provider, volume.Options{
			BackendID: cfg.Storage.Volumes.BackendID, Namespace: cfg.Storage.Volumes.Namespace, NamePattern: cfg.Storage.Volumes.NamePattern,
		})
		if err != nil {
			log.Fatalf("volume service: %v", err)
		}
	}
	var volumes computer.Volumes
	if volumeService != nil {
		volumes = volumeService
	}
	computerSvc := computer.NewService(computer.NewGORMRepository(db), volumes, computerBackend, installationID, cfg.Storage.Volumes.BackendID, settingsSource)

	agentSvc := agent.NewService(db)
	sessionSvc := session.NewService(db, agentSvc)
	sessionSvc.SetComputers(computerSvc)
	skillSvc := skill.NewService(db, cfg.Storage.DataDir)
	skillCoordinator := skill.NewCoordinator(db, skillSvc)
	attachmentSvc := attachment.NewService(db, cfg.Storage.DataDir)
	eventStore := session.NewStore(db)
	eventStore.AfterAppend = usage.NewRecorder(db).OnEvents
	repo := runtime.NewSessionRepo(db)
	bus := runtime.NewBus()
	computerSvc.SetOnChange(func(row store.Computer) {
		frame := runtime.ControlFrame{Type: "computer/upsert", UserID: row.UserID, Computer: computer.View(row), ComputerID: row.ID}
		if row.Status == "deleted" {
			frame.Type = "computer/remove"
			frame.Computer = nil
		}
		bus.PublishControl(row.OrgID, frame)
	})
	orch := runtime.NewOrchestrator(runtime.Deps{
		AgentEnvs:        agentSvc.LoadEnvs,
		MaxParallelTasks: cfg.Runtime.MaxParallelTasks,
		Computers:        computerSvc,
		Skills:           skillCoordinator,
		Events:           eventStore,
		Sessions:         repo,
		Models:           provider.NewChatModelFactory(db, cfg.MasterKey(), attachmentSvc),
		Checkpoints: func(sessionID string) adk.CheckPointStore {
			return runtime.NewCheckpointStore(db, sessionID)
		},
		Bus: bus,
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
		ToolImages: func(ctx context.Context, modelID string) bool {
			var m store.Model
			if err := db.WithContext(ctx).Select("capabilities").First(&m, "id = ?", modelID).Error; err != nil {
				return false
			}
			caps := provider.Capabilities{}
			_ = json.Unmarshal(m.Capabilities, &caps)
			return caps.Vision && caps.Tools && caps.ToolImages && caps.APIProtocol == "responses"
		},
	})
	if err := orch.RecoverPendingApprovals(context.Background()); err != nil {
		log.Printf("recover pending approvals: %v", err)
	}
	computerSvc.SetRecoveryHandler(func(ctx context.Context, row store.Computer) error {
		if err := runtime.RecoverComputer(ctx, orch, row.ID); err != nil {
			return err
		}
		return skillCoordinator.RecoverComputer(ctx, row.OrgID, row.UserID, row.ID, row.Generation)
	})
	computerSvc.Start(context.Background())

	runtimeH := runtime.NewHandler(orch, sessionSvc, eventStore, repo, bus,
		func(ctx context.Context, modelID string) (string, string, bool, error) {
			var m store.Model
			if err := db.WithContext(ctx).First(&m, "id = ?", modelID).Error; err != nil {
				return "", "", false, err
			}
			caps := provider.Capabilities{}
			_ = json.Unmarshal(m.Capabilities, &caps)
			return m.ModelCode, m.ProviderID, caps.Vision, nil
		}, cfg.Storage.DataDir, attachmentSvc)
	runtimeH.SetComputers(computerSvc)
	runtimeH.SetDownloadOptions(runtime.DownloadOptions{
		MaxBytes: cfg.Storage.Downloads.MaxBytes, MaxStagedBytes: cfg.Storage.Downloads.MaxStagedBytes,
		MaxEntries: cfg.Storage.Downloads.MaxEntries, TTL: time.Duration(cfg.Storage.Downloads.TTL), PrepareTimeout: time.Duration(cfg.Storage.Downloads.PrepareTimeout),
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
		Computers: computerSvc,
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
	computerSvc.Stop()
	log.Println("wispjar stopped")
}
