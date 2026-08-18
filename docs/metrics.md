# Metrics

VYOMM application metrics are defined exclusively in `METRICS_CONTRACT.md`.
Prometheus configuration is under `deploy/prometheus/`. Labels remain bounded:
hostnames, incident IDs, run IDs, and trace IDs are never label values.

HAMi and NVML names are not documented or dashboarded until the Phase 4
discovery procedure captures them verbatim from the pinned deployment.
