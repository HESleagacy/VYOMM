#!/usr/bin/env bash
# Teardown VYOMM nvml-mock mode cluster
set -euo pipefail

CLUSTER_NAME="vyomm-mock"

echo "Tearing down nvml-mock mode..."
helm uninstall hami -n kube-system 2>/dev/null || true
helm uninstall nvml-mock 2>/dev/null || true
kind delete cluster --name "${CLUSTER_NAME}"
echo "Done."
