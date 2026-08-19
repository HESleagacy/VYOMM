#!/usr/bin/env bash
# Teardown VYOMM trial mode cluster
set -euo pipefail

CLUSTER_NAME="vyomm-trial"

echo "Tearing down trial mode..."
helm uninstall hami -n kube-system 2>/dev/null || true
helm uninstall gpu-operator -n gpu-operator 2>/dev/null || true
kubectl delete namespace gpu-operator 2>/dev/null || true
kind delete cluster --name "${CLUSTER_NAME}"
echo "Done."
