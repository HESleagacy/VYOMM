package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/GrandRegentSarva/VYOMM/internal/config"
	"github.com/GrandRegentSarva/VYOMM/internal/observability/logging"
	"github.com/GrandRegentSarva/VYOMM/internal/observability/metrics"
	"github.com/GrandRegentSarva/VYOMM/internal/persistence"
	"github.com/GrandRegentSarva/VYOMM/internal/runbooks"
)

// fixedClock lets tests control "now" precisely instead of racing wall time.
type fixedClock struct{ t time.Time }

func (f fixedClock) Now() time.Time { return f.t }

func newTestServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	dir := t.TempDir()
	store, err := persistence.NewStore(filepath.Join(dir, "test.db"), 24*time.Hour, time.Now().UTC())
	if err != nil {
		t.Fatalf("unexpected error creating store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	rbDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(rbDir, "cpu.md"), []byte("CPU saturation runbook content"), 0o644); err != nil {
		t.Fatalf("write fixture runbook: %v", err)
	}
	lib, err := runbooks.Load(rbDir)
	if err != nil {
		t.Fatalf("unexpected error loading runbooks: %v", err)
	}

	cfg := config.Config{
		EnvironmentMode:    config.ModeTrial,
		CORSAllowedOrigins: []string{"http://localhost:5173"},
	}

	s := &Server{
		Store:    store,
		Runbooks: lib,
		Metrics:  metrics.New(),
		Config:   cfg,
		Logger:   logging.New(logging.Options{Service: "test", Mode: "trial"}),
		Clock:    fixedClock{t: time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)},
		Version:  "test-version",
		RunID:    "run-test-0001",
	}
	ts := httptest.NewServer(s.NewMux())
	t.Cleanup(ts.Close)
	return s, ts
}

func doJSON(t *testing.T, method, url string, body any) *http.Response {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	return res
}

func decodeJSON(t *testing.T, res *http.Response, v any) {
	t.Helper()
	defer res.Body.Close()
	if err := json.NewDecoder(res.Body).Decode(v); err != nil {
		t.Fatalf("decode response body: %v", err)
	}
}

func TestHealth_ReturnsRealModeAndVersion(t *testing.T) {
	_, ts := newTestServer(t)
	res := doJSON(t, http.MethodGet, ts.URL+"/healthz", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}
	var got healthDTO
	decodeJSON(t, res, &got)
	if got.Mode != "trial" {
		t.Errorf("expected mode=trial, got %q", got.Mode)
	}
	if got.Version != "test-version" {
		t.Errorf("expected version=test-version, got %q", got.Version)
	}
	if got.Checks["database"] != "ok" {
		t.Errorf("expected database check ok, got %q", got.Checks["database"])
	}
}

func validDeviceJSON(hostname string, cpu float64) map[string]any {
	return map[string]any{
		"hostname": hostname, "role": "router", "cpu_percent": cpu, "memory_percent": 30,
		"bandwidth_percent": 20, "temperature_c": 50, "latency_ms": 15, "packet_loss_percent": 0.1,
		"uptime_seconds": 100, "status": "healthy", "observed_at": "2026-08-18T12:00:00Z",
		"source": "synthetic", "mode": "trial",
	}
}

func TestIngestAndGetTelemetry_RoundTrips(t *testing.T) {
	_, ts := newTestServer(t)
	body := map[string]any{
		"run_id": "run-1", "scenario_id": "healthy-baseline",
		"devices": []map[string]any{validDeviceJSON("rtr-01", 42)},
		"logs":    []string{},
	}
	res := doJSON(t, http.MethodPost, ts.URL+"/api/v1/telemetry", body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on ingest, got %d", res.StatusCode)
	}
	var ingestResp telemetryIngestResponse
	decodeJSON(t, res, &ingestResp)
	if ingestResp.Accepted != 1 || ingestResp.Rejected != 0 {
		t.Fatalf("expected 1 accepted 0 rejected, got %+v", ingestResp)
	}

	getRes := doJSON(t, http.MethodGet, ts.URL+"/api/v1/telemetry", nil)
	var getResp telemetryGetResponse
	decodeJSON(t, getRes, &getResp)
	if len(getResp.Devices) != 1 || getResp.Devices[0].Hostname != "rtr-01" {
		t.Fatalf("expected 1 device rtr-01 in GET response, got %+v", getResp.Devices)
	}
	if getResp.Mode != "trial" {
		t.Errorf("expected mode=trial in GET response, got %q", getResp.Mode)
	}
}

