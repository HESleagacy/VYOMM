# `internal/scenarios` Package Specification

> **Status:** Authoritative Go interface and design spec for `internal/scenarios`.
> **Author:** Thinker agent, Round 3.
> **Target Implementer:** Controller agent.

---

## 1. Package Overview & Responsibilities

The `internal/scenarios` package provides deterministic scenario generation, execution, and verification capabilities. It connects `cmd/vyomm-simulator`, `cmd/vyomm-eval`, and `internal/api` endpoints (`POST /api/v1/scenarios/{name}/run` and `GET /api/v1/scenarios/runs/{run_id}`).

Key responsibilities:
1. Define the `Scenario` interface and concrete implementations for all 9 scenarios defined in `docs/scenarios.md`.
2. Generate deterministic `telemetry.DeviceTelemetry` batches based on scenario seed.
3. Provide a `Runner` to execute scenarios against a live HTTP server or in-memory server instance.
4. Compare actual HTTP API responses (`/api/v1/anomalies`, `/api/v1/incidents`) against expected scenario outcomes.

---

## 2. Core Go Interfaces & Types

```go
package scenarios

import (
	"context"
	"time"

	"github.com/GrandRegentSarva/VYOMM/internal/detection"
	"github.com/GrandRegentSarva/VYOMM/internal/incidents"
	"github.com/GrandRegentSarva/VYOMM/internal/telemetry"
)

// ExpectedOutcome defines the ground truth expectations for a scenario execution.
type ExpectedOutcome struct {
	Anomalies []ExpectedAnomaly `json:"anomalies"`
	Incidents []ExpectedIncident `json:"incidents"`
}

type ExpectedAnomaly struct {
	Device   string             `json:"device"`
	Signal   string             `json:"signal"`
	Severity detection.Severity `json:"severity"`
}

type ExpectedIncident struct {
	OccurrenceKey string             `json:"occurrence_key"`
	RootCause     string             `json:"root_cause"`
	Severity      incidents.Severity `json:"severity"`
	Sequence      int                `json:"sequence"`
}

// Scenario represents one deterministic simulation scenario.
type Scenario interface {
	Name() string
	Seed() int64
	NeedsHAMi() bool
	Generate(now time.Time) []telemetry.DeviceTelemetry
	Expected() ExpectedOutcome
}

// Step defines one phase of a scenario execution (e.g. for multi-step recurrence scenarios).
type Step struct {
	Telemetry []telemetry.DeviceTelemetry
	Action    *DecisionAction // Optional human action (e.g. resolve incident)
	WaitMs    int
}

type DecisionAction struct {
	IncidentKey string
	Status      incidents.Status
	Actor       string
}

// RunResult captures the complete execution record of a scenario run.
type RunResult struct {
	RunID             string            `json:"run_id"`
	ScenarioName      string            `json:"scenario_name"`
	Status            string            `json:"status"` // "completed", "failed"
	StartedAt         time.Time         `json:"started_at"`
	CompletedAt       time.Time         `json:"completed_at"`
	AcceptedRows      int               `json:"accepted_rows"`
	RejectedRows      int               `json:"rejected_rows"`
	DetectedAnomalies []detection.Anomaly `json:"detected_anomalies"`
	ActiveIncidents   []incidents.Incident `json:"active_incidents"`
	AllIncidents      []incidents.Incident `json:"all_incidents"`
	Error             string            `json:"error,omitempty"`
}

// Runner executes scenarios against a target VYOMM API base URL.
type Runner struct {
	BaseURL    string
	HTTPClient HTTPClient
}

type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}
```

---

## 3. Implementation Specification for Scenario Types

The catalog in `docs/scenarios.md` must be registered in a thread-safe registry:

```go
var Registry = map[string]func() Scenario{
	"healthy-baseline":                    NewHealthyBaseline,
	"gpu-memory-pressure-allocation":     NewGPUMemoryPressure,
	"gpu-core-contention-request":        NewGPUCoreContention,
	"oversubscription-attempt":            NewOversubscriptionAttempt,
	"multi-gpu-request":                   NewMultiGPURequest,
	"unschedulable-workload":              NewUnschedulableWorkload,
	"scheduler-or-metrics-endpoint-failure": NewSchedulerFailure,
	"telemetry-scrape-interruption":      NewScrapeInterruption,
	"incident-recurrence":                 NewIncidentRecurrence,
}
```

---

## 4. API Endpoint Handlers (`internal/api`)

When `internal/scenarios` is implemented by Controller, update `internal/api/handlers.go` endpoints:

### 4.1 `GET /api/v1/scenarios`
Returns the list of available scenarios and their metadata:

```json
[
  {
    "name": "healthy-baseline",
    "description": "Stable device telemetry producing zero anomalies and zero incidents",
    "seed": 1001,
    "needs_hami": false
  },
  {
    "name": "incident-recurrence",
    "description": "Trigger incident, resolve it, re-trigger to verify new occurrence creation",
    "seed": 9001,
    "needs_hami": false
  }
]
```

### 4.2 `POST /api/v1/scenarios/{name}/run`
Executes the named scenario asynchronously (or synchronously for fast synthetic scenarios) and returns HTTP 202 / 200 with `RunResult`:

```json
{
  "run_id": "run-scen-9001-20260818201500",
  "scenario_name": "incident-recurrence",
  "status": "completed",
  "started_at": "2026-08-18T20:15:00Z",
  "completed_at": "2026-08-18T20:15:01Z",
  "accepted_rows": 2,
  "rejected_rows": 0,
  "detected_anomalies": [
    {
      "id": "anom-a1b2c3d4e5",
      "score": 1.0,
      "severity": "critical",
      "device": "rtr-01",
      "signal": "memory_pressure",
      "detected_at": "2026-08-18T20:15:00Z"
    }
  ],
  "active_incidents": [
    {
      "id": "INC-f1e2d3-000002",
      "occurrence_key": "Memory Leak:rtr-01",
      "occurrence_sequence": 2,
      "severity": "high",
      "root_cause": "Memory Leak",
      "status": "active"
    }
  ]
}
```

### 4.3 `GET /api/v1/scenarios/runs/{run_id}`
Retrieves a previously stored `RunResult` by ID from the scenario run history store.
