// Package config reads all runtime settings from environment variables so the
// system can be re-tuned (thresholds, providers, limits) without a rebuild.
package config

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Port     string
	Env      string
	LogLevel string

	DatabaseURL   string
	StoreFallback bool // fall back to in-memory store if Postgres is unreachable

	DispatcherKey string

	OSRMURL         string
	OSRMProfile     string
	OSRMTimeout     time.Duration // alias of RoutingTimeout (kept for compatibility)
	RoutingTimeout  time.Duration // per-request limit for every external routing call (jury rule: 5 s)
	RoutingChaos    string        // "" | 503 | timeout — simulate an external outage from start-up
	RouteCacheTTL   time.Duration
	RoutingMode     string // auto | google | osrm | simulated
	WalkingSpeedMps float64

	// Google Maps Platform (optional). The browser key renders the base map
	// (Maps JavaScript API, HTTP-referrer restricted); the server key calls the
	// Directions API (walking). Without keys the system uses OSM tiles + OSRM.
	GoogleMapsKey       string
	GoogleDirectionsKey string

	CrisisWindow     time.Duration
	CrisisRadiusM    float64
	CrisisMinSignals int
	ShelterAlertPct  float64 // > this → red alert
	ShelterWarnPct   float64 // ≥ this → shelter no longer recommended, reroute citizens

	RateLimitRPS   float64
	RateLimitBurst int

	DefaultLang string

	// Security / anti-flood
	JWTSecret        string
	DispatchUsers    string        // "name:password:role,..."  roles: viewer | operator | admin
	TokenTTL         time.Duration
	TrustProxy       bool          // honour X-Forwarded-For for client IP (behind a reverse proxy)
	SOSRatePerMin    float64       // per client IP, POST /api/v1/sos
	SOSBurst         int
	SOSDedupeWindow  time.Duration // same citizen / same spot+type within window → duplicate
	FloodThreshold   int           // SOS per 10 s that switches flood mode on

	// PublicURL is used in SMS texts for the route link the citizen can open.
	PublicURL string
	// NotifyProvider selects the SMS gateway: "simulated" (default) | future: "twilio".
	NotifyProvider string
}

func Load() Config {
	return Config{
		Port:     env("PORT", "8080"),
		Env:      env("APP_ENV", "development"),
		LogLevel: env("LOG_LEVEL", "info"),

		DatabaseURL:   env("DATABASE_URL", ""),
		StoreFallback: envBool("STORE_FALLBACK", true),

		DispatcherKey: env("DISPATCHER_API_KEY", "dispatcher-secret"),

		OSRMURL:         strings.TrimRight(env("OSRM_URL", "https://router.project-osrm.org"), "/"),
		OSRMProfile:     env("OSRM_PROFILE", "driving"),
		OSRMTimeout:     envDuration("OSRM_TIMEOUT", envDuration("ROUTING_TIMEOUT", 5*time.Second)),
		RoutingTimeout:  envDuration("ROUTING_TIMEOUT", envDuration("OSRM_TIMEOUT", 5*time.Second)),
		RoutingChaos:    env("ROUTING_CHAOS", ""),
		RouteCacheTTL:   envDuration("ROUTE_CACHE_TTL", 24*time.Hour),
		RoutingMode:     env("ROUTING_MODE", "auto"),
		WalkingSpeedMps: envFloat("WALKING_SPEED_MPS", 1.3),

		GoogleMapsKey:       env("GOOGLE_MAPS_API_KEY", ""),
		GoogleDirectionsKey: env("GOOGLE_DIRECTIONS_API_KEY", env("GOOGLE_MAPS_API_KEY", "")),

		CrisisWindow:     envDuration("CRISIS_WINDOW", 5*time.Minute),
		CrisisRadiusM:    envFloat("CRISIS_RADIUS_M", 500),
		CrisisMinSignals: envInt("CRISIS_MIN_SIGNALS", 4),
		ShelterAlertPct:  envFloat("SHELTER_ALERT_PCT", 95),
		ShelterWarnPct:   envFloat("SHELTER_WARN_PCT", 90),

		RateLimitRPS:   envFloat("RATE_LIMIT_RPS", 20),
		RateLimitBurst: envInt("RATE_LIMIT_BURST", 40),

		DefaultLang: env("DEFAULT_LANG", "az"),

		JWTSecret:       env("JWT_SECRET", "change-me-in-production-9f1c"),
		DispatchUsers:   env("DISPATCH_USERS", "admin:admin123:admin,operator:operator123:operator,viewer:viewer123:viewer"),
		TokenTTL:        envDuration("TOKEN_TTL", 8*time.Hour),
		TrustProxy:      envBool("TRUST_PROXY", false),
		SOSRatePerMin:   envFloat("SOS_RATE_PER_MIN", 10),
		SOSBurst:        envInt("SOS_BURST", 5),
		SOSDedupeWindow: envDuration("SOS_DEDUPE_WINDOW", 30*time.Second),
		FloodThreshold:  envInt("SOS_FLOOD_THRESHOLD", 60),

		PublicURL:      strings.TrimRight(env("PUBLIC_URL", "http://localhost:"+env("PORT", "8080")), "/"),
		NotifyProvider: env("NOTIFY_PROVIDER", "simulated"),
	}
}

func (c Config) SlogLevel() slog.Level {
	switch strings.ToLower(c.LogLevel) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	}
	return slog.LevelInfo
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v, err := strconv.Atoi(env(key, "")); err == nil {
		return v
	}
	return def
}

func envFloat(key string, def float64) float64 {
	if v, err := strconv.ParseFloat(env(key, ""), 64); err == nil {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	if v, err := strconv.ParseBool(env(key, "")); err == nil {
		return v
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v, err := time.ParseDuration(env(key, "")); err == nil {
		return v
	}
	return def
}
