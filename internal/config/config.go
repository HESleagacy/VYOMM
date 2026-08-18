// Package config loads VYOMM runtime configuration from environment
// variables with explicit, documented defaults. It never silently invents
// values that affect provenance (e.g. environment mode) — trial mode is the
// only default, and it is always explicit in the returned Config.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// EnvironmentMode identifies which of the three documented execution modes
// the system is running in. It directly drives the provenance labels
// (source/mode) attached to every telemetry value.
type EnvironmentMode string

const (
	ModeTrial    EnvironmentMode = "trial"
	ModeNVMLMock EnvironmentMode = "nvml-mock"
	ModeRealGPU  EnvironmentMode = "real-gpu"
)

func (m EnvironmentMode) Valid() bool {
	switch m {
	case ModeTrial, ModeNVMLMock, ModeRealGPU:
		return true
	default:
		return false
	}
}

// Config holds all runtime configuration. Fields are intentionally explicit
// (no map[string]any) so that missing/invalid environment variables are
// caught at startup, not at first use.
type Config struct {
	EnvironmentMode EnvironmentMode

	// LLM provider (optional; empty API key means the deterministic
	// offline fallback analysis is used, and is reported truthfully via
	// /healthz rather than pretending to be "live").
	LLMAPIKey  string
	LLMBaseURL string
	LLMModel   string

	// Persistence
	SQLitePath              string
	TelemetryRetentionHours int

	// HTTP
	APIAddr            string
	CORSAllowedOrigins []string

	// Observability
	OTelExporterEndpoint  string
	OTelTracesSampleRatio float64
	LogLevel              string
}

// envLookup abstracts os.LookupEnv so tests can inject a fake environment
// without mutating process-global state.
type envLookup func(key string) (string, bool)

// Load builds a Config from the process environment. It returns an error
// (never a silently-defaulted invalid value) if a set variable is malformed.
func Load() (Config, error) {
	return load(os.LookupEnv)
}

func load(lookup envLookup) (Config, error) {
	cfg := Config{
		EnvironmentMode:         ModeTrial,
		LLMBaseURL:              "https://api.groq.com/openai/v1",
		LLMModel:                "llama-3.3-70b-versatile",
		SQLitePath:              "./data/vyomm.db",
		TelemetryRetentionHours: 24,
		APIAddr:                 ":8080",
		CORSAllowedOrigins:      []string{"http://localhost:5173", "http://localhost:8080"},
		OTelExporterEndpoint:    "http://localhost:4318",
		OTelTracesSampleRatio:   0.2,
		LogLevel:                "info",
	}

	if v, ok := lookup("VYOMM_ENVIRONMENT_MODE"); ok && v != "" {
		mode := EnvironmentMode(v)
		if !mode.Valid() {
			return Config{}, fmt.Errorf("config: invalid VYOMM_ENVIRONMENT_MODE %q (must be trial, nvml-mock, or real-gpu)", v)
		}
		cfg.EnvironmentMode = mode
	}

	if v, ok := lookup("VYOMM_LLM_API_KEY"); ok {
		cfg.LLMAPIKey = v
	}
	if v, ok := lookup("VYOMM_LLM_BASE_URL"); ok && v != "" {
		cfg.LLMBaseURL = v
	}
	if v, ok := lookup("VYOMM_LLM_MODEL"); ok && v != "" {
		cfg.LLMModel = v
	}

	if v, ok := lookup("VYOMM_SQLITE_PATH"); ok && v != "" {
		cfg.SQLitePath = v
	}
	if v, ok := lookup("VYOMM_TELEMETRY_RETENTION_HOURS"); ok && v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return Config{}, fmt.Errorf("config: invalid VYOMM_TELEMETRY_RETENTION_HOURS %q (must be a positive integer)", v)
		}
		cfg.TelemetryRetentionHours = n
	}

	if v, ok := lookup("VYOMM_API_ADDR"); ok && v != "" {
		cfg.APIAddr = v
	}
	if v, ok := lookup("VYOMM_CORS_ALLOWED_ORIGINS"); ok && v != "" {
		parts := strings.Split(v, ",")
		origins := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				origins = append(origins, p)
			}
		}
		if len(origins) == 0 {
			return Config{}, fmt.Errorf("config: VYOMM_CORS_ALLOWED_ORIGINS set but resolved to zero origins")
		}
		cfg.CORSAllowedOrigins = origins
	}

	if v, ok := lookup("VYOMM_OTEL_EXPORTER_OTLP_ENDPOINT"); ok && v != "" {
		cfg.OTelExporterEndpoint = v
	}
	if v, ok := lookup("VYOMM_OTEL_TRACES_SAMPLE_RATIO"); ok && v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f < 0 || f > 1 {
			return Config{}, fmt.Errorf("config: invalid VYOMM_OTEL_TRACES_SAMPLE_RATIO %q (must be a float in [0,1])", v)
		}
		cfg.OTelTracesSampleRatio = f
	}
	if v, ok := lookup("VYOMM_LOG_LEVEL"); ok && v != "" {
		switch strings.ToLower(v) {
		case "debug", "info", "warn", "error":
			cfg.LogLevel = strings.ToLower(v)
		default:
			return Config{}, fmt.Errorf("config: invalid VYOMM_LOG_LEVEL %q (must be debug, info, warn, or error)", v)
		}
	}

	return cfg, nil
}

// RetentionDuration returns the configured telemetry retention window as a
// time.Duration for use by the persistence pruning routine.
func (c Config) RetentionDuration() time.Duration {
	return time.Duration(c.TelemetryRetentionHours) * time.Hour
}

// HasRealLLMKey reports whether a usable (non-empty) LLM API key is
// configured. It intentionally does not validate the key against the
// provider — that only happens on first real call, and failure there falls
// back to the deterministic offline analysis rather than crashing.
func (c Config) HasRealLLMKey() bool {
	return strings.TrimSpace(c.LLMAPIKey) != ""
}
