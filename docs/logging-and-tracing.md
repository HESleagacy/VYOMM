# Logging And Tracing

Go services emit structured `log/slog` JSON. Relevant records include the
correlation fields listed in `METRICS_CONTRACT.md`, including `run_id`,
`scenario_id`, `incident_id`, `trace_id`, and `span_id`.

The full profile includes the pinned OTel Collector and Jaeger services. The
collector accepts OTLP and forwards traces to Jaeger. No credentials or API
keys belong in logs or deployment configuration.
