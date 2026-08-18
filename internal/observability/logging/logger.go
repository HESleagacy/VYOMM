// Package logging builds a structured JSON slog.Logger with the mandatory
// correlation fields defined in METRICS_CONTRACT.md ("Logs correlation
// fields"). It also redacts values that look like secrets so that API keys
// or tokens never end up in log output.
package logging

import (
	"context"
	"log/slog"
	"os"
	"regexp"
	"strings"
)

// Fixed keys every log line should be able to carry. Individual call sites
// add only the ones relevant to that event; the logger itself does not
// force every key onto every line (that would just be noise), but New()
// pre-populates the ones known at process start (service, mode).
const (
	KeyService    = "service"
	KeyMode       = "mode"
	KeyComponent  = "component"
	KeyRunID      = "run_id"
	KeyScenarioID = "scenario_id"
	KeyIncidentID = "incident_id"
	KeyTraceID    = "trace_id"
	KeySpanID     = "span_id"
	KeyEvent      = "event"
)

// redactPattern matches common secret-shaped tokens so that even if a
// caller accidentally logs a raw config value, the secret text itself is
// not written out. This is a defense-in-depth measure, not a substitute for
// callers never logging cfg.LLMAPIKey directly.
var redactPattern = regexp.MustCompile(`(?i)(gsk_[A-Za-z0-9]+|sk-[A-Za-z0-9]{16,}|bearer\s+[A-Za-z0-9._-]+)`)

// redactingHandler wraps another slog.Handler and redacts secret-shaped
// substrings from string attribute values before they are written.
type redactingHandler struct {
	slog.Handler
}

func (h redactingHandler) Handle(ctx context.Context, r slog.Record) error {
	redacted := slog.NewRecord(r.Time, r.Level, redactString(r.Message), r.PC)
	r.Attrs(func(a slog.Attr) bool {
		redacted.AddAttrs(redactAttr(a))
		return true
	})
	return h.Handler.Handle(ctx, redacted)
}

func (h redactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		out[i] = redactAttr(a)
	}
	return redactingHandler{Handler: h.Handler.WithAttrs(out)}
}

func (h redactingHandler) WithGroup(name string) slog.Handler {
	return redactingHandler{Handler: h.Handler.WithGroup(name)}
}

func redactAttr(a slog.Attr) slog.Attr {
	if a.Value.Kind() == slog.KindString {
		return slog.String(a.Key, redactString(a.Value.String()))
	}
	return a
}

func redactString(s string) string {
	if !strings.Contains(strings.ToLower(s), "bearer") && redactPattern.FindStringIndex(s) == nil {
		return s
	}
	return redactPattern.ReplaceAllString(s, "[REDACTED]")
}

// Options configures New.
type Options struct {
	Service string
	Mode    string
	Level   slog.Level
	Writer  *os.File // defaults to os.Stdout if nil
}

// New returns a JSON structured logger pre-populated with the service and
// mode fields, wrapped in a handler that redacts secret-shaped values.
func New(opts Options) *slog.Logger {
	w := opts.Writer
	if w == nil {
		w = os.Stdout
	}
	base := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: opts.Level})
	handler := redactingHandler{Handler: base}
	logger := slog.New(handler)
	return logger.With(
		slog.String(KeyService, opts.Service),
		slog.String(KeyMode, opts.Mode),
	)
}

// LevelFromString converts the validated config log level string into a
// slog.Level. Config already restricts input to a known set, so the default
// branch here is unreachable in practice but kept safe rather than panicking.
func LevelFromString(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
