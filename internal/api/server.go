package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/GrandRegentSarva/VYOMM/internal/config"
	"github.com/GrandRegentSarva/VYOMM/internal/observability/metrics"
	"github.com/GrandRegentSarva/VYOMM/internal/persistence"
	"github.com/GrandRegentSarva/VYOMM/internal/runbooks"
)

// Clock abstracts time.Now so tests can inject a fixed time. Production
// code uses realClock; nothing else in this package calls time.Now()
// directly, keeping every handler deterministically testable.
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now().UTC() }

// Server holds everything HTTP handlers need. It is deliberately a plain
// struct (not a global) so tests can construct one against a temp SQLite
// file and fixture runbooks without touching real infrastructure.
type Server struct {
	Store    *persistence.Store
	Runbooks *runbooks.Library
	Metrics  *metrics.Registry
	Config   config.Config
	Logger   *slog.Logger
	Clock    Clock
	Version  string
	RunID    string // the current process's run identifier, used as a default provenance run_id
}

// NewMux builds the full HTTP routing table matching API_CONTRACT.md,
// wrapped in CORS and metrics middleware.
func (s *Server) NewMux() http.Handler {
	if s.Clock == nil {
		s.Clock = realClock{}
	}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", s.withMetrics("/healthz", s.handleHealth))
	mux.HandleFunc("POST /api/v1/telemetry", s.withMetrics("/api/v1/telemetry", s.handleIngestTelemetry))
	mux.HandleFunc("GET /api/v1/telemetry", s.withMetrics("/api/v1/telemetry", s.handleGetTelemetry))
	mux.HandleFunc("GET /api/v1/forecast", s.withMetrics("/api/v1/forecast", s.handleForecast))
	mux.HandleFunc("GET /api/v1/anomalies", s.withMetrics("/api/v1/anomalies", s.handleAnomalies))
	mux.HandleFunc("GET /api/v1/incidents", s.withMetrics("/api/v1/incidents", s.handleListIncidents))
	mux.HandleFunc("POST /api/v1/incidents/{id}/decision", s.withMetrics("/api/v1/incidents/{id}/decision", s.handleDecideIncident))
	mux.HandleFunc("GET /api/v1/incidents/{id}/decisions", s.withMetrics("/api/v1/incidents/{id}/decisions", s.handleDecisions))
	mux.HandleFunc("GET /api/v1/runbook", s.withMetrics("/api/v1/runbook", s.handleRunbook))
	mux.HandleFunc("GET /api/v1/scenarios", s.withMetrics("/api/v1/scenarios", s.handleListScenarios))
	mux.HandleFunc("POST /api/v1/scenarios/{name}/run", s.withMetrics("/api/v1/scenarios/{name}/run", s.handleRunScenario))
	mux.HandleFunc("GET /api/v1/scenarios/runs/{run_id}", s.withMetrics("/api/v1/scenarios/runs/{run_id}", s.handleScenarioRun))
	if s.Metrics != nil {
		mux.Handle("GET /metrics", s.Metrics.Handler())
	}

	return s.withCORS(mux)
}

// withCORS enforces the explicit-allow-list CORS policy required by
// API_CONTRACT.md ("No wildcard in normal operation"). Only origins present
// in s.Config.CORSAllowedOrigins are ever reflected back.
func (s *Server) withCORS(next http.Handler) http.Handler {
	allowed := make(map[string]bool, len(s.Config.CORSAllowedOrigins))
	for _, o := range s.Config.CORSAllowedOrigins {
		allowed[o] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && allowed[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// withMetrics records vyomm_http_request_duration_seconds and
// vyomm_http_requests_total for every request, using the registered route
// pattern (not the raw path) so path parameters never explode label
// cardinality, per METRICS_CONTRACT.md.
func (s *Server) withMetrics(route string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := s.Clock.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next(rec, r)
		if s.Metrics == nil {
			return
		}
		duration := s.Clock.Now().Sub(start).Seconds()
		statusClass := strconv.Itoa(rec.status/100) + "xx"
		s.Metrics.HTTPDuration.WithLabelValues(r.Method, route, statusClass).Observe(duration)
		s.Metrics.HTTPRequests.WithLabelValues(r.Method, route, statusClass).Inc()
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorDTO{Error: errorBody{Code: code, Message: message, TraceID: newTraceID()}})
}

func newTraceID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "unavailable"
	}
	return hex.EncodeToString(b)
}
