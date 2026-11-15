// Command server runs the Urban Crisis Crowd Evacuation Engine.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nihat1337/urban-crisis-evacuation/internal/api"
	"github.com/nihat1337/urban-crisis-evacuation/internal/auth"
	"github.com/nihat1337/urban-crisis-evacuation/internal/config"
	"github.com/nihat1337/urban-crisis-evacuation/internal/crisis"
	"github.com/nihat1337/urban-crisis-evacuation/internal/events"
	"github.com/nihat1337/urban-crisis-evacuation/internal/notify"
	"github.com/nihat1337/urban-crisis-evacuation/internal/routing"
	"github.com/nihat1337/urban-crisis-evacuation/internal/seed"
	"github.com/nihat1337/urban-crisis-evacuation/internal/service"
	"github.com/nihat1337/urban-crisis-evacuation/internal/store"
	"github.com/nihat1337/urban-crisis-evacuation/internal/store/memory"
	"github.com/nihat1337/urban-crisis-evacuation/internal/store/postgres"
)

// version is injected at build time: -ldflags "-X main.version=..."
var version = "dev"

func main() {
	cfg := config.Load()
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.SlogLevel()}))
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	st := openStore(ctx, cfg, log)
	defer st.Close()

	if err := seed.Apply(ctx, st); err != nil {
		log.Error("seed failed", "err", err)
		os.Exit(1)
	}

	sim := routing.NewSimulated(cfg.WalkingSpeedMps)
	osrm := routing.NewOSRM(cfg.OSRMURL, cfg.OSRMProfile, cfg.RoutingTimeout)
	osrm.SpeedMps = cfg.WalkingSpeedMps // evacuation is on foot: comparable durations across providers
	router := routing.NewChain(cfg.RoutingMode, osrm, sim)
	router.Timeout = cfg.RoutingTimeout
	router.Cache = routing.NewRouteCache(cfg.RouteCacheTTL, 5000)
	if cfg.GoogleDirectionsKey != "" {
		router.Google = routing.NewGoogleDirections(cfg.GoogleDirectionsKey, cfg.RoutingTimeout)
		log.Info("routing: Google Routes enabled (walking)")
	}
	if cfg.RoutingChaos != "" {
		router.SetChaos(cfg.RoutingChaos)
		log.Warn("routing: simulated outage enabled from config", "chaos", cfg.RoutingChaos)
	}
	log.Info("routing", "mode", cfg.RoutingMode, "timeout", cfg.RoutingTimeout.String(), "cache_ttl", cfg.RouteCacheTTL.String())

	hub := events.NewHub()
	engine := crisis.New(crisis.Config{
		Window: cfg.CrisisWindow, RadiusM: cfg.CrisisRadiusM,
		MinSignals: cfg.CrisisMinSignals, ShelterAlertPct: cfg.ShelterAlertPct,
	}, st)
	svc := service.New(service.Config{ShelterWarnPct: cfg.ShelterWarnPct, MaxAlternatives: 2,
		DedupeWindow: cfg.SOSDedupeWindow, FloodThreshold: cfg.FloodThreshold}, st, router, engine, hub, log).
		WithNotifier(notify.NewLogSender(log), cfg.PublicURL) // NOTIFY_PROVIDER=simulated; a real gateway plugs in here
	log.Info("notifications", "provider", cfg.NotifyProvider, "public_url", cfg.PublicURL)
	svc.StartMonitors(ctx) // pushes routing.status events when the external API goes down / comes back

	handler := api.New(api.Config{
		DispatcherKey: cfg.DispatcherKey, RateLimitRPS: cfg.RateLimitRPS, RateLimitBurst: cfg.RateLimitBurst,
		DefaultLang: cfg.DefaultLang, Version: version, GoogleMapsKey: cfg.GoogleMapsKey,
		JWTSecret: []byte(cfg.JWTSecret), Users: auth.ParseUsers(cfg.DispatchUsers), TokenTTL: cfg.TokenTTL,
		TrustProxy: cfg.TrustProxy, SOSRatePerMin: cfg.SOSRatePerMin, SOSBurst: cfg.SOSBurst,
	}, svc, hub, log)
	if cfg.JWTSecret == "change-me-in-production-9f1c" {
		log.Warn("JWT_SECRET is the default demo value — set a real secret in production")
	}
	log.Info("security", "users", len(auth.ParseUsers(cfg.DispatchUsers)), "sos_rate_per_min", cfg.SOSRatePerMin,
		"dedupe_window", cfg.SOSDedupeWindow.String(), "flood_threshold_10s", cfg.FloodThreshold, "trust_proxy", cfg.TrustProxy)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		log.Info("server listening", "addr", srv.Addr, "version", version, "store", st.Name(),
			"routing", cfg.RoutingMode, "env", cfg.Env)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("listen", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

func openStore(ctx context.Context, cfg config.Config, log *slog.Logger) store.Store {
	if cfg.DatabaseURL == "" {
		log.Info("store: in-memory (DATABASE_URL not set)")
		return memory.New()
	}
	pg, err := postgres.New(ctx, cfg.DatabaseURL)
	if err == nil {
		log.Info("store: postgres")
		return pg
	}
	if cfg.StoreFallback {
		log.Warn("postgres unavailable, falling back to in-memory store", "err", err)
		return memory.New()
	}
	log.Error("postgres connection failed", "err", err)
	os.Exit(1)
	return nil
}
