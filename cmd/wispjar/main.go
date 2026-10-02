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

	"github.com/ravenmk2/wispjar/internal/auth"
	"github.com/ravenmk2/wispjar/internal/config"
	"github.com/ravenmk2/wispjar/internal/gateway"
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
	router := gateway.NewRouter(gateway.Deps{Config: cfg, DB: db, Auth: authSvc})

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
	log.Println("wispjar stopped")
}
