# VYOMM Migration Plan: Python Demo → Go HAMi GPU Observability Project

Status: **Phase 0 complete, Phase 1 in progress.** This document is updated as each
phase lands. It is the single source of truth for what has actually shipped versus
what is still planned. Do not trust marketing language elsewhere until it matches
an entry here with a passing test.

## Why this migration

An independent read-only audit (see `docs/audit-2026-08-18.md` if retained, or the
externally delivered `abcdef.md` report) found that the original Python/FastAPI
prototype:

- displayed fabricated metrics (`prediction_accuracy=94.2`, formula-based
  `gpu_utilization`/`inference_latency`) as if they were live measurements,
- called its forecast "Chronos-style" while implementing linear extrapolation,
- called its retrieval "RAG" while using SHA-256 hash-bucket pseudo-embeddings,
- wrote telemetry/incidents to SQLite but never restored them on restart,
- grew the telemetry table without bound,
- used deterministic incident IDs that silently prevented a resolved incident
  from ever recurring,
- served the Vite **development** server as the "production" frontend,
- claimed to be "air-gapped" while making live calls to a cloud LLM provider,
- shipped a live provider API key in a local `.env` file.

None of this is safe to present as evidence for GPU-observability engineering
work. The fix is not cosmetic — it requires a real backend with tested behavior,
provenance-tagged values, real metrics, and a real evaluation pipeline.

## Secret handling (Phase 0 — done)

- The committed working tree contained a live Groq API key in `.env`
  (`.env` was already `.gitignored` and the key was **not** found in git history
  via `git log -p --all | grep gsk_` — confirmed during the audit). The file has
  been deleted from the working tree in this migration.
- **Action required by the repository owner:** rotate/revoke that key at
  the Groq console immediately, since it was exposed in a local working copy
  and in audit transcripts. Do not reuse it.
- Added `.env.example` with no real secrets and an explicit trial-mode default
  (`VYOMM_ENVIRONMENT_MODE=trial`) that requires no key at all.
- Removed other untracked throwaway artifacts that should never have been
  present: `backend.log`, `frontend.log`, `simulator.log`, `vyomm.db` (35 MB,
  demonstrating the unbounded-growth defect), `venv/`.

## Target end state

See `README.md` (rewritten) and `docs/architecture.md` for the full target
architecture. Summary: Go backend (`cmd/vyomm-api`), Go simulator
(`cmd/vyomm-simulator`), Go evaluator (`cmd/vyomm-eval`), Go CLI
(`cmd/vyommctl`), React/TypeScript frontend built to static assets, SQLite via
a pure-Go driver with goose migrations, Prometheus + Grafana + OTel Collector +
Jaeger for observability, kind + HAMi for the nvml-mock Kubernetes profile.

## Parity requirement before deletion

Per the working rules, the existing Python implementation (`backend/`,
`simulator/`) is **retained until Go feature parity is proven by tests**. It is
not deleted in Phase 0 or Phase 1. It will be removed only in Phase 6/7 once:

1. the Go API server implements every endpoint the Python server implements,
   with equivalent or stricter behavior (notably: fixed persistence-restore
   and incident-recurrence bugs, which the Python version never had correct),
2. the Go simulator produces deterministic, seeded telemetry with recorded
   ground truth for every scenario in `docs/scenarios.md`,
3. a parity test suite (`tests/parity/`) passes comparing Go endpoint
   contracts against the documented Python contracts,
4. `docker-compose.yml` and `docker/nginx/*` (Python-era) are superseded by
   `deploy/compose/*`.

## Phase tracking

| Phase | Scope | Status |
|---|---|---|
| 0 | Secrets removed, `.env.example`, this plan | **Done** |
| 1 | Go module, config, slog, health, SQLite+goose, ported API + tests | In progress |
| 2 | Go simulator, deterministic seeds, ground truth | Not started |
| 3 | Prometheus/OTel/Jaeger, real metrics only | Not started |
| 4 | HAMi trial + nvml-mock, `vyommctl doctor`/`bootstrap` | Not started |
| 5 | React refactor, provenance badges, production build | Scaffolded by Writer agent, verified by Controller |
| 6 | Go evaluator, full test pyramid, CI, Python removal | Not started |
| 7 | Acceptance bundle + manual UAT session | Not started |

Every row above only moves to "Done" once its corresponding tests actually
pass in this repository — recorded with exact commands and output, not
narrative claims.

## Phase 1 progress (Controller, Go foundation)

Done and verified with passing `go test`:
- `internal/config` — env-based config loader, 7/7 tests pass.
- `internal/observability/logging` — redacting JSON slog wrapper, 5/5 tests pass.
- `internal/telemetry` — provenance-envelope `Value` type + `DeviceTelemetry`
  with validation, 13/13 tests pass.
