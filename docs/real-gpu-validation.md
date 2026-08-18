# Real GPU Validation Plan

> **Status:** PLACEHOLDER — Phase 4 covers trial and nvml-mock modes only.
> Real GPU validation is Phase 6+ work, gated on having access to a machine
> with a physical NVIDIA GPU.

## Purpose

This document will define the procedure for validating VYOMM against a real
NVIDIA GPU with the full HAMi stack (not simulated). It does not apply to
the current development phase.

## Prerequisites (not yet available)

- Machine or cloud instance with ≥1 NVIDIA GPU (e.g., T4, A100)
- NVIDIA driver v440+
- CUDA toolkit v10.2+
- nvidia-container-toolkit configured
- Kubernetes cluster with GPU node(s)

## What real-gpu mode would validate that trial/nvml-mock cannot

1. CUDA program execution inside HAMi-managed containers
2. Physical memory enforcement by hami-core library
3. Physical compute limit enforcement
4. Real DCGM metric values (temperature, utilization, memory usage)
5. vgpu-monitor sidecar health (currently crashes in nvml-mock mode)
6. End-to-end flow: real GPU telemetry → VYOMM ingest → anomaly detection → incident

## Metric discovery for real-gpu mode

The `METRICS_CONTRACT.md` discovery procedure applies identically:

1. Deploy HAMi + GPU Operator on real GPU cluster
2. `curl` the DCGM exporter endpoint
3. Save raw scrape to `artifacts/runs/<run-id>/metrics/discovered-metrics.txt`
4. Record verified metric names in `docs/modes.md`
5. Do not assume metric names from documentation — verify from live scrape

## Timeline

Blocked on hardware access. Will be scheduled after Phase 4 nvml-mock
validation is complete and the Controller confirms scheduling semantics
work correctly in the kind cluster.
