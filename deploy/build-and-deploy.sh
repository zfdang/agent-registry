#!/usr/bin/env bash
# Build both images locally (arm64), import them into every k3s node's
# containerd, and roll out. No remote image registry involved.
#
# Usage: ./deploy/build-and-deploy.sh [api|web|all]   (default: all)
set -euo pipefail

cd "$(dirname "$0")/.."

TARGET="${1:-all}"
TAG="$(date -u +%Y%m%d%H%M%S)"
NAMESPACE=agent-registry

# Node list is operator-specific and never committed. Provide it via
# REGISTRY_K3S_NODES (space-separated ssh destinations) or deploy/nodes.local
# (one per line; gitignored).
if [[ -n "${REGISTRY_K3S_NODES:-}" ]]; then
  read -r -a NODES <<< "${REGISTRY_K3S_NODES}"
elif [[ -f "deploy/nodes.local" ]]; then
  NODES=()
  while IFS= read -r line; do
    [[ -z "${line}" || "${line}" == \#* ]] && continue
    NODES+=("${line}")
  done < deploy/nodes.local
else
  echo "error: set REGISTRY_K3S_NODES=\"node1 node2 ...\" or create deploy/nodes.local" >&2
  exit 1
fi

build_and_import() { # name dockerfile context
  local name="$1" dockerfile="$2" context="$3"
  local image="localhost/agent-registry/${name}:${TAG}"
  echo "==> building ${image}"
  docker build --platform linux/arm64 -f "${dockerfile}" -t "${image}" "${context}"
  echo "==> importing ${image} into nodes: ${NODES[*]}"
  docker save "${image}" > "/tmp/agent-registry-${name}-${TAG}.tar"
  for node in "${NODES[@]}"; do
    ssh -o BatchMode=yes "${node}" 'sudo k3s ctr images import -' \
      < "/tmp/agent-registry-${name}-${TAG}.tar" >/dev/null &
  done
  wait
  rm -f "/tmp/agent-registry-${name}-${TAG}.tar"
}

echo "==> ensuring namespace + secret"
kubectl apply -f deploy/k8s/namespace.yaml
if ! kubectl -n "${NAMESPACE}" get secret registry-secrets >/dev/null 2>&1; then
  PGPASS="$(openssl rand -hex 24)"
  kubectl -n "${NAMESPACE}" create secret generic registry-secrets \
    --from-literal=POSTGRES_PASSWORD="${PGPASS}" \
    --from-literal=REGISTRY_DATABASE_URL="postgres://registry:${PGPASS}@postgres:5432/registry?sslmode=disable"
  echo "    created registry-secrets with a fresh random password"
fi

[[ "${TARGET}" == "all" || "${TARGET}" == "api" ]] && build_and_import api deploy/docker/api.Dockerfile .
[[ "${TARGET}" == "all" || "${TARGET}" == "web" ]] && build_and_import web deploy/docker/web.Dockerfile .

# Only apply the manifests being deployed: api.yaml/web.yaml carry a
# placeholder :latest image, and applying one during a partial deploy would
# reset the other deployment to a tag that exists on no node.
echo "==> applying manifests"
kubectl apply -f deploy/k8s/postgres.yaml -f deploy/k8s/ingress.yaml
[[ "${TARGET}" == "all" || "${TARGET}" == "api" ]] && kubectl apply -f deploy/k8s/api.yaml
[[ "${TARGET}" == "all" || "${TARGET}" == "web" ]] && kubectl apply -f deploy/k8s/web.yaml

if [[ "${TARGET}" == "all" || "${TARGET}" == "api" ]]; then
  kubectl -n "${NAMESPACE}" set image deploy/registry-api api="localhost/agent-registry/api:${TAG}"
fi
if [[ "${TARGET}" == "all" || "${TARGET}" == "web" ]]; then
  kubectl -n "${NAMESPACE}" set image deploy/registry-web web="localhost/agent-registry/web:${TAG}"
fi

echo "==> waiting for rollout"
kubectl -n "${NAMESPACE}" rollout status sts/postgres --timeout=180s
kubectl -n "${NAMESPACE}" rollout status deploy/registry-api --timeout=180s
kubectl -n "${NAMESPACE}" rollout status deploy/registry-web --timeout=180s

echo "==> done: https://registry.ip402.xyz (tag ${TAG})"
