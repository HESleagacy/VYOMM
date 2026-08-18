# VYOMM API Contract (authoritative — do not deviate without updating this file)

Status: DRAFT v1, defined during Phase 1. This is the single source of truth for
anyone building the frontend, evaluator, or scenario runner against the Go API.
If the Go implementation and this file ever disagree, this file wins until a
commit updates both together.

Base URL (local): `http://localhost:8080`

All responses are `application/json`. All timestamps are RFC3339 UTC
(`2026-08-18T12:00:00Z`). All mutating endpoints require `Content-Type:
application/json`.

## Provenance envelope (mandatory for every displayed metric/value)

Every value that could be mistaken for a real hardware measurement MUST be
wrapped like this — in API responses, not just the UI:

```json
{
  "value": 67.2,
  "unit": "percent",
  "source": "synthetic",
  "mode": "trial",
  "observed_at": "2026-08-18T12:00:00Z",
  "run_id": "run-2026-08-18-0001"
}
```

- `source` ∈ `synthetic | mock | real`
- `mode` ∈ `trial | nvml-mock | real-gpu`
- If a physical measurement is not available in the current mode, the API
  returns `"value": null, "unavailable_reason": "Unavailable in simulated mode"`
  instead of a manufactured number. Clients must render that reason, not `0`.

## Endpoints

### `GET /healthz`
Returns real dependency status, not a static string.
```json
{
  "status": "ok",
  "mode": "trial",
  "checks": {
    "database": "ok",
    "llm_provider": "fallback",
    "otel_exporter": "disabled"
  },
  "version": "0.1.0",
  "run_id": "run-2026-08-18-0001"
}
```

### `POST /api/v1/telemetry`
Ingest a batch of device telemetry (used by the simulator).
Request:
```json
{
  "run_id": "run-2026-08-18-0001",
  "scenario_id": "healthy-baseline",
  "devices": [
    {
      "hostname": "rtr-01",
      "role": "router",
      "cpu_percent": 42.1,
      "memory_percent": 38.0,
      "bandwidth_percent": 20.0,
      "temperature_c": 51.0,
      "latency_ms": 14.2,
      "packet_loss_percent": 0.1,
      "uptime_seconds": 3600,
      "status": "healthy",
      "observed_at": "2026-08-18T12:00:00Z",
      "source": "synthetic",
      "mode": "trial"
    }
  ],
  "logs": ["optional free-text log lines"]
}
```
Response: `{"accepted": 1, "rejected": 0, "errors": []}` — rejected entries are
listed with a reason; ingestion never silently drops invalid rows (see
`vyomm_ingestion_errors_total` metric).

### `GET /api/v1/telemetry`
Returns latest known state (restored from persistence on restart).
```json
{
  "devices": [ ...same shape as above... ],
  "server_time": "2026-08-18T12:00:05Z",
  "mode": "trial"
}
```

### `GET /api/v1/forecast?device=rtr-01`
Honest forecast, explicitly not "Chronos". If no forecasting model is active,
returns `"method": "linear-extrapolation"` — never an invented model name.
```json
{
  "device": "rtr-01",
  "method": "linear-extrapolation",
  "horizon_minutes": 30,
  "confidence": { "value": 62.0, "unit": "percent", "source": "computed", "mode": "trial", "observed_at": "...", "run_id": "..." },
  "points": [ {"label": "+5m", "cpu_percent": 44.0, "latency_ms": 15.0, "packet_loss_percent": 0.1} ]
}
```

### `GET /api/v1/anomalies`
```json
[{"id": "anom-...", "device": "rtr-01", "signal": "cpu_saturation", "score": 0.91, "severity": "high", "detected_at": "..."}]
```

### `GET /api/v1/incidents`
```json
{
  "active": { "...incident object or null..." },
  "items": [
    {
      "id": "INC-3f1a9c-000002",
      "occurrence_key": "cpu-saturation:rtr-01",
      "occurrence_sequence": 2,
      "severity": "high",
      "affected_devices": ["rtr-01"],
      "root_cause": "CPU Saturation",
      "status": "active",
      "created_at": "...",
      "updated_at": "...",
      "confidence": 0.87,
      "predicted_sla_breach_minutes": 22,
      "recommended_action": "..."
    }
  ]
}
```
Note `id` is unique per occurrence; `occurrence_key` links recurrences of the
same fault+device together so history is preserved and a resolved incident
CAN recur as a new `id` with incremented `occurrence_sequence`.

### `POST /api/v1/incidents/{id}/decision`
Request: `{"status": "resolved" | "ignored", "actor": "user"}`
Response: the updated incident object, plus it is appended to an audit trail
retrievable via `GET /api/v1/incidents/{id}/decisions`.

### `GET /api/v1/incidents/{id}/decisions`
```json
[{"status": "resolved", "actor": "user", "decided_at": "..."}]
```

### `GET /api/v1/runbook?query=...`
```json
[{"title": "Cpu", "source": "cpu.md", "content": "...", "match_method": "keyword-overlap"}]
```
`match_method` is always honestly reported (`keyword-overlap` today; would be
`embedding-cosine` only if a real embedding model is ever integrated).

### `GET /api/v1/scenarios`
Lists available deterministic scenarios (see `docs/scenarios.md`).

### `POST /api/v1/scenarios/{name}/run`
Triggers one scenario run with a fixed seed; returns `run_id` immediately,
result is polled via `GET /api/v1/scenarios/runs/{run_id}`.

### `GET /metrics`
Prometheus exposition format. See `METRICS_CONTRACT.md` for the exact,
non-invented metric names.

## Error shape (all endpoints)
```json
{"error": {"code": "invalid_request", "message": "human readable", "trace_id": "..."}}
```
No endpoint returns a bare 500 with no body. No handler swallows an error
silently — every error path must either return this shape or log at `error`
level with the same `trace_id`.

## CORS
No wildcard in normal operation. Allowed origins come from
`VYOMM_CORS_ALLOWED_ORIGINS` (comma-separated), default
`http://localhost:5173,http://localhost:8080` for local dev only.