- `internal/detection` — threshold-based anomaly detection (honest port of
  the Python thresholds, no ML claim), 6/6 tests pass.
- `internal/forecast` — linear-extrapolation forecast honestly labeled
  `method: "linear-extrapolation"` (never "Chronos-style"), 8/8 tests pass
  after fixing one test whose input wasn't steep enough to cross the risk
  threshold (verified by hand-computing the risk score, not by loosening the
  implementation).

- `internal/incidents` — pure `Correlate()` rule engine (honest port of the
  Python if/elif cascade) plus a `Store` with recurrence-safe occurrence
  IDs, 11/11 tests pass. Includes
  `TestStore_ResolvedIncidentCanRecur`, a direct regression test proving
  the fix for the original defect (deterministic incident IDs meant a
  resolved incident could never re-fire) — the test asserts a *new* incident
  ID and incremented `OccurrenceSequence` are produced after resolution,
  with full history preserved via `OccurrenceKey`.

Full suite as of this checkpoint: `go build ./...` clean, `go vet ./...`
clean, `go test ./...` → 6 packages, 42 tests, all passing.

Remaining for Phase 1: `internal/persistence` (SQLite + goose migrations,
restart restore, retention pruning — this is where the Store above gets a
durable backing store and where the other confirmed defect, "written but
never restored," gets fixed), `internal/api` (HTTP handlers matching
`API_CONTRACT.md`), `cmd/vyomm-api` wiring.

## Phase 4 progress (Thinker agent — HAMi/kind design)

Delivered: `docs/hami-design-spec.md`, `docs/modes.md`,
`docs/real-gpu-validation.md`, `deploy/kind/kind-config.yaml`,
`deploy/hami/{trial,mock}-values.yaml`, `deploy/hami/{bootstrap,teardown}-{trial,mock}.sh`.

Controller verification performed:
- `bash -n` on all four shell scripts — syntactically valid.
- YAML parse (`python3 -c "import yaml; yaml.safe_load(...)"`) on kind config
  and both HAMi values files — valid.
- Confirmed the spec is honest about provenance: only `DCGM_FI_DEV_GPU_UTIL`
  is claimed as confirmed (from tutorial docs, not a live scrape), everything
  else is explicitly marked "could not verify — needs live cluster," matching
  `METRICS_CONTRACT.md`'s discovery-procedure requirement.
- Confirmed the `bootstrap-mock.sh` script auto-detects the kind node's
  Kubernetes version for `scheduler.kubeScheduler.imageTag` (resolves open
  question #1 from the design spec).

**Explicitly NOT done:** no live `kind create cluster` / HAMi install has
been run. Given this machine's real-time resource state at review time
(`free -h` showed 1.1 GiB available RAM, 6.8 GiB swap in use — independently
confirmed, not just the Thinker's estimate), attempting a live bring-up now
risks destabilizing the machine to "prove" something that can instead be
correctly deferred. **HAMi version pin (`v2.9.0`), the exact scheduler metric
names, and whether the bootstrap scripts actually succeed end-to-end remain
unverified** until run on adequate hardware or a CI runner with more RAM.

## Phase 3/5 progress (Writer agent — observability configs + frontend scaffold)

Delivered: `deploy/compose/{minimal,full}.yml`, `deploy/prometheus/prometheus.yml`,
`deploy/otel/collector.yml`, `deploy/grafana/**`, `docs/metrics.md`,
`docs/logging-and-tracing.md`, `docs/scenarios.md`, `Makefile` (stub targets),
`web/**` (React/TS/Vite scaffold with fixture-driven components).

Controller verification performed and passing:
- All YAML/JSON configs parse validly (`yaml.safe_load` / `json.load`).
- `grep -rniE "dcgm|hami_" deploy/prometheus deploy/grafana` → no matches —
  confirms the Writer did not invent HAMi/DCGM metric names in dashboards or
  scrape configs, correctly leaving those pending Thinker/Controller discovery.
- `make doctor` → prints `not yet implemented: check local dependencies` and
  exits non-zero (verified `EXIT CODE: 2`) — stubs fail loudly per the rule,
  they do not fake success.
- `cd web && npm test -- --run` → 2 test files, 4 tests, all pass.
- `cd web && npm run build` → succeeds, produces `dist/` static bundle
  (146.62 kB JS gzip 47.26 kB) — production build path confirmed working,
  not a dev-server placeholder.

**Not yet verified:** whether `web/src/fixtures/` shapes match
`API_CONTRACT.md` field-for-field (pending a full component-by-component
diff), and whether the Prometheus/OTel configs actually scrape/receive data
(no live stack has been started yet).
