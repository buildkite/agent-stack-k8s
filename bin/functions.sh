describe() {
  echo "--- $1…"
}

squelch() {
  $@ > /dev/null 2>&1
}

asdf_add_plugins() {
  if [ -f .tool-versions ]; then
    describe "Add asdf language plugins"
    asdf plugin add golang || true
  fi
}

asdf_install_tools() {
  if [ -f .tool-versions ]; then
    describe ":golang: Install language versions"
    asdf install
  fi
}

asdf_update_plugins() {
  if [ -f .tool-versions ]; then
    describe "Update asdf language plugins"
    asdf plugin-update --all
  fi
}

brew_bundle_install() {
  if [ -f Brewfile ]; then
    brew bundle check || {
      describe "Install Homebrew dependencies"
      brew bundle
    }
  fi
}

buildkite_queue_busywork() {
  local times=$1
  local token=$2
  describe "Queuing busywork ${times} times"
  for i in $(seq 1 ${times})
  do
    curl -s --output /dev/null -H "Authorization: Bearer $token" "https://api.buildkite.com/v2/organizations/buildkite-kubernetes-stack/pipelines/busywork/builds" \
      -X "POST" \
      -F "commit=HEAD" \
      -F "branch=main" \
      -F "message=build ${i}"
  done
}

date_utc_now() {
  date -u +"%Y-%m-%dT%H:%M:%SZ"
}

k8s_apply_kustomize() {
  local kubecontext=$1
  local overlay=$2
  describe ":kubernetes: Applying kustomize template $overlay"
  kustomize build k8s/metrics | kubectl --context ${kubecontext} apply -f -
  kustomize build k8s/buildkite | kubectl --context ${kubecontext} apply -f -
}

k8s_destroy_kustomize() {
  local kubecontext=$1
  describe ":kubernetes: Destroying kustomize resources"
  kustomize build k8s/buildkite | kubectl --context ${kubecontext} delete -f -
  kustomize build k8s/metrics | kubectl --context ${kubecontext} delete -f -
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

k8s_watch_hpa() {
  local kubecontext=$1
  local kubenamespace=$2
  local hpaname=$3
  while :
  do
    date_utc_now
    kubectl \
      --context ${kubecontext} \
      --namespace ${kubenamespace} \
      get hpa/${hpaname}
    sleep 5
  done
}

kind_create_cluster() {
  describe ":kubernetes: Creating kind cluster"
  kind create cluster --image kindest/node:v1.23.10
}

kind_delete_cluster() {
  describe ":kubernetes: Deleting kind cluster"
  kind delete cluster
}