func TestIngestTelemetry_RejectsInvalidRowsWithReason(t *testing.T) {
	_, ts := newTestServer(t)
	invalid := validDeviceJSON("rtr-02", 200) // out of range
	body := map[string]any{"run_id": "run-1", "devices": []map[string]any{invalid}}
	res := doJSON(t, http.MethodPost, ts.URL+"/api/v1/telemetry", body)
	var resp telemetryIngestResponse
	decodeJSON(t, res, &resp)
	if resp.Rejected != 1 || len(resp.Errors) != 1 {
		t.Fatalf("expected 1 rejected with 1 recorded error, got %+v", resp)
	}
}

func TestIngestTelemetry_MalformedJSONReturns400WithErrorShape(t *testing.T) {
	_, ts := newTestServer(t)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/telemetry", bytes.NewReader([]byte("{not json")))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", res.StatusCode)
	}
	var errResp errorDTO
	decodeJSON(t, res, &errResp)
	if errResp.Error.Code == "" || errResp.Error.TraceID == "" {
		t.Fatalf("expected populated error code and trace_id, got %+v", errResp)
	}
}

func TestIngestTelemetry_TriggersIncidentCorrelation(t *testing.T) {
	_, ts := newTestServer(t)
	// Memory Leak rule: memory > 94 and cpu > 80.
	device := validDeviceJSON("rtr-01", 85)
	device["memory_percent"] = 96.0
	body := map[string]any{"run_id": "run-1", "devices": []map[string]any{device}}
	doJSON(t, http.MethodPost, ts.URL+"/api/v1/telemetry", body).Body.Close()

	res := doJSON(t, http.MethodGet, ts.URL+"/api/v1/incidents", nil)
	var resp incidentsListResponse
	decodeJSON(t, res, &resp)
	if resp.Active == nil {
		t.Fatal("expected an active incident after ingesting a memory-leak-triggering device")
	}
	if resp.Active.RootCause != "Memory Leak" {
		t.Errorf("expected root_cause Memory Leak, got %q", resp.Active.RootCause)
	}
}

func TestIngestTelemetry_TriggersAnomalyDetection(t *testing.T) {
	_, ts := newTestServer(t)
	device := validDeviceJSON("rtr-01", 99)
	body := map[string]any{"run_id": "run-1", "devices": []map[string]any{device}}
	doJSON(t, http.MethodPost, ts.URL+"/api/v1/telemetry", body).Body.Close()

	res := doJSON(t, http.MethodGet, ts.URL+"/api/v1/anomalies", nil)
	var anomalies []anomalyDTO
	decodeJSON(t, res, &anomalies)
	if len(anomalies) == 0 {
		t.Fatal("expected at least one anomaly for CPU=99")
	}
}

func TestDecideIncident_ThenRecurAfterResolution(t *testing.T) {
	_, ts := newTestServer(t)
	device := validDeviceJSON("rtr-01", 85)
	device["memory_percent"] = 96.0
	body := map[string]any{"run_id": "run-1", "devices": []map[string]any{device}}
	doJSON(t, http.MethodPost, ts.URL+"/api/v1/telemetry", body).Body.Close()

	listRes := doJSON(t, http.MethodGet, ts.URL+"/api/v1/incidents", nil)
	var list incidentsListResponse
	decodeJSON(t, listRes, &list)
	firstID := list.Active.ID

	decRes := doJSON(t, http.MethodPost, ts.URL+"/api/v1/incidents/"+firstID+"/decision", decisionRequest{Status: "resolved", Actor: "tester"})
	if decRes.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 deciding incident, got %d", decRes.StatusCode)
	}
	var decided incidentDTO
	decodeJSON(t, decRes, &decided)
	if decided.Status != "resolved" {
		t.Fatalf("expected status=resolved, got %q", decided.Status)
	}

	// Audit trail check.
	trailRes := doJSON(t, http.MethodGet, ts.URL+"/api/v1/incidents/"+firstID+"/decisions", nil)
	var trail []decisionDTO
	decodeJSON(t, trailRes, &trail)
	if len(trail) != 1 || trail[0].Actor != "tester" {
		t.Fatalf("expected 1 decision by tester in audit trail, got %+v", trail)
	}

	// Re-ingest the same triggering telemetry: must recur as a NEW incident.
	doJSON(t, http.MethodPost, ts.URL+"/api/v1/telemetry", body).Body.Close()
	list2Res := doJSON(t, http.MethodGet, ts.URL+"/api/v1/incidents", nil)
	var list2 incidentsListResponse
	decodeJSON(t, list2Res, &list2)
	if list2.Active == nil {
		t.Fatal("expected a new active incident after re-triggering following resolution")
	}
	if list2.Active.ID == firstID {
		t.Fatalf("expected a NEW incident ID for the recurrence, got the same ID %q", firstID)
	}
	if len(list2.Items) != 2 {
		t.Fatalf("expected 2 incidents total (resolved + recurrence), got %d", len(list2.Items))
	}
}

