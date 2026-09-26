package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/GoDeskio/GoDesk-SMS/selfhosted/internal/auth"
	"github.com/GoDeskio/GoDesk-SMS/selfhosted/internal/httpapi"
	"github.com/GoDeskio/GoDesk-SMS/selfhosted/internal/secretbox"
	"github.com/GoDeskio/GoDesk-SMS/selfhosted/internal/store/pg"
)

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	database, err := pg.Open(ctx, cfg.databaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer database.Close()

	tokens, err := auth.NewTokens(cfg.jwtSecret)
	if err != nil {
		log.Fatal(err)
	}
	boxSecret := cfg.credentialsKey
	if boxSecret == "" {
		boxSecret = cfg.jwtSecret
	}
	box, err := secretbox.New(boxSecret)
	if err != nil {
		log.Fatal(err)
	}
	var oidc *auth.OIDC
	if cfg.oidcIssuer != "" {
		oidc = &auth.OIDC{
			Issuer:       cfg.oidcIssuer,
			ClientID:     cfg.oidcClientID,
			ClientSecret: cfg.oidcClientSecret,
			RedirectURL:  cfg.oidcRedirectURL,
		}
	}
	api := httpapi.New(httpapi.Server{
		Store:     database,
		Tokens:    tokens,
		Box:       box,
		PublicURL: cfg.publicURL,
		OIDC:      oidc,
	})
	server := &http.Server{
		Addr:              cfg.httpAddr,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	log.Printf("godesk sms listening on %s", cfg.httpAddr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

type config struct {
	httpAddr         string
	databaseURL      string
	jwtSecret        string
	credentialsKey   string
	publicURL        string
	oidcIssuer       string
	oidcClientID     string
	oidcClientSecret string
	oidcRedirectURL  string
}

func loadConfig() (config, error) {
	cfg := config{
		httpAddr:         env("HTTP_ADDR", ":8080"),
		databaseURL:      os.Getenv("DATABASE_URL"),
		jwtSecret:        os.Getenv("JWT_SECRET"),
		credentialsKey:   os.Getenv("CREDENTIALS_KEY"),
		publicURL:        env("PUBLIC_URL", "http://godesk.localhost"),
		oidcIssuer:       os.Getenv("OIDC_ISSUER"),
		oidcClientID:     os.Getenv("OIDC_CLIENT_ID"),
		oidcClientSecret: os.Getenv("OIDC_CLIENT_SECRET"),
		oidcRedirectURL:  os.Getenv("OIDC_REDIRECT_URL"),
	}
	if cfg.databaseURL == "" {
		return config{}, errors.New("DATABASE_URL is required")
	}
	if len(cfg.jwtSecret) < 32 {
		return config{}, errors.New("JWT_SECRET must be at least 32 characters")
	}
	if cfg.oidcIssuer != "" && (cfg.oidcClientID == "" || cfg.oidcClientSecret == "" || cfg.oidcRedirectURL == "") {
		return config{}, errors.New("OIDC_CLIENT_ID, OIDC_CLIENT_SECRET, and OIDC_REDIRECT_URL are required when OIDC_ISSUER is set")
	}
	return cfg, nil
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
