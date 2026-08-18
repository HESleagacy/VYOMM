package config

import (
	"testing"
	"time"
)

func fakeEnv(values map[string]string) envLookup {
	return func(key string) (string, bool) {
		v, ok := values[key]
		return v, ok
	}
}

func TestLoad_Defaults(t *testing.T) {
	cfg, err := load(fakeEnv(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.EnvironmentMode != ModeTrial {
		t.Errorf("expected default mode trial, got %q", cfg.EnvironmentMode)
	}
	if cfg.HasRealLLMKey() {
		t.Errorf("expected no real LLM key by default")
	}
	if cfg.TelemetryRetentionHours != 24 {
		t.Errorf("expected default retention 24h, got %d", cfg.TelemetryRetentionHours)
	}
	if cfg.RetentionDuration() != 24*time.Hour {
		t.Errorf("expected retention duration 24h, got %v", cfg.RetentionDuration())
	}
	if len(cfg.CORSAllowedOrigins) != 2 {
		t.Errorf("expected 2 default CORS origins, got %v", cfg.CORSAllowedOrigins)
	}
}

func TestLoad_Overrides(t *testing.T) {
	cfg, err := load(fakeEnv(map[string]string{
		"VYOMM_ENVIRONMENT_MODE":          "nvml-mock",
		"VYOMM_LLM_API_KEY":               "real-key-value",
		"VYOMM_TELEMETRY_RETENTION_HOURS": "6",
		"VYOMM_CORS_ALLOWED_ORIGINS":      "https://a.example, https://b.example",
		"VYOMM_OTEL_TRACES_SAMPLE_RATIO":  "1.0",
		"VYOMM_LOG_LEVEL":                 "DEBUG",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.EnvironmentMode != ModeNVMLMock {
		t.Errorf("expected nvml-mock, got %q", cfg.EnvironmentMode)
	}
	if !cfg.HasRealLLMKey() {
		t.Errorf("expected real LLM key to be detected")
	}
	if cfg.TelemetryRetentionHours != 6 {
		t.Errorf("expected retention 6h, got %d", cfg.TelemetryRetentionHours)
	}
	if len(cfg.CORSAllowedOrigins) != 2 || cfg.CORSAllowedOrigins[0] != "https://a.example" {
		t.Errorf("unexpected CORS origins: %v", cfg.CORSAllowedOrigins)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("expected normalized log level debug, got %q", cfg.LogLevel)
	}
}

func TestLoad_InvalidMode(t *testing.T) {
	_, err := load(fakeEnv(map[string]string{"VYOMM_ENVIRONMENT_MODE": "definitely-not-real"}))
	if err == nil {
		t.Fatal("expected error for invalid environment mode, got nil")
	}
}

func TestLoad_InvalidRetention(t *testing.T) {
	for _, v := range []string{"0", "-1", "notanumber"} {
		_, err := load(fakeEnv(map[string]string{"VYOMM_TELEMETRY_RETENTION_HOURS": v}))
		if err == nil {
			t.Fatalf("expected error for retention hours %q, got nil", v)
		}
	}
}

func TestLoad_InvalidSampleRatio(t *testing.T) {
	for _, v := range []string{"-0.1", "1.1", "abc"} {
		_, err := load(fakeEnv(map[string]string{"VYOMM_OTEL_TRACES_SAMPLE_RATIO": v}))
		if err == nil {
			t.Fatalf("expected error for sample ratio %q, got nil", v)
		}
	}
}

func TestLoad_InvalidLogLevel(t *testing.T) {
	_, err := load(fakeEnv(map[string]string{"VYOMM_LOG_LEVEL": "verbose"}))
	if err == nil {
		t.Fatal("expected error for invalid log level, got nil")
	}
}

func TestLoad_EmptyCORSResolvesToError(t *testing.T) {
	_, err := load(fakeEnv(map[string]string{"VYOMM_CORS_ALLOWED_ORIGINS": " , ,"}))
	if err == nil {
		t.Fatal("expected error when CORS origins resolve to empty list")
	}
}
