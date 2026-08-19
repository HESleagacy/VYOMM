# HAMi Live-Validation Runbook

> **Status:** Step-by-step operational runbook for live HAMi cluster deployment and metrics discovery.
> **Author:** Thinker agent, Round 3.
> **Target Executor:** Controller agent or Human Operator.
> **Prerequisites:** Host system passing `vyommctl doctor` checks (>= 2.5 GiB available RAM, >= 15 GiB disk free).

---

## 1. Pre-Flight Verification

Before attempting any cluster bootstrap, execute the pre-flight resource checks specified in [`docs/hami-design-spec.md` §3](file:///home/sarvadubey/Desktop/Projects/VYOMM/docs/hami-design-spec.md):

```bash
# 1. Check Available RAM (Must be >= 2.5 GiB / 2560 MiB)
FREE_RAM_MB=$(free -m | awk '/^Mem:/ {print $7}')
echo "Available RAM: ${FREE_RAM_MB} MiB"
if [ "${FREE_RAM_MB}" -lt 2560 ]; then
  echo "FAIL: Available RAM (${FREE_RAM_MB} MiB) is below required 2560 MiB (2.5 GiB)."
  echo "Please close resource-heavy processes (browsers, IDEs) and retry."
  exit 1
fi

# 2. Check Available Disk Space (Must be >= 15 GiB)
FREE_DISK_GB=$(df -BG / | awk 'NR==2 {print $4}' | sed 's/G//')
echo "Available Disk: ${FREE_DISK_GB} GiB"
if [ "${FREE_DISK_GB}" -lt 15 ]; then
  echo "FAIL: Free disk space (${FREE_DISK_GB} GiB) is below required 15 GiB."
  exit 1
fi

# 3. Verify Required Tools
for tool in docker kind kubectl helm; do
  if ! command -v "$tool" &>/dev/null; then
    echo "FAIL: Required tool '$tool' is not installed or not in PATH."
    exit 1
  fi
done
echo "PRE-FLIGHT SUCCESS: System resources and tools verified."
```

---

## 2. Trial Mode Validation (`bootstrap-trial.sh`)

Trial mode deploys a single-node `kind` cluster with `fake-gpu-operator` and the HAMi scheduler (device plugin disabled to prevent conflicts).

### 2.1 Execution Sequence

```bash
cd /home/sarvadubey/Desktop/Projects/VYOMM
./deploy/hami/bootstrap-trial.sh
```

### 2.2 Expected Step-by-Step Success Output

| Step | Operation | Expected Success Indicator |
|------|-----------|----------------------------|
| 1/10 | Kind cluster creation | `Creating cluster "hami-lab" ...` followed by `Set kubectl context to "kind-hami-lab"` |
| 2/10 | Node detection | `Node name: hami-lab-control-plane` |
| 3/10 | Node labeling | `node/hami-lab-control-plane labeled` (`gpu=on`, `run.ai/simulated-gpu-node-pool=default`) |
| 4/10 | fake-gpu-operator install | `STATUS: deployed` in `gpu-operator` namespace |
| 5/10 | Topology labeling | `node/hami-lab-control-plane labeled` (`nvidia.com/gpu.product=Tesla-K80`, `memory=11441`) |
| 6/10 | Node Capacity check | `nvidia.com/gpu: 2` visible in `kubectl describe node` |
| 7/10 | Node Annotation | `hami.io/node-nvidia-register` JSON array present on node |
| 8/10 | HAMi Repo Add | `"hami-charts" has been added to your repositories` |
| 9/10 | HAMi Scheduler install | `STATUS: deployed` in `kube-system` namespace |
| 10/10 | Verification | `hami-scheduler` Pod status `Running (1/1 or 2/2)` |

### 2.3 Verification Commands

```bash
# Verify Pod Status
kubectl get pods -n kube-system -l app.kubernetes.io/name=hami
# Expected: hami-scheduler-xxxxx 2/2 Running

kubectl get pods -n gpu-operator
# Expected: fake-gpu-operator and dcgm-exporter running

# Verify Simulated GPU Capacity
kubectl get node hami-lab-control-plane -o jsonpath='{.status.capacity}'
# Expected: contains "nvidia.com/gpu":"2"
```

---

## 3. `nvml-mock` Mode Validation (`bootstrap-mock.sh`)

`nvml-mock` mode builds HAMi from `main` branch source and uses NVIDIA's `nvml-mock` library to simulate 8 A100 GPUs (80 virtual GPU slots).

> [!WARNING]
> Building HAMi from source takes 10–20 minutes and requires ~2 GiB of free disk for Docker layer caching.

### 3.1 Execution Sequence

```bash
cd /home/sarvadubey/Desktop/Projects/VYOMM
./deploy/hami/bootstrap-mock.sh
```

### 3.2 Expected Step-by-Step Success Output

| Step | Operation | Expected Success Indicator |
|------|-----------|----------------------------|
| 1 | Kind cluster creation | Cluster `nvml-mock-test` created |
| 2 | Build `nvml-mock` | Docker image `nvml-mock:local` built |
| 3 | Load `nvml-mock` image | `Image: "nvml-mock:local" load to kind cluster "nvml-mock-test" complete` |
| 4 | Helm install `nvml-mock` | Release `nvml-mock` deployed |
| 5 | Verify GPU Discovery | `GPU_PRESENT: true` on node |
| 6 | Clone & Build HAMi `main` | Docker image `hami:local` built |
| 7 | Load `hami:local` image | `Image: "hami:local" load complete` |
| 8 | Install HAMi Helm chart | Release `hami` deployed in `kube-system` |
| 9 | Label node `gpu=on` | `node/... labeled` |
| 10 | Discovery Strategy env | Set `DEVICE_DISCOVERY_STRATEGY=nvml` on `hami-device-plugin` |
| 11 | Rollout status | `daemonset "hami-device-plugin" successfully rolled out` |

> [!NOTE]
> In Step 11, `hami-device-plugin` pod status showing `1/2 READY (CrashLoopBackOff for vgpu-monitor sidecar)` is **EXPECTED and NORMAL** per Lab 5 tutorial, because `vgpu-monitor` requires real DCGM hardware metrics. `device-plugin` container itself runs fine.

### 3.3 Resource Verification Command

```bash
NODE_NAME=$(kubectl get nodes -o jsonpath='{.items[0].metadata.name}')
kubectl describe node ${NODE_NAME} | grep nvidia.com/gpu
# Expected Output:
# nvidia.com/gpu.present=true
# nvidia.com/gpu: 80
# nvidia.com/gpu: 80
```

---

## 4. Live Metric Discovery Procedure

Per `METRICS_CONTRACT.md`, metric names must **never be invented**. The Controller must run this procedure on the live cluster and record the exact output.

### 4.1 Discovery Command Sequence

Run this while either `trial` or `nvml-mock` environment is running:

```bash
RUN_ID="run-discovery-$(date +%Y%m%d-%H%M%S)"
OUTPUT_DIR="artifacts/runs/${RUN_ID}/metrics"
mkdir -p "${OUTPUT_DIR}"

echo "=== 1. Discovering HAMi Scheduler Endpoint Metrics ===" > "${OUTPUT_DIR}/discovered-metrics.txt"
# Port-forward or direct curl to hami-scheduler port 31993
SCHED_POD=$(kubectl -n kube-system get pod -l app.kubernetes.io/name=hami -o jsonpath='{.items[0].metadata.name}')
kubectl -n kube-system exec "${SCHED_POD}" -c hami-scheduler -- curl -s http://localhost:31993/metrics >> "${OUTPUT_DIR}/discovered-metrics.txt" 2>&1 || echo "Scheduler endpoint direct curl failed" >> "${OUTPUT_DIR}/discovered-metrics.txt"

echo -e "\n=== 2. Discovering fake-gpu-operator DCGM Exporter Metrics ===" >> "${OUTPUT_DIR}/discovered-metrics.txt"
DCGM_POD=$(kubectl -n gpu-operator get pod -l app=nvidia-dcgm-exporter -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)
if [ -n "${DCGM_POD}" ]; then
  kubectl -n gpu-operator exec "${DCGM_POD}" -- curl -s http://localhost:9400/metrics | grep -E '^# HELP|^# TYPE|^DCGM_' >> "${OUTPUT_DIR}/discovered-metrics.txt"
else
  echo "DCGM exporter pod not present in current mode." >> "${OUTPUT_DIR}/discovered-metrics.txt"
fi

echo "Discovery completed. Output saved to: ${OUTPUT_DIR}/discovered-metrics.txt"
```

---

## 5. Rollback & Teardown Checklist

If any bootstrap script fails or needs clean teardown:

### 5.1 Trial Mode Teardown

```bash
./deploy/hami/teardown-trial.sh
# Verification: kind get clusters should not contain "hami-lab"
```

### 5.2 `nvml-mock` Mode Teardown

```bash
./deploy/hami/teardown-mock.sh
# Verification: kind get clusters should not contain "nvml-mock-test"
```

### 5.3 Emergency Force Cleanup

```bash
kind delete cluster --name hami-lab 2>/dev/null || true
kind delete cluster --name nvml-mock-test 2>/dev/null || true
docker system prune -f --volumes
rm -rf .build-cache/
```
