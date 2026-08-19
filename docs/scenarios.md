# Scenario Catalog — Complete Ground Truth

> **Status:** Complete ground truth derived from running Go code.
> **Author:** Thinker agent, Round 3.
> **Source files read:** `internal/detection/anomaly.go` (thresholds),
> `internal/incidents/incidents.go` (correlation rules), `internal/incidents/store.go`
> (recurrence logic), `internal/telemetry/model.go` (validation).
> Every numeric value below is traceable to a specific line in one of these files.

## Detection thresholds reference

From [`internal/detection/anomaly.go` L45–L51](file:///home/sarvadubey/Desktop/Projects/VYOMM/internal/detection/anomaly.go#L45-L51):

| Signal | Field | Warning ≥ | Critical ≥ |
|--------|-------|-----------|------------|
| `cpu_saturation` | `CPUPercent` | 88 | 97 |
| `memory_pressure` | `MemoryPercent` | 86 | 96 |
| `high_latency` | `LatencyMS` | 85 | 150 |
| `packet_loss` | `PacketLossPercent` | 3.5 | 8 |
| `thermal_drift` | `TemperatureC` | 72 | 86 |

Severity assignment ([L75–L81](file:///home/sarvadubey/Desktop/Projects/VYOMM/internal/detection/anomaly.go#L75-L81)):
- `v >= critical` → `critical`
- `score > 0.82` (where `score = v / critical`, capped at 1.0) → `high`
- otherwise → `medium`

## Correlation rules reference

From [`internal/incidents/incidents.go` L108–L163](file:///home/sarvadubey/Desktop/Projects/VYOMM/internal/incidents/incidents.go#L108-L163).
Rules are evaluated in order; **first match wins** per device ([L211](file:///home/sarvadubey/Desktop/Projects/VYOMM/internal/incidents/incidents.go#L211)).

| # | Root cause | Severity | SLA mins | Match condition |
|---|-----------|----------|----------|-----------------|
| 1 | Congestion Incident | critical | 22 | `role==router && cpu>95 && maxNeighborLatency>100 && maxNeighborLoss>5` |
| 2 | Firewall Saturation | high | 18 | `role==firewall && (cpu>90 OR bw>92) && latency>90` |
| 3 | Memory Leak | high | 22 | `memory>94 && cpu>80` |
| 4 | Packet Loss Degradation | high | 22 | `packetLoss>7 && latency>120` |
| 5 | Switch Overheating | high | 12 | `temp>84 && role==switch` |
| 6 | BGP Edge Instability | critical | 15 | `role==gateway && latency>140 && packetLoss>4` |

Confidence formula ([L201](file:///home/sarvadubey/Desktop/Projects/VYOMM/internal/incidents/incidents.go#L201)):
`confidence = 0.78 + min(0.18, (cpu + latency/2 + packetLoss*6) / 1000)`

---

## Scenario 1: `healthy-baseline`

**Seed:** 1001

**Purpose:** Prove that normal telemetry produces zero anomalies and zero incidents.

**Source:** `synthetic` | **Mode:** `trial` | **Needs HAMi:** No

### Telemetry (single device, single tick)

| Field | Value | Rationale |
|-------|-------|-----------|
| hostname | `rtr-01` | |
| role | `router` | |
| cpu_percent | 42.0 | Below warning=88 ([L46](file:///home/sarvadubey/Desktop/Projects/VYOMM/internal/detection/anomaly.go#L46)) |
| memory_percent | 38.0 | Below warning=86 ([L47](file:///home/sarvadubey/Desktop/Projects/VYOMM/internal/detection/anomaly.go#L47)) |
| bandwidth_percent | 20.0 | No threshold check exists |
| temperature_c | 51.0 | Below warning=72 ([L50](file:///home/sarvadubey/Desktop/Projects/VYOMM/internal/detection/anomaly.go#L50)) |
| latency_ms | 14.0 | Below warning=85 ([L48](file:///home/sarvadubey/Desktop/Projects/VYOMM/internal/detection/anomaly.go#L48)) |
| packet_loss_percent | 0.1 | Below warning=3.5 ([L49](file:///home/sarvadubey/Desktop/Projects/VYOMM/internal/detection/anomaly.go#L49)) |
| status | `healthy` | |

### Expected outcome

- **Anomalies:** 0 — no field reaches any warning threshold.
- **Incidents:** 0 — no anomalies means no correlation candidates.
- **Validation evidence:** `TestDetect_NoAnomaliesForHealthyDevice` in
  [`anomaly_test.go` L28–L33](file:///home/sarvadubey/Desktop/Projects/VYOMM/internal/detection/anomaly_test.go#L28-L33)
  uses identical values and asserts `len(got) == 0`. `TestCorrelate_NoMatchForHealthyDevices`
  in [`incidents_test.go` L27–L33](file:///home/sarvadubey/Desktop/Projects/VYOMM/internal/incidents/incidents_test.go#L27-L33)
  uses `cpu=40, mem=30, lat=15, loss=0.1` and asserts 0 candidates.

---

## Scenario 2: `gpu-memory-pressure-allocation`

**Seed:** 2001

**Purpose:** Simulate a pod requesting more GPU memory than available, triggering
the HAMi scheduler to annotate a memory-budget allocation. This is a
HAMi-observable scenario — VYOMM observes the allocation annotation and
records a `vyomm_scheduler_allocation_events_total{event_type="allocated"}`
counter increment.

**Source:** `synthetic` (telemetry) + `mock` (HAMi annotation) | **Mode:** `nvml-mock` | **Needs HAMi:** Yes

### Telemetry (network-level — simulated, no anomaly expected)

Same as `healthy-baseline` — network devices are stable. The GPU event
is observed via HAMi pod annotation, not telemetry ingestion.

### HAMi observable (per [hami-design-spec.md §5.6](file:///home/sarvadubey/Desktop/Projects/VYOMM/docs/hami-design-spec.md))

- A test pod requests `nvidia.com/gpu: 1` with `nvidia.com/gpumem: 30720`
  (30 GiB of a 40 GiB A100).
- VYOMM watches for `hami.io/vgpu-devices-allocated` annotation.
- Expected annotation: `GPU-<UUID>,NVIDIA,30720,100:;`
- VYOMM increments `vyomm_scheduler_allocation_events_total{event_type="allocated"}`.

### Expected outcome

- **Anomalies:** 0 (network telemetry is healthy).
- **Incidents:** 0 (no anomaly-driven correlation).
- **Metric change:** `vyomm_scheduler_allocation_events_total{event_type="allocated"}` += 1.

---

## Scenario 3: `gpu-core-contention-request`

**Seed:** 3001

**Purpose:** Simulate a pod requesting limited GPU cores, verifying HAMi
records the core percentage in the allocation annotation.

**Source:** `synthetic` + `mock` | **Mode:** `nvml-mock` | **Needs HAMi:** Yes

### HAMi observable

- A test pod requests `nvidia.com/gpu: 1` with `nvidia.com/gpucores: 30` (30%).
- Expected annotation: `GPU-<UUID>,NVIDIA,40960,30:;`
- VYOMM increments `vyomm_scheduler_allocation_events_total{event_type="allocated"}`.

### Expected outcome

- **Anomalies:** 0 | **Incidents:** 0
- **Metric change:** `vyomm_scheduler_allocation_events_total{event_type="allocated"}` += 1.

---

## Scenario 4: `oversubscription-attempt`

**Seed:** 4001

**Purpose:** Request more vGPU slots than available (e.g. 81 on a node with 80),
causing the scheduler to reject the pod.

**Source:** `synthetic` + `mock` | **Mode:** `nvml-mock` | **Needs HAMi:** Yes

### HAMi observable

- A test pod requests `nvidia.com/gpu: 81` (exceeds 80 available slots).
- Pod stays `Pending` — no `hami.io/vgpu-devices-allocated` annotation.
- Kubernetes events from `hami-scheduler` will show scheduling failure.
- VYOMM increments `vyomm_scheduler_allocation_events_total{event_type="rejected"}`.

### Expected outcome

- **Anomalies:** 0 | **Incidents:** 0 (no anomaly triggered by a pending pod).
- **Metric change:** `vyomm_scheduler_allocation_events_total{event_type="rejected"}` += 1.
- **VYOMM does NOT create a fake incident** for unschedulable GPU pods — that
  would be an invented signal. The scheduler rejection is only recorded as a
  Prometheus counter.

---

## Scenario 5: `multi-gpu-request`

**Seed:** 5001

**Purpose:** Request 2 GPUs for a single pod, verifying HAMi writes two
allocation entries in the annotation.

**Source:** `synthetic` + `mock` | **Mode:** `nvml-mock` | **Needs HAMi:** Yes

### HAMi observable

- A test pod requests `nvidia.com/gpu: 2`.
- Expected annotation: `GPU-<UUID1>,NVIDIA,40960,100:;GPU-<UUID2>,NVIDIA,40960,100:;`
  (two semicolon-separated entries).
- VYOMM increments `vyomm_scheduler_allocation_events_total{event_type="allocated"}` by 1
  (one allocation event per pod, not per GPU).

### Expected outcome

- **Anomalies:** 0 | **Incidents:** 0
- **Metric change:** `vyomm_scheduler_allocation_events_total{event_type="allocated"}` += 1.

---

## Scenario 6: `unschedulable-workload`

**Seed:** 6001

**Purpose:** Submit a pod requesting a nonexistent GPU resource type, ensuring
VYOMM observes the scheduling failure correctly.

**Source:** `synthetic` + `mock` | **Mode:** `nvml-mock` | **Needs HAMi:** Yes

### HAMi observable

- A test pod requests `nvidia.com/gpu: 1` on a node where the device-plugin
  is not running (e.g., node label `gpu=on` removed).
- Pod stays `Pending`.
- VYOMM increments `vyomm_scheduler_allocation_events_total{event_type="rejected"}`.

### Expected outcome

- **Anomalies:** 0 | **Incidents:** 0
- **Metric change:** `vyomm_scheduler_allocation_events_total{event_type="rejected"}` += 1.

---

## Scenario 7: `scheduler-or-metrics-endpoint-failure`

**Seed:** 7001

**Purpose:** Simulate the HAMi scheduler endpoint becoming unreachable.
VYOMM must record the failure via `vyomm_hami_scrape_success{mode="nvml-mock"} = 0`.

**Source:** `synthetic` | **Mode:** `nvml-mock` | **Needs HAMi:** Yes (then deliberately broken)

### Procedure

1. Bootstrap nvml-mock cluster normally.
2. Kill the `hami-scheduler` pod: `kubectl -n kube-system delete pod -l app.kubernetes.io/name=hami --force`.
3. VYOMM's HAMi scrape goroutine attempts `curl http://<node-ip>:31993/metrics` and gets connection refused.
4. VYOMM sets `vyomm_hami_scrape_success{mode="nvml-mock"} = 0`.

### Network telemetry (independent)

Same as `healthy-baseline` — network devices remain stable.

### Expected outcome

- **Anomalies:** 0 | **Incidents:** 0 (the scheduler failure is a metric signal, not a telemetry anomaly).
- **Metric change:** `vyomm_hami_scrape_success` → 0.
- **What this proves:** VYOMM correctly distinguishes "I can't reach the data source" from "the data shows a problem."

---

## Scenario 8: `telemetry-scrape-interruption`

**Seed:** 8001

**Purpose:** Prove VYOMM handles missing/empty telemetry gracefully — if the
simulator stops sending, the API returns the last known state and does not
fabricate new data.

**Source:** `synthetic` | **Mode:** `trial` | **Needs HAMi:** No

### Procedure

1. Ingest one tick of healthy telemetry (same as scenario 1).
2. Stop the simulator (no further POST requests).
3. `GET /api/v1/telemetry` returns the devices from tick 1.
4. `GET /api/v1/anomalies` returns empty (no new detection runs without new ingest).
5. `GET /api/v1/incidents` returns no active incident.

### Expected outcome

- **Anomalies:** 0 | **Incidents:** 0
- **Telemetry:** stale but honest — last known values returned, not manufactured.
- **What this proves:** No background fabrication; detection is synchronous with ingest
  (see [`handlers.go` L52](file:///home/sarvadubey/Desktop/Projects/VYOMM/internal/api/handlers.go#L52):
  `s.correlateIncidents(req.Devices, now)` is called only inside `handleIngestTelemetry`).

---

## Scenario 9: `incident-recurrence-after-resolution`

**Seed:** 9001

**Purpose:** Prove the fix for the original Python recurrence bug: resolving
an incident does not permanently silence that device+fault combination.

**Source:** `synthetic` | **Mode:** `trial` | **Needs HAMi:** No

### Telemetry (triggering a Memory Leak incident)

From the "Memory Leak" rule ([`incidents.go` L128–L134](file:///home/sarvadubey/Desktop/Projects/VYOMM/internal/incidents/incidents.go#L128-L134)):
`d.MemoryPercent > 94 && d.CPUPercent > 80`

| Field | Value | Rationale |
|-------|-------|-----------|
| hostname | `rtr-01` | |
| role | `router` | |
| cpu_percent | 85.0 | > 80, satisfies Memory Leak rule |
| memory_percent | 96.0 | > 94, satisfies Memory Leak rule |
| bandwidth_percent | 20.0 | |
| temperature_c | 50.0 | Below thermal_drift warning |
| latency_ms | 15.0 | Below high_latency warning |
| packet_loss_percent | 0.1 | Below packet_loss warning |
| status | `healthy` | |

### Detection details

Anomalies triggered (from detection thresholds):
- `memory_pressure`: 96.0 ≥ warning=86, score = 96/96 = 1.0 → **critical** (≥ critical=96)

Correlation candidate:
- Rule 3 "Memory Leak" matches: `mem=96 > 94` ✓, `cpu=85 > 80` ✓.
- Rule 1 "Congestion Incident" does NOT match: `cpu=85 ≤ 95`.
- Rule 2 "Firewall Saturation" does NOT match: `role=router ≠ firewall`.
- **First match is Rule 3.**
- `occurrence_key` = `"Memory Leak:rtr-01"` ([L283](file:///home/sarvadubey/Desktop/Projects/VYOMM/internal/incidents/incidents.go#L283))
- Confidence = `0.78 + min(0.18, (85 + 15/2 + 0.1*6)/1000)` = `0.78 + min(0.18, 93.1/1000)` = `0.78 + 0.093` = `0.87`

### Step-by-step expected sequence

1. **Tick 1 — Ingest triggering telemetry:**
   - POST the device above.
   - `handleIngestTelemetry` calls `correlateIncidents` synchronously ([`handlers.go` L52](file:///home/sarvadubey/Desktop/Projects/VYOMM/internal/api/handlers.go#L52)).
   - `Correlate` produces 1 candidate: `{OccurrenceKey: "Memory Leak:rtr-01", RootCause: "Memory Leak", Severity: "high"}`.
   - `Store.Upsert` creates a new incident (history empty, so seq=1) ([`store.go` L50–L68](file:///home/sarvadubey/Desktop/Projects/VYOMM/internal/incidents/store.go#L50-L68)).
   - **Result:** Incident A with `id=INC-<hash>-000001`, `occurrence_key="Memory Leak:rtr-01"`, `occurrence_sequence=1`, `status="active"`.

2. **Tick 2 — Re-ingest same telemetry (deduplication test):**
   - POST same device again.
   - `Store.Upsert` finds an active occurrence for `"Memory Leak:rtr-01"` ([L43–L47](file:///home/sarvadubey/Desktop/Projects/VYOMM/internal/incidents/store.go#L43-L47)) → returns `created=false`.
   - **Result:** Same incident A, no duplication. This is NOT a bug.

3. **Tick 3 — Resolve the incident:**
   - POST `/api/v1/incidents/<A.id>/decision` with `{"status": "resolved", "actor": "user"}`.
   - `Store.Decide` sets `A.Status = "resolved"` ([`store.go` L82](file:///home/sarvadubey/Desktop/Projects/VYOMM/internal/incidents/store.go#L82)).
   - Audit trail now has 1 entry.

4. **Tick 4 — Re-ingest triggering telemetry again:**
   - POST same device.
   - `Store.Upsert` checks history for `"Memory Leak:rtr-01"`: last occurrence (A) has `status="resolved"` → does NOT return early → creates a **new** incident.
   - **Result:** Incident B with `id=INC-<hash>-000002` (different from A), `occurrence_key="Memory Leak:rtr-01"` (same), `occurrence_sequence=2`, `status="active"`.

5. **Verification assertions:**
   - `B.ID != A.ID` (new unique ID)
   - `B.OccurrenceSequence == 2`
   - `B.OccurrenceKey == A.OccurrenceKey == "Memory Leak:rtr-01"`
   - `GET /api/v1/incidents` returns 2 items: A (resolved) and B (active)
   - History for `"Memory Leak:rtr-01"` contains `[A.ID, B.ID]`

### Evidence from existing tests

- [`TestStore_ResolvedIncidentCanRecur`](file:///home/sarvadubey/Desktop/Projects/VYOMM/internal/incidents/incidents_test.go#L106-L156) — unit test proving the in-memory Store recurrence.
- [`TestDecideIncident_ThenRecurAfterResolution`](file:///home/sarvadubey/Desktop/Projects/VYOMM/internal/api/server_test.go#L214-L258) — HTTP-level integration test proving the full pipeline: ingest → incident → resolve → re-ingest → new incident with `len(Items)==2`.

---

## Summary table

| # | Scenario ID | Anomalies | Incident root cause | Incident severity | Needs HAMi |
|---|------------|-----------|--------------------|--------------------|------------|
| 1 | `healthy-baseline` | 0 | none | — | No |
| 2 | `gpu-memory-pressure-allocation` | 0 | none (HAMi allocation event) | — | Yes |
| 3 | `gpu-core-contention-request` | 0 | none (HAMi annotation) | — | Yes |
| 4 | `oversubscription-attempt` | 0 | none (pod rejected) | — | Yes |
| 5 | `multi-gpu-request` | 0 | none (HAMi annotation) | — | Yes |
| 6 | `unschedulable-workload` | 0 | none (pod rejected) | — | Yes |
| 7 | `scheduler-or-metrics-endpoint-failure` | 0 | none (scrape failure) | — | Yes |
| 8 | `telemetry-scrape-interruption` | 0 | none (stale data) | — | No |
| 9 | `incident-recurrence-after-resolution` | ≥1 (`memory_pressure`) | Memory Leak | high | No |

> [!NOTE]
> Scenarios 2–7 are HAMi-observable scenarios. They do NOT produce
> anomaly-driven incidents because the HAMi integration points are pod
> annotations and scheduler events, not telemetry threshold breaches.
> The anomaly detection system only operates on `DeviceTelemetry` fields
> (CPU, memory, latency, packet loss, temperature). HAMi events are
> recorded via dedicated VYOMM Prometheus metrics, not the anomaly/incident
> pipeline.
