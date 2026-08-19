#!/usr/bin/env bash
# VYOMM Trial Mode Bootstrap Script
#
# UNVERIFIED: This script has not been run on a live system.
# It is a draft for the Controller to validate.
#
# Based on: https://project-hami.io/tutorials/labs/local-fake-gpu
#
# Prerequisites (checked by `vyommctl doctor`):
#   - Docker running
#   - kind, kubectl, helm installed
#   - ≥ 4 CPU, ≥ 2.5 GiB available RAM
#
# What this creates:
#   - A kind cluster named "vyomm-trial"
#   - fake-gpu-operator v0.0.80 simulating 2 Tesla K80 GPUs
#   - HAMi v2.9.0 scheduler (device-plugin disabled)
#
# What this does NOT provide:
#   - HAMi device-plugin GPU registration
#   - Memory/compute slicing
#   - CUDA runtime
#   - Real DCGM metrics

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

CLUSTER_NAME="vyomm-trial"
FAKE_GPU_OPERATOR_VERSION="0.0.80"
HAMI_CHART_VERSION="2.9.0"
KIND_CONFIG="${REPO_ROOT}/deploy/kind/kind-config.yaml"

echo "=== VYOMM Trial Mode Bootstrap ==="
echo "Cluster: ${CLUSTER_NAME}"
echo "HAMi: v${HAMI_CHART_VERSION}"
echo "fake-gpu-operator: v${FAKE_GPU_OPERATOR_VERSION}"
echo ""

# Step 1: Create kind cluster
echo "[1/10] Creating kind cluster..."
kind create cluster --name "${CLUSTER_NAME}" --config "${KIND_CONFIG}"

# Step 2: Get node name
NODE_NAME=$(kubectl get nodes -o jsonpath='{.items[0].metadata.name}')
K8S_VERSION=$(kubectl version -o json 2>/dev/null | grep -o '"gitVersion": "[^"]*"' | head -1 | cut -d'"' -f4)
echo "  Node: ${NODE_NAME}"
echo "  Kubernetes: ${K8S_VERSION}"

# Step 3: Create gpu-operator namespace
echo "[2/10] Creating gpu-operator namespace..."
kubectl create namespace gpu-operator
kubectl label namespace gpu-operator pod-security.kubernetes.io/enforce=privileged

# Step 4: Label node for fake-gpu-operator
echo "[3/10] Labeling node for fake-gpu-operator..."
kubectl label node "${NODE_NAME}" run.ai/simulated-gpu-node-pool=default

# Step 5: Install fake-gpu-operator
echo "[4/10] Installing fake-gpu-operator v${FAKE_GPU_OPERATOR_VERSION}..."
helm upgrade -i gpu-operator \
  oci://ghcr.io/run-ai/fake-gpu-operator/fake-gpu-operator \
  --namespace gpu-operator \
  --create-namespace \
  --version "${FAKE_GPU_OPERATOR_VERSION}"

# Step 6: Wait for pods
echo "[5/10] Waiting for fake-gpu-operator pods..."
sleep 10
kubectl wait --for=condition=Ready pod --all \
  -n gpu-operator --timeout=180s 2>/dev/null || echo "  (some pods may still be starting)"

# Step 7: Wait for GPU registration
echo "[6/10] Waiting for GPU resource registration (30s)..."
sleep 30

# Step 8: Verify GPU capacity
echo "[7/10] Verifying GPU capacity..."
GPU_COUNT=$(kubectl get node "${NODE_NAME}" -o jsonpath='{.status.capacity.nvidia\.com/gpu}' 2>/dev/null || echo "0")
if [ "${GPU_COUNT}" = "0" ] || [ -z "${GPU_COUNT}" ]; then
  echo "  ⚠ WARNING: GPU capacity not yet reported. Check: kubectl get pods -n gpu-operator"
else
  echo "  ✓ Node reports ${GPU_COUNT} simulated GPUs"
fi

# Step 9: Install HAMi (scheduler only)
echo "[8/10] Adding HAMi Helm repo..."
helm repo add hami-charts https://project-hami.github.io/HAMi/ 2>/dev/null || true
helm repo update

echo "[9/10] Installing HAMi v${HAMI_CHART_VERSION} (scheduler only)..."
helm install hami hami-charts/hami \
  -n kube-system \
  --version "${HAMI_CHART_VERSION}" \
  -f "${REPO_ROOT}/deploy/hami/trial-values.yaml" \
  --set scheduler.kubeScheduler.imageTag="${K8S_VERSION}"

# Step 10: Wait and verify
echo "[10/10] Waiting for HAMi scheduler..."
sleep 15
kubectl wait --for=condition=Ready pod -l app.kubernetes.io/name=hami \
  -n kube-system --timeout=120s 2>/dev/null || echo "  (scheduler may still be starting)"

echo ""
echo "=== Trial Mode Bootstrap Complete ==="
echo ""
kubectl get pods -n gpu-operator
echo ""
kubectl get pods -n kube-system | grep hami
echo ""
echo "Cluster: ${CLUSTER_NAME}"
echo "Mode:    trial"
echo ""
echo "NOTE: This mode proves HAMi scheduler operation with simulated GPUs."
echo "NOTE: It does NOT prove memory slicing, compute limits, or CUDA execution."
echo ""
echo "To tear down: deploy/hami/teardown-trial.sh"