func TestDecideIncident_UnknownIDReturns404(t *testing.T) {
	_, ts := newTestServer(t)
	res := doJSON(t, http.MethodPost, ts.URL+"/api/v1/incidents/INC-MISSING/decision", decisionRequest{Status: "resolved", Actor: "x"})
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", res.StatusCode)
	}
}

func TestDecideIncident_InvalidStatusReturns400(t *testing.T) {
	_, ts := newTestServer(t)
	res := doJSON(t, http.MethodPost, ts.URL+"/api/v1/incidents/INC-WHATEVER/decision", decisionRequest{Status: "banana", Actor: "x"})
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid status, got %d", res.StatusCode)
	}
}

func TestForecast_HonestlyLabeledMethod(t *testing.T) {
	_, ts := newTestServer(t)
	device := validDeviceJSON("rtr-01", 50)
	body := map[string]any{"run_id": "run-1", "devices": []map[string]any{device}}
	doJSON(t, http.MethodPost, ts.URL+"/api/v1/telemetry", body).Body.Close()

	res := doJSON(t, http.MethodGet, ts.URL+"/api/v1/forecast?device=rtr-01", nil)
	var f forecastDTO
	decodeJSON(t, res, &f)
	if f.Method != "linear-extrapolation" {
		t.Fatalf("expected honest method label, got %q", f.Method)
	}
	if len(f.Points) != 6 || f.Points[0].Label != "+5m" {
		t.Fatalf("expected 6 points starting at +5m, got %+v", f.Points)
	}
	if f.Confidence.Source != "computed" {
		t.Errorf("expected confidence source=computed, got %q", f.Confidence.Source)
	}
}

func TestRunbook_ReturnsKeywordOverlapResults(t *testing.T) {
	_, ts := newTestServer(t)
	res := doJSON(t, http.MethodGet, ts.URL+"/api/v1/runbook?query=cpu", nil)
	var results []runbookDTO
	decodeJSON(t, res, &results)
	if len(results) == 0 {
		t.Fatal("expected at least one runbook result")
	}
	if results[0].MatchMethod != "keyword-overlap" {
		t.Errorf("expected honest match_method, got %q", results[0].MatchMethod)
	}
}

func TestScenarioEndpoints_HonestlyReportNotImplemented(t *testing.T) {
	_, ts := newTestServer(t)
	res := doJSON(t, http.MethodPost, ts.URL+"/api/v1/scenarios/healthy-baseline/run", nil)
	if res.StatusCode != http.StatusNotImplemented {
		t.Fatalf("expected 501 for unimplemented scenario execution, got %d", res.StatusCode)
	}
	var errResp errorDTO
	decodeJSON(t, res, &errResp)
	if errResp.Error.Code != "not_implemented" {
		t.Errorf("expected not_implemented error code, got %q", errResp.Error.Code)
	}
}

func TestCORS_OnlyReflectsAllowedOrigin(t *testing.T) {
	_, ts := newTestServer(t)
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/healthz", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if got := res.Header.Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Errorf("expected allowed origin reflected, got %q", got)
	}

	req2, _ := http.NewRequest(http.MethodGet, ts.URL+"/healthz", nil)
	req2.Header.Set("Origin", "http://evil.example")
	res2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if got := res2.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("expected no CORS header for disallowed origin, got %q", got)
	}
}

func TestMetricsEndpoint_ExposesPrometheusFormat(t *testing.T) {
	_, ts := newTestServer(t)
	res := doJSON(t, http.MethodGet, ts.URL+"/metrics", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from /metrics, got %d", res.StatusCode)
	}
	ct := res.Header.Get("Content-Type")
	if ct == "" {
		t.Error("expected a content-type header from /metrics")
	}
	res.Body.Close()
}

func TestHTTPMetrics_RecordedAfterRequests(t *testing.T) {
	s, ts := newTestServer(t)
	doJSON(t, http.MethodGet, ts.URL+"/healthz", nil).Body.Close()
	res := doJSON(t, http.MethodGet, ts.URL+"/metrics", nil)
	defer res.Body.Close()
	var buf bytes.Buffer
	buf.ReadFrom(res.Body)
	if !bytes.Contains(buf.Bytes(), []byte("vyomm_http_requests_total")) {
		t.Fatalf("expected vyomm_http_requests_total to appear in /metrics output")
	}
	_ = s
}
