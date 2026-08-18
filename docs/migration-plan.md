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

- `internal/persistence` — SQLite (via `modernc.org/sqlite`, pure-Go, no
  CGO) + goose migrations (`00001_init.sql`: `telemetry`, `incidents`,
  `incident_decisions` tables), 8/8 tests pass, including:
  - `TestRestore_SurvivesRestart` — direct regression test for "written but
    never restored": closes the store, reopens against the same file,
    asserts telemetry history and incidents are both present in memory
    afterward (the original Python bug: this returned empty in the audit).
  - `TestRestore_RecurrenceHistoryPreservedAcrossRestart` — combines both
    fixed defects: resolve an incident, restart, re-detect the same fault,
    assert a new occurrence is created (not suppressed) and both
    occurrences survive in restored history.
  - `TestPrune_RemovesRowsOlderThanRetention` — direct regression test for
    unbounded growth: ingests one row inside and one outside the retention
    window, asserts `Prune` removes exactly the one outside it.
  - `TestIngest_RejectsInvalidRowsWithReason` — invalid rows are rejected
    with a recorded reason string, never silently dropped or silently
    stored malformed.

Full suite as of this checkpoint: `go build ./...` clean, `go vet ./...`
clean, `go test ./... -race` → 7 packages, 50 tests, all passing, no data
races detected.

- `internal/runbooks` — honest keyword-overlap retrieval (explicitly NOT
  semantic search; `MatchMethod` constant is always reported truthfully as
  `"keyword-overlap"`, never invented as `"embedding-cosine"` or similar).
  8/8 tests pass, including an integration test against the real
  `runbooks/*.md` files confirming a "cpu saturation" query actually
  surfaces `cpu.md` first.

Remaining for Phase 1: `internal/api` (HTTP handlers matching
`API_CONTRACT.md`), `cmd/vyomm-api` wiring, including the periodic
background pruning call (currently `Prune` exists but nothing calls it on a
schedule yet — that wiring belongs in `cmd/vyomm-api`, not the library).

## Round 2 agent deliverables — Controller verification and fixes

Both agents delivered substantial Round 2 work. Verified with real commands,
two genuine bugs found and fixed (documented below so both agents know
exactly what changed and why — neither was a case of "the agent didn't try,"
both were subtle library-semantics issues).

**Writer — `internal/observability/metrics` (Prometheus instrumentation):**
Implements all 15 metrics from `METRICS_CONTRACT.md` with correct bounded
label sets. Found and fixed one compile-time bug: the `Registry.Gatherer`
field is statically typed as the narrower `prometheus.Gatherer` interface,
but the test tried to pass it directly to `testutil.CollectAndCount`, which
requires `prometheus.Collector` — `*prometheus.Registry` (the concrete
runtime type) implements both interfaces, so a one-line type assertion in
the test (`m.Gatherer.(prometheus.Collector)`) fixes it without changing
`metrics.go`'s public API. Also fixed a second, semantic bug in
`TestLabelsDoNotIncludeDeviceOrRun`: it asserted
`testutil.CollectAndCount(m.TelemetryRecordsReceived) == 1` after using two
distinct label combinations, but `CollectAndCount` on a `*CounterVec` counts
time series (one per label combination), not metric families — the
assertion could never pass as written. Rewrote it to correctly check both
things the test actually intended: 2 time series for 2 distinct bounded
label combinations, AND exactly 1 metric family/name via
`Gatherer.Gather()`. 2/2 tests now pass.

**Writer — `internal/observability/tracing` (OTel SDK setup):**
Correct `Init()`/`Tracer()`/span-name-constants design. Found and fixed one
test bug: `TestInMemorySpanRecords` checked `exporter.GetSpans()` *after*
calling `provider.Shutdown()`, but `tracetest.InMemoryExporter.Shutdown()`
calls `Reset()` internally, clearing recorded spans — so the assertion
always saw 0 regardless of whether tracing worked. Confirmed via a
throwaway debug test that spans ARE correctly recorded immediately after
`span.End()` (before `Shutdown`); moved the assertion before the
`Shutdown()` call. 2/2 tests now pass.

**Writer — `cmd/vyomm-simulator`:** Minimal but working deterministic
simulator (`math/rand/v2` with explicit seed, verified byte-identical JSON
output for repeated seed+scenario). Currently supports 5 placeholder
scenario names (`healthy-baseline`, `cpu-saturation`, `memory-pressure`,
`high-latency`, `packet-loss`) rather than the full 9 required scenarios —
expected, since it was built ahead of Thinker's `docs/scenarios.md` ground
truth, which is still pending. Sends exactly one batch and exits, rather
than looping continuously like the original Python simulator — will need
extending once ground truth lands. 2/2 tests pass.

