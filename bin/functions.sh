describe() {
  echo "--- $1…"
}

squelch() {
  $@ > /dev/null 2>&1
}

brew_bundle_install() {
  if [ -f Brewfile ]; then
    brew bundle check || {
      describe "Install Homebrew dependencies"
      brew bundle
    }
  fi
}

k8s_apply_kustomize() {
  local kubecontext=$1
  describe ":kubernetes: applying manifests"
  kubectl --context ${kubecontext} apply -f k8s/metrics.yaml
  kubectl --context ${kubecontext} apply -f k8s/buildkite.yaml

}

k8s_destroy_kustomize() {
  local kubecontext=$1
  describe ":kubernetes: Destroying resources"
  kubectl --context ${kubecontext} delete -f k8s/metrics.yaml
  kubectl --context ${kubecontext} delete -f k8s/buildkite.yaml
}

k8s_wait_for_deployment() {
  local kubecontext=$1
  local kubenamespace=$2
  local deploymentname=$3
  describe ":kubernetes: Waiting for deployments/${deploymentname}"
  kubectl \
    --context ${kubecontext} \
    --namespace ${kubenamespace} \
    wait --for=condition=available \
    deployments/${deploymentname} \
    --timeout=120s
}

kind_create_cluster() {
  describe ":kubernetes: Creating kind cluster"
  kind create cluster --image kindest/node:v1.23.10
}

kind_delete_cluster() {
  describe ":kubernetes: Deleting kind cluster"
  kind delete cluster
}
