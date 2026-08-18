# VYOMM Metrics Contract (authoritative)

Rule: **never invent HAMi or DCGM metric names.** VYOMM's own application
metrics are namespaced `vyomm_*` and are the only metrics VYOMM defines.
HAMi/NVML metric names, once discovered from the actually running pinned
version in Phase 4, get recorded verbatim in
`artifacts/runs/<run-id>/metrics/discovered-metrics.txt` — they are never
hand-typed into dashboards from memory.

## VYOMM application metrics (Go backend + simulator), all bounded-cardinality

| Metric | Type | Labels (bounded only) | Notes |
|---|---|---|---|
| `vyomm_telemetry_records_received_total` | counter | `mode`, `source` | no per-device label |
| `vyomm_telemetry_ingestion_errors_total` | counter | `mode`, `reason_class` | `reason_class` is a small fixed enum (`validation`,`storage`,`other`), never raw error text |
| `vyomm_incidents_active` | gauge | `mode`, `severity` | |
| `vyomm_incidents_resolved_total` | counter | `mode`, `severity` | |
| `vyomm_incidents_recurred_total` | counter | `mode` | increments when an occurrence_key gets a new active occurrence after prior resolution |
| `vyomm_scenario_runs_total` | counter | `scenario_name`, `mode`, `result` | `scenario_name` is from the fixed scenario catalog (bounded set), `result` ∈ `pass,fail` |
| `vyomm_detection_latency_seconds` | histogram | `mode` | time from telemetry ingest to anomaly/incident creation |
| `vyomm_http_request_duration_seconds` | histogram | `method`, `route`, `status_class` | `route` is the registered pattern, not the raw path (no path params in label) |
| `vyomm_http_requests_total` | counter | `method`, `route`, `status_class` | |
| `vyomm_hami_scrape_success` | gauge (0/1) | `mode` | whether VYOMM successfully scraped the HAMi/device-plugin endpoint |
| `vyomm_scheduler_allocation_events_total` | counter | `mode`, `event_type` | only populated where obtainable from HAMi in nvml-mock mode; `event_type` bounded enum (`requested`,`allocated`,`rejected`) |
| `vyomm_evaluation_precision` | gauge | `run_id` **excluded from label — use as a value tagged via `evaluation` job info metric instead** | see note below |
| `vyomm_persistence_operations_total` | counter | `operation`, `result` | `operation` ∈ `insert,restore,prune`; `result` ∈ `ok,error` |
| `vyomm_persistence_pruned_rows_total` | counter | `table` | bounded table name enum |
| `vyomm_records_dropped_total` | counter | `mode`, `reason_class` | |

**Cardinality rule:** device hostnames, incident IDs, run IDs, trace IDs, and
scenario run IDs must NEVER appear as Prometheus label values. Where a
run/scenario needs to be correlated to a metric sample, do it via exemplars
(OTel trace exemplars) or by joining against `artifacts/runs/<id>/metrics/`
snapshots — not via label cardinality explosion. `vyomm_evaluation_*` results
are written to `evaluation/report.json`/`report.md`, not high-cardinality
Prometheus series.

## Discovery procedure for HAMi/NVML metrics (Phase 4, AI #1 responsibility)

1. Deploy the pinned HAMi version in the chosen lab mode (trial fake-GPU or
   nvml-mock) per the two referenced tutorials.
2. `curl` the actual metrics endpoint(s) HAMi/device-plugin expose.
3. Save the raw scrape verbatim to
   `artifacts/runs/<run-id>/metrics/discovered-metrics.txt`.
4. Record, in `docs/modes.md`, exactly which of those metric names are used
   in dashboards/recording rules, quoting them verbatim from step 3.
5. If a desired signal (e.g., "GPU memory allocated") is not exposed in a
   given mode, do not synthesize a same-named metric. Expose
   `vyomm_synthetic_gpu_memory_allocated_bytes{mode="trial"}` instead, with
   the `vyomm_synthetic_` prefix making the provenance unambiguous even in
   raw Prometheus output.

## Logs correlation fields (all Go services, `log/slog` JSON)

Required keys on relevant log lines: `time`, `level`, `service`, `mode`,
`component`, `run_id`, `scenario_id`, `incident_id`, `trace_id`, `span_id`,
`event`, `msg`. No secrets, tokens, or API keys in any field — redact with
a fixed `[REDACTED]` marker if a value matches a configured secret pattern.