**Writer — `.github/workflows/ci.yml`:** Correctly scoped to what exists
(Go build/vet/gofmt/test-race + web test/build), no jobs for nonexistent
targets (no kind/HAMi/acceptance job yet). Matches the exact commands
verified locally above.

**Writer — `deploy/docker/{vyomm-api,vyomm-simulator}.Dockerfile`:**
Correctly marked `UNTESTED` since `cmd/vyomm-api` doesn't exist yet. Base
image `golang:1.25-alpine` actually matches the real `go.mod` (`go 1.25.0`,
auto-set by tooling) better than this plan's earlier stated "Go 1.23"
target — **correction to this document**: the actual pinned Go version is
**1.25.0**, not 1.23 as originally planned; 1.23 is superseded by reality
and this is the authoritative correction.

**Writer — light theme + accessibility fix (Round 1 followup):** Confirmed
delivered and correct: `web/src/styles.css` now uses a light background
(`#f5f7f8`)/dark text, `transition` properties in the 150–250ms range
(180ms) on hover/focus states, visible `:focus-visible` outlines, and a
`@media (prefers-reduced-motion: reduce)` block. `npm test` (4/4) and
`npm run build` both still pass after the change.

**Thinker — Round 2 (scenario ground truth + evaluation design):** Not yet
delivered at this checkpoint (`docs/scenarios.md` still the Round-1
skeleton, `docs/evaluation-design.md` does not exist yet). Not a problem —
just recorded honestly as pending.

Full repo state after these fixes: `go build ./...` clean, `go vet ./...`
clean, `gofmt -l .` empty, `go test ./... -race` → **11 packages, 63 tests,
all passing**, `web`: 4/4 tests passing, production build succeeds.
`go mod tidy` run to reconcile direct/indirect dependency declarations
after both agents' `go get` calls; all pinned versions preserved exactly
(`client_golang` v1.20.5, `otel` family v1.32.0, `goose` v3.24.1,
`modernc.org/sqlite` v1.34.4).

## Milestone: first runnable VYOMM binary (`cmd/vyomm-api` + `internal/api`)

This is the first point in the migration where VYOMM has an actual running
server, not just tested library code. Built:

