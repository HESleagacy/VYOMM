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
| 5 | React refactor, provenance badges, production build | Not started |
| 6 | Go evaluator, full test pyramid, CI, Python removal | Not started |
| 7 | Acceptance bundle + manual UAT session | Not started |

Every row above only moves to "Done" once its corresponding tests actually
pass in this repository — recorded with exact commands and output, not
narrative claims.
