# HAMi Integration Design Spec (Phase 4 Interface Contract)

> **Author:** Thinker agent
> **Status:** DRAFT — not yet validated by Controller on a live cluster
> **Pinned HAMi version:** `v2.9.0` (Helm chart `hami-2.9.0`, latest stable release per
> [project-hami.io/changelog](https://project-hami.io/changelog))
> **Pinned fake-gpu-operator version:** `0.0.80`
> (OCI chart `ghcr.io/run-ai/fake-gpu-operator/fake-gpu-operator:0.0.80`)

This file is an interface specification the Controller uses to implement
`internal/hami/` in Go. It defines inputs, outputs, error messages, and
exact command sequences — never pseudocode.

---

## 1. Helm Decision and Justification

**Decision: Helm is required for both HAMi and nvml-mock. This satisfies the
project constraint.**

Justification: HAMi's *only* officially supported install path is its Helm
chart (`hami-charts/hami`). There are no raw-manifest alternatives
documented or maintained. The nvml-mock chart
(`oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock`) likewise ships only
as a Helm chart. The fake-gpu-operator for trial mode also installs via Helm
OCI chart. Since all three components exclusively distribute as Helm charts,
the Helm requirement is inherent to the toolchain, not a discretionary
choice by VYOMM.

---

## 2. Resource Requirements

### Current machine profile

| Resource | Available | Notes |
|----------|-----------|-------|
| CPU cores | 4 | `nproc` output |
| Total RAM | 7.6 GiB | Already 5.6 GiB in use, 1.2 GiB available |
| Swap | 11 GiB | 5.4 GiB used |

### Trial mode resource budget

| Component | CPU request | Memory request |
|-----------|------------|----------------|
| kind cluster (1 control-plane node) | — | ~700 MiB |
| fake-gpu-operator (6 pods) | ~100m total | ~200 MiB |
| HAMi scheduler (1 pod, 2 containers) | ~100m | ~128 MiB |
| **Total trial-mode overhead** | **~1 CPU core** | **~1.5 GiB** |

### nvml-mock mode resource budget

| Component | CPU request | Memory request |
|-----------|------------|----------------|
| kind cluster (1 control-plane node) | — | ~700 MiB |
| nvml-mock DaemonSet | ~50m | ~64 MiB |
| HAMi device-plugin + scheduler | ~200m | ~256 MiB |
| **Total nvml-mock overhead** | **~1 CPU core** | **~1.5 GiB** |

### ⚠️ Resource warning for this specific machine

This machine has only 4 CPU / 7.6 GiB total, with heavy existing usage
(1.2 GiB available, 5.4 GiB swap used). Running either mode will push
the system into heavier swap territory. `make doctor` MUST refuse to
proceed rather than half-start a cluster.

**Recommended minimums for `make doctor`:**

| Resource | Trial mode | nvml-mock mode |
|----------|-----------|----------------|
| CPU cores | ≥ 4 | ≥ 4 |
| Available RAM (not total) | ≥ 2.5 GiB | ≥ 2.5 GiB |
| Disk free | ≥ 10 GiB | ≥ 15 GiB (HAMi source build) |

---

## 3. Doctor Check Specification

The `vyommctl doctor` (or `make doctor`) command must check each
prerequisite and print a clear pass/fail with an actionable message.
Checks are evaluated in order; on the first `FATAL`, the command exits 1.

### 3.1 Check table

| # | Check | How to detect | PASS message | FAIL message (exact) | Severity |
|---|-------|--------------|-------------|---------------------|----------|
| 1 | Docker daemon running | `docker info --format '{{.ServerVersion}}'` exit 0 | `✓ Docker <version>` | `✗ FATAL: Docker is not running. Install: https://docs.docker.com/engine/install/` | FATAL |
| 2 | kind installed | `kind version` exit 0 | `✓ kind <version>` | `✗ FATAL: kind not found. Install: https://kind.sigs.k8s.io/docs/user/quick-start/#installation` | FATAL |
| 3 | kubectl installed | `kubectl version --client --short 2>/dev/null` exit 0 | `✓ kubectl <version>` | `✗ FATAL: kubectl not found. Install: https://kubernetes.io/docs/tasks/tools/` | FATAL |
| 4 | Helm 3.x installed | `helm version --short` exit 0, starts with `v3.` | `✓ Helm <version>` | `✗ FATAL: Helm 3.x not found. Install: https://helm.sh/docs/intro/install/` | FATAL |
| 5 | CPU cores ≥ 4 | `nproc` ≥ 4 | `✓ CPU cores: <n>` | `✗ FATAL: Need ≥ 4 CPU cores, found <n>. Cannot run kind cluster safely.` | FATAL |
| 6 | Available RAM ≥ 2.5 GiB | Parse `MemAvailable` from `/proc/meminfo`, ≥ 2621440 KiB | `✓ Available RAM: <n> GiB` | `✗ FATAL: Need ≥ 2.5 GiB available RAM, found <n> GiB. Close other programs or add RAM.` | FATAL |
| 7 | Disk space ≥ 10 GiB | `df` on workspace mount, available ≥ 10 GiB | `✓ Disk free: <n> GiB` | `✗ FATAL: Need ≥ 10 GiB free disk, found <n> GiB.` | FATAL |
| 8 | Go installed (nvml-mock only) | `go version` exit 0 | `✓ Go <version>` | `✗ WARN: Go not found — needed only for nvml-mock mode (HAMi source build). Install: https://go.dev/dl/` | WARN |
| 9 | NVIDIA driver (real-gpu only) | `nvidia-smi --query-gpu=driver_version --format=csv,noheader` | `✓ NVIDIA driver <version>` | `✗ INFO: No NVIDIA driver detected. Trial and nvml-mock modes work without it.` | INFO |

### 3.2 Exit behavior

- Any `FATAL` → print all checks run so far, exit 1.
- Any `WARN` → print warning, continue.
- All pass → print `All checks passed. Ready for mode: <trial|nvml-mock>`, exit 0.

---

## 4. Bootstrap Command Sequences

### 4.1 `bootstrap-trial` — Trial Mode (fake-gpu-operator + HAMi scheduler only)

Based on [Lab 2: Local Fake GPU](https://project-hami.io/tutorials/labs/local-fake-gpu).

```bash
#!/usr/bin/env bash
set -euo pipefail

CLUSTER_NAME="vyomm-trial"
FAKE_GPU_OPERATOR_VERSION="0.0.80"
HAMI_CHART_VERSION="2.9.0"

# Step 1: Create kind cluster
kind create cluster --name "${CLUSTER_NAME}" --config deploy/kind/kind-config.yaml

# Step 2: Set NODE_NAME
NODE_NAME=$(kubectl get nodes -o jsonpath='{.items[0].metadata.name}')
echo "NODE_NAME=${NODE_NAME}"

# Step 3: Create gpu-operator namespace with privileged PSA
kubectl create namespace gpu-operator
kubectl label namespace gpu-operator pod-security.kubernetes.io/enforce=privileged

# Step 4: Label node for fake-gpu-operator
kubectl label node "${NODE_NAME}" run.ai/simulated-gpu-node-pool=default

# Step 5: Install fake-gpu-operator via Helm OCI
helm upgrade -i gpu-operator \
  oci://ghcr.io/run-ai/fake-gpu-operator/fake-gpu-operator \
  --namespace gpu-operator \
  --create-namespace \
  --version "${FAKE_GPU_OPERATOR_VERSION}"

# Step 6: Wait for fake-gpu-operator pods
kubectl wait --for=condition=Ready pod -l app.kubernetes.io/instance=gpu-operator \
  -n gpu-operator --timeout=180s || true
echo "Waiting 30s for device registration..."
sleep 30

# Step 7: Verify GPU capacity on node
kubectl get node "${NODE_NAME}" \
  -o custom-columns=NAME:.metadata.name,GPU:.status.capacity.nvidia\\.com/gpu

# Step 8: Add HAMi Helm repo
helm repo add hami-charts https://project-hami.github.io/HAMi/
helm repo update

# Step 9: Install HAMi (scheduler only, device-plugin disabled)
helm install hami hami-charts/hami \
  -n kube-system \
  --version "${HAMI_CHART_VERSION}" \
  --set devicePlugin.enabled=false

# Step 10: Wait for HAMi scheduler
kubectl wait --for=condition=Ready pod -l app.kubernetes.io/name=hami \
  -n kube-system --timeout=120s || true

# Step 11: Verify
kubectl get pods -n kube-system | grep hami
echo ""
echo "=== Trial mode bootstrap complete ==="
echo "HAMi scheduler is running. fake-gpu-operator provides simulated nvidia.com/gpu resources."
echo "NOTE: This mode does NOT prove memory slicing, compute limits, or CUDA execution."
```

### 4.2 `bootstrap-mock` — nvml-mock Mode (full HAMi device-plugin + scheduler)

Based on [Lab 5: nvml-mock](https://project-hami.io/tutorials/labs/nvml-mock).

```bash
#!/usr/bin/env bash
set -euo pipefail

CLUSTER_NAME="vyomm-mock"
HAMI_CHART_VERSION="2.9.0"

# Step 1: Create kind cluster
kind create cluster --name "${CLUSTER_NAME}" --config deploy/kind/kind-config.yaml

NODE_NAME=$(kubectl get nodes -o jsonpath='{.items[0].metadata.name}')
echo "NODE_NAME=${NODE_NAME}"

# Step 2: Build nvml-mock from NVIDIA k8s-test-infra
NVML_MOCK_DIR=$(mktemp -d)
git clone --depth 1 https://github.com/NVIDIA/k8s-test-infra.git "${NVML_MOCK_DIR}"
docker build -t nvml-mock:local -f "${NVML_MOCK_DIR}/deployments/nvml-mock/Dockerfile" "${NVML_MOCK_DIR}"
kind load docker-image nvml-mock:local --name "${CLUSTER_NAME}"

# Step 3: Install nvml-mock via Helm OCI
helm install nvml-mock oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock \
  --set image.repository=nvml-mock \
  --set image.tag=local \
  --wait --timeout 120s

# Step 4: Verify GPU node label
kubectl get node "${NODE_NAME}" \
  -o custom-columns=NAME:.metadata.name,GPU_PRESENT:.metadata.labels.nvidia\\.com/gpu\\.present

# Step 5: Build HAMi from source (main branch, contains nvml-mock fixes)
HAMI_DIR=$(mktemp -d)
git clone --depth 1 https://github.com/Project-HAMi/HAMi.git "${HAMI_DIR}"
cd "${HAMI_DIR}" && git submodule update --init --recursive
docker build -t hami:local -f docker/Dockerfile .
kind load docker-image hami:local --name "${CLUSTER_NAME}"
cd -

# Step 6: Install HAMi via Helm (from cloned charts, using local images)
helm install hami "${HAMI_DIR}/charts/hami" \
  -n kube-system \
  --set devicePlugin.image.repository=hami \
  --set devicePlugin.image.tag=local \
  --set scheduler.image.repository=hami \
  --set scheduler.image.tag=local \
  --set devicePlugin.nvidiaDriverRoot=/var/lib/nvml-mock/driver \
  --set scheduler.kubeScheduler.imageTag=v1.32.2

# Step 7: Label node for HAMi device-plugin
kubectl label node "${NODE_NAME}" gpu=on

# Step 8: Set NVML device discovery strategy
kubectl -n kube-system set env daemonset/hami-device-plugin \
  -c device-plugin \
  DEVICE_DISCOVERY_STRATEGY=nvml

# Step 9: Roll out and wait
kubectl -n kube-system rollout restart daemonset/hami-device-plugin
kubectl -n kube-system rollout status daemonset/hami-device-plugin --timeout=120s || true

# Step 10: Verify 80 virtual GPUs (8 A100 × 10 slots each)
kubectl describe node "${NODE_NAME}" | grep nvidia.com/gpu

# Step 11: Check HAMi pods
kubectl -n kube-system get pods -l app.kubernetes.io/name=hami

echo ""
echo "=== nvml-mock bootstrap complete ==="
echo "HAMi device-plugin + scheduler running with 8 simulated A100 GPUs."
echo "NOTE: The vgpu-monitor sidecar (1/2 READY) is expected — it requires real GPU monitoring."
echo "NOTE: This mode validates scheduling/allocation semantics, NOT CUDA execution or physical enforcement."
```

---

## 5. HAMi Metrics and Endpoints

### 5.1 HAMi scheduler service

The HAMi scheduler deploys a Kubernetes Service `hami-scheduler` in `kube-system`:

```
service/hami-scheduler  NodePort  <ClusterIP>  443:31998/TCP,31993:31993/TCP
```

- Port `31993` is the scheduler's metrics/info HTTP port.
- Port `443` (mapped to `31998`) is the webhook HTTPS port.

### 5.2 Metrics endpoint status

> **⚠️ COULD NOT VERIFY — needs live cluster.**
>
> The HAMi scheduler exposes an HTTP endpoint on port `31993` that the
> `hami-scheduler` Service makes available. Based on the Helm chart and
> source inspection, this endpoint likely exposes scheduling metrics, but
> the **exact metric names** cannot be determined without running
> `curl http://<node-ip>:31993/metrics` on a live cluster.
>
> Per `METRICS_CONTRACT.md`, invented HAMi metric names are not acceptable.
> The Controller must run the discovery procedure (§5.3) on a live cluster.

### 5.3 Metric discovery procedure for Controller (Phase 4 validation)

Once the cluster is running, the Controller must execute:

```bash
# 1. Port-forward the HAMi scheduler metrics port
kubectl -n kube-system port-forward svc/hami-scheduler 31993:31993 &
PF_PID=$!
sleep 3

# 2. Scrape raw metrics
RUN_ID="run-$(date +%Y-%m-%d)-0001"
mkdir -p "artifacts/runs/${RUN_ID}/metrics"
curl -s http://localhost:31993/metrics > "artifacts/runs/${RUN_ID}/metrics/discovered-metrics.txt"
kill ${PF_PID}

# 3. In trial mode with fake-gpu-operator + DCGM exporter:
# Also scrape the DCGM exporter if Prometheus is deployed
kubectl -n monitoring exec prometheus-prometheus-kube-prometheus-prometheus-0 -- \
  promtool query instant http://localhost:9090 'DCGM_FI_DEV_GPU_UTIL'
```

### 5.4 Known metrics from fake-gpu-operator (trial mode)

The fake-gpu-operator deploys a simulated `nvidia-dcgm-exporter` DaemonSet.
From the Lab 2 tutorial, the confirmed metric name is:

- `DCGM_FI_DEV_GPU_UTIL` — simulated GPU utilization (always 0 in fake mode)

This is the DCGM metric format. Additional DCGM metrics (e.g.,
`DCGM_FI_DEV_GPU_TEMP`, `DCGM_FI_DEV_MEM_COPY_UTIL`) likely exist but
**have not been verified from a live scrape** — they must be discovered
per §5.3, not assumed.

### 5.5 HAMi annotations (observable without metrics scrape)

HAMi records allocation data as pod annotations, not as Prometheus metrics.
These are the confirmed annotation keys from the tutorials:

| Annotation | Set by | Content |
|-----------|--------|---------|
| `hami.io/vgpu-devices-allocated` | HAMi scheduler | `<UUID>,<vendor>,<memMiB>,<cores%>:;` per vGPU slot |
| `hami.io/node-nvidia-register` | HAMi device-plugin (or manual in trial mode) | JSON array of GPU device registrations |
| `hami.io/node-handshake` | HAMi device-plugin | Handshake timestamp |

### 5.6 VYOMM integration points

VYOMM's Go code (`internal/hami/`) should:

1. **Scrape the HAMi scheduler endpoint** (port 31993) for scheduling metrics.
   Record raw output to `artifacts/runs/<run-id>/metrics/discovered-metrics.txt`.
   Set `vyomm_hami_scrape_success{mode="<mode>"}` gauge to 1 on success, 0 on failure.

2. **Watch pod annotations** for `hami.io/vgpu-devices-allocated` to track
   allocation events. Increment `vyomm_scheduler_allocation_events_total`
   with `event_type` parsed from the annotation lifecycle:
   - `requested` = pod created with `nvidia.com/gpu` resource
   - `allocated` = annotation present
   - `rejected` = pod stuck in Pending with scheduling failure event

3. **Never fabricate** HAMi metric names. If a signal is unavailable, use
   the `vyomm_synthetic_*` prefix per `METRICS_CONTRACT.md`.

---

## 6. Teardown Commands

### Trial mode
```bash
helm uninstall hami -n kube-system
helm uninstall gpu-operator -n gpu-operator
kubectl delete namespace gpu-operator
kind delete cluster --name vyomm-trial
```

### nvml-mock mode
```bash
helm uninstall hami -n kube-system
helm uninstall nvml-mock
kind delete cluster --name vyomm-mock
```

---

## 7. Open Questions for Controller

1. **kubeScheduler.imageTag**: The nvml-mock tutorial pins this to `v1.35.0`
   but kind ships `kindest/node:v1.32.2` by default. The Helm value
   `scheduler.kubeScheduler.imageTag` must match the kind node's Kubernetes
   version. The bootstrap script should auto-detect this:
   ```bash
   K8S_VERSION=$(kubectl version -o json | jq -r '.serverVersion.gitVersion')
   ```
   and pass `--set scheduler.kubeScheduler.imageTag=${K8S_VERSION}`.

2. **HAMi source build time**: Building HAMi from source (nvml-mock mode)
   involves a multi-stage Docker build with CUDA base images. On this
   4-CPU/7.6GiB machine, this could take 15-30 minutes on first run.
   Consider pre-building and caching the image, or providing a
   `--skip-build` flag that uses a pre-built image tag from GHCR.

3. **Swap pressure**: With current system load (1.2 GiB available, 5.4 GiB
   swap), running kind + HAMi will almost certainly cause significant swap
   activity. The `doctor` check should be strict about available (not total)
   RAM and warn accordingly.
