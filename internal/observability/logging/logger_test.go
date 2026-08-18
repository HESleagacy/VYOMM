package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTestLogger builds a logger writing to a temp file so we can read back
// and parse the JSON output for assertions.
func newTestLogger(t *testing.T, opts Options) (*slog.Logger, func() string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "log.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create temp log file: %v", err)
	}
	t.Cleanup(func() { f.Close() })
	opts.Writer = f
	logger := New(opts)
	return logger, func() string {
		f.Sync()
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read temp log file: %v", err)
		}
		return string(b)
	}
}

func TestNew_EmitsJSONWithServiceAndMode(t *testing.T) {
	logger, read := newTestLogger(t, Options{Service: "vyomm-api", Mode: "trial", Level: slog.LevelInfo})
	logger.Info("startup complete", slog.String(KeyComponent, "server"), slog.String(KeyRunID, "run-001"))

	line := strings.TrimSpace(read())
	var parsed map[string]any
	if err := json.Unmarshal([]byte(line), &parsed); err != nil {
		t.Fatalf("expected valid JSON line, got %q: %v", line, err)
	}
	if parsed[KeyService] != "vyomm-api" {
		t.Errorf("expected service=vyomm-api, got %v", parsed[KeyService])
	}
	if parsed[KeyMode] != "trial" {
		t.Errorf("expected mode=trial, got %v", parsed[KeyMode])
	}
	if parsed[KeyComponent] != "server" {
		t.Errorf("expected component=server, got %v", parsed[KeyComponent])
	}
	if parsed["msg"] != "startup complete" {
		t.Errorf("expected msg to be preserved, got %v", parsed["msg"])
	}
}

func TestNew_RedactsSecretShapedValues(t *testing.T) {
	logger, read := newTestLogger(t, Options{Service: "vyomm-api", Mode: "trial", Level: slog.LevelInfo})
	logger.Info("llm call", slog.String("detail", "Authorization: Bearer gsk_abcdEFGH12345token"))

	out := read()
	if strings.Contains(out, "gsk_abcdEFGH12345token") {
		t.Fatalf("expected secret to be redacted, got raw log: %s", out)
	}
	if !strings.Contains(out, "[REDACTED]") {
		t.Fatalf("expected redaction marker in log output: %s", out)
	}
}

func TestNew_RedactsMessageItself(t *testing.T) {
	logger, read := newTestLogger(t, Options{Service: "vyomm-api", Mode: "trial", Level: slog.LevelInfo})
	logger.Error("upstream call failed with key sk-1234567890abcdef1234")

	out := read()
	if strings.Contains(out, "sk-1234567890abcdef1234") {
		t.Fatalf("expected secret in message to be redacted, got: %s", out)
	}
}

func TestLevelFromString(t *testing.T) {
	cases := map[string]slog.Level{
		"debug":   slog.LevelDebug,
		"info":    slog.LevelInfo,
		"warn":    slog.LevelWarn,
		"error":   slog.LevelError,
		"unknown": slog.LevelInfo,
	}
	for input, want := range cases {
		if got := LevelFromString(input); got != want {
			t.Errorf("LevelFromString(%q) = %v, want %v", input, got, want)
		}
	}
}

func TestNew_RespectsLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	dir := t.TempDir()
	path := filepath.Join(dir, "level.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer f.Close()
	logger := New(Options{Service: "svc", Mode: "trial", Level: slog.LevelWarn, Writer: f})
	logger.Info("should not appear")
	logger.Warn("should appear")
	f.Sync()
	b, _ := os.ReadFile(path)
	buf.Write(b)
	content := buf.String()
	if strings.Contains(content, "should not appear") {
		t.Errorf("expected info-level log to be filtered out at warn level")
	}
	if !strings.Contains(content, "should appear") {
		t.Errorf("expected warn-level log to be present")
	}
}
