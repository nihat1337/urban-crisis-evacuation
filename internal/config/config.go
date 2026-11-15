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
	OSRMTimeout     time.Duration
	RoutingMode     string // auto | osrm | simulated
	WalkingSpeedMps float64

	CrisisWindow     time.Duration
	CrisisRadiusM    float64
	CrisisMinSignals int
	ShelterAlertPct  float64 // > this → red alert
	ShelterWarnPct   float64 // ≥ this → shelter no longer recommended, reroute citizens

	RateLimitRPS   float64
	RateLimitBurst int

	DefaultLang string
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
		OSRMTimeout:     envDuration("OSRM_TIMEOUT", 4*time.Second),
		RoutingMode:     env("ROUTING_MODE", "auto"),
		WalkingSpeedMps: envFloat("WALKING_SPEED_MPS", 1.3),

		CrisisWindow:     envDuration("CRISIS_WINDOW", 5*time.Minute),
		CrisisRadiusM:    envFloat("CRISIS_RADIUS_M", 500),
		CrisisMinSignals: envInt("CRISIS_MIN_SIGNALS", 4),
		ShelterAlertPct:  envFloat("SHELTER_ALERT_PCT", 95),
		ShelterWarnPct:   envFloat("SHELTER_WARN_PCT", 90),

		RateLimitRPS:   envFloat("RATE_LIMIT_RPS", 20),
		RateLimitBurst: envInt("RATE_LIMIT_BURST", 40),

		DefaultLang: env("DEFAULT_LANG", "az"),
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
