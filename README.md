# VYOMM

Predictive NOC (Network Operations Center) Copilot demonstration, being
re-engineered as a **GPU-observability engineering project** built around HAMi
(Heterogeneous AI Computing virtualization middleware) on Kubernetes.

The end-to-end workflow it models:

```
Telemetry Ingest → Forecast → Anomaly Detection → Incident Correlation
                 → Runbook Retrieval → Copilot Reasoning → Human Approval
```

## Current state

VYOMM is mid-migration from a legacy Python/FastAPI prototype to a **Go
rewrite**. All active engineering is in the Go implementation, which is real,
test-driven, and runnable:

| Area | Status |
|---|---|
| Go backend API (`cmd/vyomm-api`, `internal/api`) | **Working, tested, runs live** |
| Go domain logic (telemetry/detection/forecast/incidents/runbooks) | **Complete, tested** |
| Persistence (SQLite + goose migrations, restart restore, retention) | **Complete, tested** |
| Observability (Prometheus metrics, OTel tracing, structured logging) | **Complete, tested** |
| Go simulator (`cmd/vyomm-simulator`) | Minimal placeholder (one batch, exits) |
| Scenario engine (`internal/scenarios`) | Not started (endpoints return 501) |
| Evaluator (`cmd/vyomm-eval`) | Designed only |
| HAMi / kind GPU integration | Design + scaffolding only, never run live |
| Frontend (`web/`) | Scaffold, fixture-driven, not wired to live API |
| Legacy Python stack (`backend/`, `simulator/`, `frontend/`) | Deprecated, retained for parity |

A key project discipline: **no fabricated results**. Unimplemented endpoints
return `501`, forecasting/retrieval are labeled by their real method
(`linear-extrapolation`, `keyword-overlap`), and the UI refuses to render
un-provenanced numbers as measurements. See `STATUS.md` for the detailed
analysis.

## Run the Go API

Requires Go 1.25+. No LLM API key is required — the demo runs fully offline
with a deterministic fallback analysis.

```bash
mkdir -p data                    # SQLite parent dir (default ./data/vyomm.db)
go run ./cmd/vyomm-api
```

Open `http://localhost:8080`. On startup the API restores persisted state
(SQLite), and a background goroutine prunes telemetry older than the retention
window (default 24h).

Optional environment overrides (see `.env.example`):

```bash
VYOMM_LLM_API_KEY=...            # enables the real LLM provider (OpenAI-compatible)
VYOMM_SQLITE_PATH=./data/vyomm.db
VYOMM_API_ADDR=:8080
VYOMM_OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318   # optional OTel/Jaeger
```

## Endpoints

All under `http://localhost:8080`:

- `GET /healthz` — real dependency status (DB liveness, LLM config, OTel)
- `POST /api/v1/telemetry` — ingest a device telemetry batch (used by the simulator)
- `GET /api/v1/telemetry` — latest known device state (restored across restarts)
- `GET /api/v1/forecast?device=rtr-01` — honest `linear-extrapolation` forecast
- `GET /api/v1/anomalies` — threshold-based anomaly detection results
- `GET /api/v1/incidents` — correlated incidents with recurrence history
- `POST /api/v1/incidents/{id}/decision` — approve/ignore an incident
- `GET /api/v1/incidents/{id}/decisions` — decision audit trail
- `GET /api/v1/runbook?query=...` — keyword-overlap runbook retrieval
- `GET /api/v1/scenarios` — deterministic scenario list (see `docs/scenarios.md`)
- `POST /api/v1/scenarios/{name}/run` — run a scenario (currently 501)
- `GET /metrics` — Prometheus exposition format

Every metric/value is wrapped in a provenance envelope (`source`, `mode`,
`observed_at`, `run_id`). See `API_CONTRACT.md` (authoritative) and
`METRICS_CONTRACT.md`.

## Observability

- `GET /metrics` — Prometheus; all 15 `vyomm_*` metrics per `METRICS_CONTRACT.md`
- OTel traces (exported via OTLP/HTTP to Jaeger/collector) — best-effort: a
  missing collector never takes down the API
- Structured JSON logging (redacting) via `slog`

## Testing

```bash
go build ./... && go vet ./... && gofmt -l .
go test ./...        # all packages pass
```

CI (`.github/workflows/ci.yml`) runs build/vet/gofmt/test-race plus the web
job on every push.

## Documentation

- `STATUS.md` — honest project status and code-review findings
- `docs/migration-plan.md` — phase tracking for the Python→Go migration
- `docs/scenarios.md` — the 9 deterministic scenarios (ground truth)
- `docs/modes.md` — `trial` / `nvml-mock` / `real-gpu` operating modes
- `docs/hami-design-spec.md` — HAMi design, `deploy/hami/` scaffolding

## Legacy Python stack

The original hackathon prototype (`backend/`, `simulator/`, `frontend/`) is
still runnable via `docker-compose.yml`, but is deprecated and slated for
removal once the Go rewrite reaches feature parity. It is retained only as a
reference.