- `internal/api` — HTTP handlers for every endpoint in `API_CONTRACT.md`
  except scenario execution (honestly returns HTTP 501 `not_implemented`,
  since `internal/scenarios` doesn't exist yet — no faked success). DTOs
  live entirely in `internal/api/dto.go`, keeping domain types
  (`forecast.Result`, `incidents.Incident`, `detection.Anomaly`) independent
  of wire format. Explicit-allowlist CORS (`withCORS`), no wildcard.
  Prometheus HTTP metrics middleware records duration/count per route
  pattern + method + status class (never per raw path, keeping cardinality
  bounded). 15/15 tests pass, including
  `TestDecideIncident_ThenRecurAfterResolution` — the same recurrence
  regression proven in `internal/incidents`/`internal/persistence`, now
  proven again through the actual HTTP layer end-to-end.
- Added anomaly storage to `internal/persistence.Store` (bounded in-memory
  ring buffer, deliberately NOT persisted to SQLite since anomalies are
  cheaply re-derivable from telemetry — documented as a deliberate design
  choice, not a repeat of the "written but never restored" defect). 2 new
  tests (`TestIngest_DetectsAnomaliesFromValidRows`,
  `TestIngest_AnomaliesDedupeWithinDetectionWindow`) pass.
- `cmd/vyomm-api` — wires config, structured logging, persistence,
  runbooks, and Prometheus metrics into a real `net/http` server with
  graceful shutdown (`signal.NotifyContext`) and a background retention
  pruning goroutine (`startRetentionPruning`, interval = retention/4,
  clamped to [1m, 1h]) — this is what actually calls `Store.Prune` on a
  schedule in production, closing the gap noted in the previous checkpoint.

**Live process verification performed (not just `go test`):** built the
real binary (`go build -o vyomm-api ./cmd/vyomm-api`), ran it against a
temp SQLite file, and via `curl` against the live HTTP server:
- `/healthz`, `POST/GET /api/v1/telemetry`, `/api/v1/anomalies`,
  `/api/v1/incidents`, `/api/v1/forecast`, `/api/v1/runbook` all returned
  correct, contract-shaped JSON.
- Ingesting a device with `cpu_percent=99, memory_percent=96` produced a
  real `Memory Leak` incident and two real anomalies (`cpu_saturation`,
  `memory_pressure`), both severity `critical`, confirming detection rules
  fire correctly through the full HTTP path.
- `/metrics` returned real Prometheus exposition format with actual
  histogram buckets from the requests just made
  (`vyomm_http_request_duration_seconds_bucket`), not a placeholder.
- **Killed the live process, restarted it against the same SQLite file**:
  the previously-ingested telemetry and the previously-resolved incident
  were both present in the fresh process's responses — the persistence
  restore fix verified at the process level, not just in `go test`.

Full repo: `go build ./...` clean, `go vet ./...` clean, `gofmt -l .`
empty, `go test ./... -race` → **12 packages, 80 tests, all passing**
(verified via `go test ./... -v -race | grep -c "^--- PASS"` = 80).

Not yet done: `internal/api` doesn't yet call into
`internal/observability/tracing` (spans aren't emitted from handlers yet —
the package exists and is tested standalone, but nothing calls
`tracing.Tracer(...).Start(...)` from the HTTP layer); scenario execution
endpoints are stubs; no Dockerfile has been built into an actual image yet
(only validated as Go source + `go build` of the target binary).

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

### Deep verification pass (Controller, second review)

**Fixture/contract field alignment — verified, matches:**
`web/src/fixtures/data.json` and `web/src/types.ts` were diffed field-by-field
against `API_CONTRACT.md`. `Health`, `Device`, `Incident`, `Provenance`
envelope, and the telemetry/forecast response shapes all match the contract
exactly (same field names, same nesting). One integration note for when
`internal/api` is built: `API_CONTRACT.md`'s forecast points use
`"label": "+5m"` (string), but the Go domain type
`internal/forecast.Point` currently uses `LabelMinutes int` — this is not a
bug, it's expected: the API layer's response DTO must format
`fmt.Sprintf("+%dm", p.LabelMinutes)` when serializing, keeping the internal
domain model integer-typed (more testable) while the wire format matches the
contract string. Recorded here so it isn't forgotten when `internal/api` is
built.

**Provenance/honesty discipline in `web/src/App.tsx` — verified, good:**
The `gap()` helper explicitly labels any field NOT covered by a provenance
envelope (e.g. "GPU utilization", "Inference latency", "Device count") as
*"Not provenance-wrapped in API_CONTRACT.md"* rather than inventing a
plausible-looking number. The Evaluation screen explicitly states "no
fabricated score is shown" instead of hardcoding an accuracy percentage.
The "Run scenario" button is correctly `disabled` since no live API exists
yet — no fake success is simulated.

**Confirmed gaps requiring follow-up (Writer, Phase 5 completion):**
1. `web/src/styles.css` implements a **dark** theme
   (`background:#0b1118`, `color:#e7edf5`) — the task requires a
   "restrained **light** theme." This needs to be redone, not just
   re-skinned at the margins.
2. `grep -n "prefers-reduced-motion\|:focus\|transition" web/src/styles.css`
   → **zero matches**. No focus-visible states, no transitions, no
   reduced-motion media query anywhere. This fails the explicit
   accessibility/motion requirements in the task and must be added.
3. `deploy/compose/full.yml`'s `vyomm-api` service references image
   `vyomm-api:local`, which does not exist yet (no Dockerfile/build defined
   for the Go binary), and has no environment variables set
   (`VYOMM_ENVIRONMENT_MODE`, `VYOMM_OTEL_EXPORTER_OTLP_ENDPOINT`, etc.) or a
   `/data` volume for SQLite persistence. Expected at this stage (Go API
   doesn't exist yet either) but tracked as a concrete follow-up once
   `cmd/vyomm-api` exists.

**Verified working, no changes needed:**
- `deploy/otel/collector.yml`: OTLP receiver (grpc+http) → batch processor →
  Jaeger OTLP exporter. Single traces pipeline, no fabricated metrics
  pipeline — consistent with the architecture decision to have Prometheus
  scrape `/metrics` directly rather than routing metrics through the
  collector.
- `deploy/prometheus/prometheus.yml`: single scrape job for `vyomm-api:8080`,
  with an explicit comment that HAMi metrics are intentionally absent
  pending Phase 4 discovery — correct restraint, matches
  `METRICS_CONTRACT.md`.
- Pinned versions in `deploy/compose/full.yml` match the versions recorded
  in the original plan (Prometheus v2.55.1, Grafana 11.3.1, OTel Collector
  Contrib 0.115.0, Jaeger 1.63, Loki 3.3.0).
