# Evaluation Design Specification (`cmd/vyomm-eval`)

> **Status:** Authoritative design specification for `cmd/vyomm-eval` and evaluation logic.
> **Author:** Thinker agent, Round 3.
> **Scope:** Ground truth comparison, metric definitions, recurrence scoring rules, and latency measurement.

---

## 1. Architectural Foundation & Execution Context

### 1.1 Ingestion and Detection Timing
In VYOMM, anomaly detection and incident correlation are **synchronous with telemetry ingestion**. 

As implemented in [`internal/api/handlers.go` L30–L78](file:///home/sarvadubey/Desktop/Projects/VYOMM/internal/api/handlers.go#L30-L78):
1. A batch of telemetry is received via `POST /api/v1/telemetry`.
2. Telemetry rows are persisted via `s.Store.Ingest`.
3. `s.correlateIncidents` is called **synchronously** within the handler goroutine:
   - `detection.Detect(valid, now)` evaluates static thresholds.
   - `incidents.Correlate(valid)` evaluates rule matches.
   - `s.Store.UpsertIncident` updates incident state.
4. The HTTP response is returned to the caller.

Because there is no async background polling loop for detection, detection occurs at timestamp $T_{\text{ingest}}$.

### 1.2 Evaluation Window & Time-Tolerance Window ($\Delta t$)
When running a benchmark scenario:
- **Telemetry Batch Timestamp ($T_{\text{tick}}$):** The `observed_at` timestamp sent in the telemetry payload.
- **Ingest Response Timestamp ($T_{\text{recv}}$):** The wall-clock time when the API server finishes processing the batch.
- **Time-Tolerance Window ($\Delta t$):** Defined as **0 seconds (exact tick matching)** for batch-level anomaly/incident evaluation. Because detection is synchronous per HTTP ingest call, any anomaly or incident resulting from tick $T$ is generated during that exact ingest request. For live stream scenarios where wall-clock alignment is measured, a tolerance window of **$\Delta t = \pm 20 \text{ seconds}$** (matching `DetectionWindowSeconds` in [`internal/detection/anomaly.go` L57](file:///home/sarvadubey/Desktop/Projects/VYOMM/internal/detection/anomaly.go#L57)) applies for collapsing duplicate time buckets.

---

## 2. Classification Definitions (TP, FP, FN, TN)

Evaluation is performed separately for **Anomalies** and **Incidents**.

### 2.1 Anomaly Evaluation

An anomaly tuple is defined as $(D, S, \text{Severity})$ where $D$ is the device hostname and $S$ is the signal name (`cpu_saturation`, `memory_pressure`, `high_latency`, `packet_loss`, `thermal_drift`).

- **True Positive (TP_anom):** Ground truth specifies an anomaly $(D, S, \text{Severity})$ for tick $T$, and `GET /api/v1/anomalies` records an anomaly for $D$ and $S$ with matching $\text{Severity}$ detected at tick $T$.
- **False Positive (FP_anom):** An anomaly $(D, S, \text{Severity})$ is recorded by the API for tick $T$, but ground truth specifies no anomaly (or a different signal/device) for tick $T$.
- **False Negative (FN_anom):** Ground truth specifies an anomaly $(D, S, \text{Severity})$ for tick $T$, but no matching anomaly is recorded by the API for tick $T$.
- **True Negative (TN_anom):** Ground truth specifies no anomaly for device $D$ and signal $S$ at tick $T$, and no anomaly is recorded. (Used for specificity calculation).

### 2.2 Incident Evaluation & Recurrence Scoring

An incident candidate is defined by its `OccurrenceKey` (`RootCause:Hostname`).

#### Recurrence Scoring Principle (One Logical Fault vs. Multiple Occurrences)
Per [`internal/incidents/incidents.go`](file:///home/sarvadubey/Desktop/Projects/VYOMM/internal/incidents/incidents.go) and [`internal/incidents/store.go`](file:///home/sarvadubey/Desktop/Projects/VYOMM/internal/incidents/store.go):
- Multiple telemetry ticks arriving while an incident is `active` are deduplicated into the **same** incident ID (`OccurrenceSequence` remains unchanged, `created=false`).
- Once an incident is marked `resolved` or `ignored`, a subsequent triggering tick creates a **new** incident occurrence (`OccurrenceSequence` increments, `created=true`).

#### Evaluation Rules for Recurrence:
1. **Deduplication Phase (Active Incident):** Ingesting $N$ consecutive triggering ticks while active MUST produce exactly **1 active incident**.
   - If 1 active incident exists: **1 TP_inc**.
   - If $K > 1$ active incidents are created for the same `OccurrenceKey` without resolution: 1 TP_inc, and $K - 1$ FP_inc (over-segmentation defect).
2. **Recurrence Phase (Post-Resolution):** Resolving incident $I_1$ and then ingesting a triggering tick MUST produce incident $I_2$ (`OccurrenceSequence = 2`).
   - If $I_2$ is created with sequence 2: **1 TP_inc** for the recurrence.
   - If no new incident is created (silenced/suppressed, the original Python defect): **1 FN_inc** for the recurrence.

---

## 3. Metrics & Formulas (Including Zero-Denominator Edge Cases)

### 3.1 Precision ($P$)
$$P = \frac{TP}{TP + FP}$$

**Zero-Denominator Rule:**
- If $TP + FP = 0$ (no detections produced by system):
  - If ground truth also expected 0 detections ($FN = 0$, e.g., `healthy-baseline`), $P = 1.0$ (100%).
  - If ground truth expected $>0$ detections ($FN > 0$), $P = 0.0$.

### 3.2 Recall ($R$)
$$R = \frac{TP}{TP + FN}$$

**Zero-Denominator Rule:**
- If $TP + FN = 0$ (ground truth expects 0 detections, e.g., `healthy-baseline`):
  - If system produced 0 detections ($FP = 0$), $R = 1.0$ (100%).
  - If system produced $>0$ detections ($FP > 0$), $R = 1.0$ (Recall is trivially 100% satisfied as no true items were missed, but Precision will penalize FP).

### 3.3 F1 Score ($F_1$)
$$F_1 = \frac{2 \cdot P \cdot R}{P + R}$$

**Zero-Denominator Rule:**
- If $P + R = 0$, $F_1 = 0.0$.

### 3.4 Metric Summary Table

| Case | Expected (GT) | Detected (API) | TP | FP | FN | Precision | Recall | F1 |
|------|--------------|----------------|----|----|----|-----------|--------|----|
| Healthy Baseline | 0 | 0 | 0 | 0 | 0 | 1.0 | 1.0 | 1.0 |
| Healthy with False Alarm | 0 | 1 | 0 | 1 | 0 | 0.0 | 1.0 | 0.0 |
| Anomaly Missed | 1 | 0 | 0 | 0 | 1 | 0.0 | 0.0 | 0.0 |
| Perfect Match | 1 | 1 | 1 | 0 | 0 | 1.0 | 1.0 | 1.0 |

---

## 4. Detection Latency Definition & Percentiles

### 4.1 Latency Definition

For a telemetry batch $B$ sent at client time $t_{\text{start}}$:
1. $t_{\text{start}}$: High-precision timestamp captured immediately before sending `POST /api/v1/telemetry`.
2. $t_{\text{end}}$: Timestamp captured immediately after HTTP response header and body reading complete.
3. **End-to-End Ingestion & Detection Latency ($\mathbf{L}$):**
   $$L = t_{\text{end}} - t_{\text{start}}$$

Since anomaly detection and incident correlation occur synchronously inside `handleIngestTelemetry` (before HTTP 200 is written), $L$ measures the complete wall-clock processing time including:
- JSON decoding and payload validation
- SQLite persistence write
- Static threshold detection (`detection.Detect`)
- Incident correlation (`incidents.Correlate`)
- Incident store upsert (`s.Store.UpsertIncident`)
- JSON response encoding

### 4.2 Reported Percentiles

`cmd/vyomm-eval` must collect latency measurements $L_1, L_2, \dots, L_N$ across all batch ingest calls in a test run and calculate:
- **p50 (Median):** 50th percentile latency.
- **p95:** 95th percentile latency (SLA target threshold indicator).
- **p99:** 99th percentile latency (tail latency indicator).

#### Percentile Calculation Algorithm (Nearest Rank / Linear Interpolation):
Given sorted array of $N$ latencies $L_{(1)} \le L_{(2)} \le \dots \le L_{(N)}$:
- Rank $k = p \times (N - 1) + 1$ for percentile $p \in \{0.50, 0.95, 0.99\}$.
- If $N=0$, report `0ms` (Source: `unavailable`).

---

## 5. Output Format of `cmd/vyomm-eval`

`cmd/vyomm-eval` must output a structured JSON report matching the provenance requirements of `API_CONTRACT.md`:

```json
{
  "evaluation_id": "eval-20260818-001",
  "run_id": "run-test-001",
  "mode": "trial",
  "scenarios_evaluated": 9,
  "metrics": {
    "anomalies": {
      "true_positives": 5,
      "false_positives": 0,
      "false_negatives": 0,
      "precision": 1.0,
      "recall": 1.0,
      "f1_score": 1.0
    },
    "incidents": {
      "true_positives": 1,
      "false_positives": 0,
      "false_negatives": 0,
      "precision": 1.0,
      "recall": 1.0,
      "f1_score": 1.0
    },
    "recurrence": {
      "recurrence_tests": 1,
      "recurrences_detected": 1,
      "sequence_accuracy": 1.0
    }
  },
  "latency_ms": {
    "p50": 1.25,
    "p95": 3.10,
    "p99": 5.80,
    "unit": "ms",
    "source": "computed"
  },
  "evaluated_at": "2026-08-18T20:10:00Z"
}
```
