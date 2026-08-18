# Scenario Catalog

This is the structure-only catalog for deterministic simulator work. Ground
truth, seeds, expected detections, and run evidence are filled by the
Controller when the Go scenario runner exists.

| ID | Description | Ground truth | Expected result |
|---|---|---|---|
| `healthy-baseline` | Stable device telemetry | pending | pending |
| `cpu-saturation` | Sustained CPU pressure | pending | pending |
| `memory-pressure` | Sustained memory pressure | pending | pending |
| `high-latency` | Path latency degradation | pending | pending |
| `packet-loss` | Loss and retransmit symptoms | pending | pending |
| `firewall-saturation` | Firewall session or CPU pressure | pending | pending |
| `bgp-failure` | Gateway route instability | pending | pending |
| `incident-recurrence` | Resolved fault occurs again | pending | pending |
| `invalid-telemetry` | Validation and rejected rows | pending | pending |
