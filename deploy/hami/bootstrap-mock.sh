#!/usr/bin/env bash
# VYOMM nvml-mock Mode Bootstrap Script
#
# UNVERIFIED: This script has not been run on a live system.
# It is a draft for the Controller to validate.
#
# Based on: https://project-hami.io/tutorials/labs/nvml-mock
#
# Prerequisites (checked by `vyommctl doctor`):
#   - Docker running
#   - kind, kubectl, helm, go, git installed
#   - ≥ 4 CPU, ≥ 2.5 GiB available RAM, ≥ 15 GiB disk (HAMi source build)
#
# What this creates:
#   - A kind cluster named "vyomm-mock"
#   - nvml-mock simulating 8 A100 GPUs (80 virtual slots after HAMi partitioning)
#   - HAMi v2.9.0 (built from main branch) with device-plugin + scheduler
#
# What this does NOT provide:
#   - CUDA runtime execution
#   - Physical memory/compute enforcement
#   - Real DCGM metrics
#   - vgpu-monitor health (expected to crash: 1/2 READY)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

CLUSTER_NAME="vyomm-mock"
KIND_CONFIG="${REPO_ROOT}/deploy/kind/kind-config.yaml"
BUILD_DIR="${REPO_ROOT}/.build-cache"

echo "=== VYOMM nvml-mock Mode Bootstrap ==="
echo "Cluster: ${CLUSTER_NAME}"
echo "This mode builds HAMi from source — first run may take 15-30 minutes."
echo ""

mkdir -p "${BUILD_DIR}"

# Step 1: Create kind cluster
echo "[1/11] Creating kind cluster..."
kind create cluster --name "${CLUSTER_NAME}" --config "${KIND_CONFIG}"

NODE_NAME=$(kubectl get nodes -o jsonpath='{.items[0].metadata.name}')
K8S_VERSION=$(kubectl version -o json 2>/dev/null | grep -o '"gitVersion": "[^"]*"' | head -1 | cut -d'"' -f4)
echo "  Node: ${NODE_NAME}"
echo "  Kubernetes: ${K8S_VERSION}"

# Step 2: Build nvml-mock
echo "[2/11] Building nvml-mock..."
NVML_MOCK_DIR="${BUILD_DIR}/k8s-test-infra"
if [ ! -d "${NVML_MOCK_DIR}" ]; then
  git clone --depth 1 https://github.com/NVIDIA/k8s-test-infra.git "${NVML_MOCK_DIR}"
fi
docker build -t nvml-mock:local -f "${NVML_MOCK_DIR}/deployments/nvml-mock/Dockerfile" "${NVML_MOCK_DIR}"

echo "[3/11] Loading nvml-mock into kind..."
kind load docker-image nvml-mock:local --name "${CLUSTER_NAME}"

# Step 3: Install nvml-mock via Helm
echo "[4/11] Installing nvml-mock..."
helm install nvml-mock oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock \
  --set image.repository=nvml-mock \
  --set image.tag=local \
  --wait --timeout 120s

# Step 4: Verify GPU discovery
echo "[5/11] Verifying GPU discovery..."
GPU_PRESENT=$(kubectl get node "${NODE_NAME}" \
  -o jsonpath='{.metadata.labels.nvidia\.com/gpu\.present}' 2>/dev/null || echo "")
if [ "${GPU_PRESENT}" = "true" ]; then
  echo "  ✓ nvidia.com/gpu.present=true"
else
  echo "  ⚠ GPU not yet detected. Waiting 15s..."
  sleep 15
fi

# Step 5: Build HAMi from source
echo "[6/11] Building HAMi from source (main branch)..."
HAMI_DIR="${BUILD_DIR}/HAMi"
if [ ! -d "${HAMI_DIR}" ]; then
  git clone --depth 1 https://github.com/Project-HAMi/HAMi.git "${HAMI_DIR}"
  cd "${HAMI_DIR}" && git submodule update --init --recursive && cd -
fi
docker build -t hami:local -f "${HAMI_DIR}/docker/Dockerfile" "${HAMI_DIR}"

echo "[7/11] Loading HAMi image into kind..."
kind load docker-image hami:local --name "${CLUSTER_NAME}"

# Step 6: Install HAMi
echo "[8/11] Installing HAMi (device-plugin + scheduler)..."
helm install hami "${HAMI_DIR}/charts/hami" \
  -n kube-system \
  -f "${REPO_ROOT}/deploy/hami/mock-values.yaml" \
  --set scheduler.kubeScheduler.imageTag="${K8S_VERSION}"

# Step 7: Label node for HAMi device-plugin
echo "[9/11] Labeling node for HAMi device-plugin..."
kubectl label node "${NODE_NAME}" gpu=on

# Step 8: Set NVML device discovery strategy
echo "[10/11] Configuring NVML device discovery..."
kubectl -n kube-system set env daemonset/hami-device-plugin \
  -c device-plugin \
  DEVICE_DISCOVERY_STRATEGY=nvml

# Step 9: Roll out and verify
kubectl -n kube-system rollout restart daemonset/hami-device-plugin
echo "  Waiting for device-plugin rollout..."
kubectl -n kube-system rollout status daemonset/hami-device-plugin --timeout=120s 2>/dev/null || true

echo "[11/11] Verifying GPU resources..."
sleep 10
kubectl describe node "${NODE_NAME}" | grep nvidia.com/gpu || echo "  (GPU resources may still be registering)"

echo ""
echo "=== nvml-mock Mode Bootstrap Complete ==="
echo ""
kubectl -n kube-system get pods -l app.kubernetes.io/name=hami
echo ""
echo "Cluster: ${CLUSTER_NAME}"
echo "Mode:    nvml-mock"
echo "Expected: 80 virtual GPUs (8 A100 × 10 slots)"
echo ""
echo "NOTE: vgpu-monitor sidecar showing 1/2 READY is EXPECTED."
echo "NOTE: This mode validates scheduling/allocation semantics."
echo "NOTE: It does NOT prove CUDA execution or physical enforcement."
echo ""
echo "To tear down: deploy/hami/teardown-mock.sh"
