// Command keenetic-policy-switcher is a single self-contained binary: it
// serves the embedded frontend and proxies policy-switching commands to a
// Keenetic router over its RCI API.
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/joho/godotenv"

	"keenetic-policy-switcher/internal/api"
	"keenetic-policy-switcher/internal/favorites"
	"keenetic-policy-switcher/internal/keenetic"
	"keenetic-policy-switcher/web"
)

func main() {
	// A missing .env is not an error: in Docker, variables come from the
	// container's environment — there is no .env file there, nor should be.
	_ = godotenv.Load()

	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	client, err := keenetic.New(cfg.KeeneticHost, cfg.KeeneticLogin, cfg.KeeneticPassword, cfg.RequestTimeout)
	if err != nil {
		log.Fatalf("keenetic client: %v", err)
	}

	favoritesStore, err := favorites.New(cfg.FavoritesFile)
	if err != nil {
		log.Fatalf("favorites store (%s): %v", cfg.FavoritesFile, err)
	}

	mux := http.NewServeMux()
	api.NewHandler(routerClientAdapter{client}, favoritesStore, nil).Register(mux)
	mux.Handle("/", http.FileServerFS(web.FS))

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	log.Printf("listening on %s, router: %s", cfg.ListenAddr, cfg.KeeneticHost)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("server stopped: %v", err)
	}
}

// routerClientAdapter adapts *keenetic.Client to the narrow api.KeeneticClient
// interface, converting between the two packages' otherwise-identical
// Device/Policy types (api intentionally doesn't import keenetic — see
// internal/api/handlers.go).
type routerClientAdapter struct {
	client *keenetic.Client
}

func (a routerClientAdapter) ListDevices(ctx context.Context) ([]api.Device, error) {
	devices, err := a.client.ListDevices(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]api.Device, len(devices))
	for i, d := range devices {
		out[i] = api.Device{MAC: d.MAC, Name: d.Name, IP: d.IP, Online: d.Online, PolicyID: d.PolicyID}
	}
	return out, nil
}

func (a routerClientAdapter) ListPolicies(ctx context.Context) ([]api.Policy, error) {
	policies, err := a.client.ListPolicies(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]api.Policy, len(policies))
	for i, p := range policies {
		out[i] = api.Policy{ID: p.ID, Name: p.Name}
	}
	return out, nil
}

func (a routerClientAdapter) SetPolicy(ctx context.Context, mac, policyID string) error {
	return a.client.SetPolicy(ctx, mac, policyID)
}

type config struct {
	KeeneticHost     string
	KeeneticLogin    string
	KeeneticPassword string
	ListenAddr       string
	RequestTimeout   time.Duration
	FavoritesFile    string
}

const defaultRequestTimeout = 10 * time.Second

func loadConfig() (config, error) {
	cfg := config{
		KeeneticHost:     os.Getenv("KEENETIC_HOST"),
		KeeneticLogin:    os.Getenv("KEENETIC_LOGIN"),
		KeeneticPassword: os.Getenv("KEENETIC_PASSWORD"),
		ListenAddr:       os.Getenv("LISTEN_ADDR"),
		RequestTimeout:   defaultRequestTimeout,
		FavoritesFile:    os.Getenv("FAVORITES_FILE"),
	}

	for _, req := range []struct {
		name  string
		value string
	}{
		{"KEENETIC_HOST", cfg.KeeneticHost},
		{"KEENETIC_LOGIN", cfg.KeeneticLogin},
		{"KEENETIC_PASSWORD", cfg.KeeneticPassword},
	} {
		if req.value == "" {
			return config{}, fmt.Errorf("required environment variable %s is not set", req.name)
		}
	}

	if cfg.ListenAddr == "" {
		cfg.ListenAddr = ":8080"
	}

	if cfg.FavoritesFile == "" {
		cfg.FavoritesFile = "favorites.json"
	}

	if raw := os.Getenv("REQUEST_TIMEOUT"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return config{}, fmt.Errorf("invalid REQUEST_TIMEOUT=%q: %w", raw, err)
		}
		cfg.RequestTimeout = d
	}

	return cfg, nil
}
