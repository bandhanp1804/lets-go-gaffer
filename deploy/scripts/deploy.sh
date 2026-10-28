#!/usr/bin/env bash
# Deploys lets-go-gaffer to Azure. Run from the repository root.
#
#   deploy/scripts/deploy.sh infra   # Terraform: network, AKS, registry, Postgres, budget; prints the host
#   deploy/scripts/deploy.sh app     # images, platform charts (Traefik, cert-manager, Temporal,
#                                    # oauth2-proxy) and the app chart. Safe to rerun to roll out.
#   deploy/scripts/deploy.sh status  # pods, autoscalers and the address
#
# Needs: az (logged in: az login), terraform, kubectl, helm, docker with buildx.
# Secrets never pass through Terraform: they go from .env and deploy/azure.env straight into
# Kubernetes Secrets.
set -euo pipefail
# Paths below are from the repository root: run from there, whatever folder this was started in.
cd "$(dirname "$0")/../.."

TF=deploy/terraform
NS=gaffer
CHARTS=deploy/helm

tf() { terraform -chdir="$TF" "$@"; }
out() { tf output -raw "$1"; }
die() { echo "deploy: $*" >&2; exit 1; }

# envvar KEY FILE: the value of KEY from the environment (as in CI), else from FILE, never echoed.
envvar() {
  if [ -n "${!1:-}" ]; then printf '%s' "${!1}"; return; fi
  grep -E "^$1=" "$2" 2>/dev/null | head -1 | cut -d= -f2- || true
}

# In GitHub Actions there is no .env, no tfvars and nobody to answer a prompt.
CI=${CI:-}

init() { tf init -input=false -backend-config=backend.hcl >/dev/null; }

infra() {
  az account show >/dev/null 2>&1 || die "run az login first"
  [ -n "$CI" ] || [ -f "$TF/terraform.tfvars" ] || die "copy $TF/terraform.tfvars.example to terraform.tfvars and fill it in"
  init
  if [ -n "$CI" ]; then
    tf apply -input=false -auto-approve # the approval was the production environment's
  else
    tf apply -input=false
  fi
  eval "$(out get_credentials)"
  echo
  echo "Infrastructure is up. The app will be at: https://$(out host)"
  echo "Next: create the GitHub OAuth app (see deploy/azure.env.example) with callback"
  echo "  https://$(out host)/oauth2/callback"
  echo "then fill in deploy/azure.env and run: deploy/scripts/deploy.sh app"
}

app() {
  [ -n "$CI" ] || [ -f deploy/azure.env ] || die "copy deploy/azure.env.example to deploy/azure.env and fill it in"
  init
  local host acr tag pghost pgpass
  host=$(out host); acr=$(out acr_login_server); pghost=$(out postgres_host); pgpass=$(out postgres_password)
  # In CI the checkout is the commit (tools may touch files like the Terraform lock); locally,
  # uncommitted changes are marked so a dirty image is never mistaken for a commit.
  if [ -n "$CI" ]; then tag=${GITHUB_SHA:0:7}; else tag=$(git rev-parse --short HEAD)$(git diff --quiet || echo -dirty); fi
  eval "$(out get_credentials)"

  echo "== images ($tag) for linux/amd64"
  az acr login --name "$(out acr_name)" >/dev/null
  docker buildx build --platform linux/amd64 -f deploy/docker/Dockerfile.gaffer -t "$acr/gaffer:$tag" --push .
  docker buildx build --platform linux/amd64 -f deploy/docker/Dockerfile.web -t "$acr/gaffer-web:$tag" --push .

  echo "== platform: Traefik, cert-manager"
  # Platform charts are applied with --force-conflicts: AKS's admissions enforcer edits some of
  # their objects (cert-manager's webhook namespaceSelector) after install, and Helm 4's
  # server-side apply otherwise refuses every later upgrade over that field. AKS sets it again.
  helm repo add traefik https://traefik.github.io/charts >/dev/null
  helm repo add jetstack https://charts.jetstack.io >/dev/null
  helm repo add oauth2-proxy https://oauth2-proxy.github.io/manifests >/dev/null
  helm repo add temporal https://go.temporal.io/helm-charts >/dev/null
  helm repo add dex https://charts.dexidp.io >/dev/null
  helm repo update >/dev/null
  helm upgrade --install --force-conflicts traefik traefik/traefik --version 42.1.3 -n traefik --create-namespace \
    -f "$CHARTS/values-traefik.yaml" \
    --set "service.annotations.service\.beta\.kubernetes\.io/azure-load-balancer-ipv4=$(out ingress_ip)" \
    --set "service.annotations.service\.beta\.kubernetes\.io/azure-load-balancer-resource-group=$(az aks show -g "$(out resource_group)" -n "$(out cluster_name)" --query nodeResourceGroup -o tsv)" \
    --wait
  helm upgrade --install --force-conflicts cert-manager jetstack/cert-manager --version v1.21.3 -n cert-manager --create-namespace \
    --set crds.enabled=true --set resources.requests.cpu=10m --set resources.requests.memory=48Mi --wait

  echo "== secrets"
  kubectl create namespace "$NS" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  local dsn="postgres://gaffer:${pgpass}@${pghost}:5432/gaffer?sslmode=require&pool_max_conns=5"
  local args=(--from-literal=GAFFER_DSN="$dsn")
  # Only what the app needs from .env. FPL_AUTH_HEADER is left out: it expires within the hour.
  for k in ANTHROPIC_API_KEY ANTHROPIC_MODEL VOYAGE_API_KEY VOYAGE_MODEL GAFFER_TEAM_ID NTFY_TOPIC NTFY_SERVER; do
    v=$(envvar "$k" .env)
    [ -n "$v" ] && args+=(--from-literal="$k=$v")
  done
  # The key that encrypts saved settings: made once, then always the one already in the
  # cluster. A new key would make every saved team and token unreadable.
  local datakey
  datakey=$(kubectl -n "$NS" get secret gaffer-secrets -o jsonpath='{.data.GAFFER_DATA_KEY}' 2>/dev/null | base64 -d || true)
  [ -n "$datakey" ] || datakey=$(openssl rand -base64 32)
  args+=(--from-literal=GAFFER_DATA_KEY="$datakey")
  # Who is never limited (docs/sign-in.md#limits).
  local owners
  owners=$(envvar GAFFER_OWNERS deploy/azure.env)
  [ -n "$owners" ] && args+=(--from-literal=GAFFER_OWNERS="$owners")
  kubectl -n "$NS" create secret generic gaffer-secrets "${args[@]}" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  kubectl -n "$NS" create secret generic temporal-db --from-literal=password="$pgpass" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  # oauth2-proxy signs in through Dex as the client "oauth2-proxy". Its cookie secret and its
  # secret with Dex are made once and kept, so a redeploy does not sign everyone out.
  local cookie dexsecret
  cookie=$(kubectl -n "$NS" get secret oauth2-proxy-secrets -o jsonpath='{.data.cookie-secret}' 2>/dev/null | base64 -d || true)
  [ -n "$cookie" ] || cookie=$(openssl rand -base64 32 | head -c 32)
  dexsecret=$(kubectl -n "$NS" get secret oauth2-proxy-secrets -o jsonpath='{.data.dex-client-secret}' 2>/dev/null | base64 -d || true)
  [ -n "$dexsecret" ] || dexsecret=$(openssl rand -hex 32)
  kubectl -n "$NS" create secret generic oauth2-proxy-secrets \
    --from-literal=client-id=oauth2-proxy \
    --from-literal=client-secret="$dexsecret" \
    --from-literal=dex-client-secret="$dexsecret" \
    --from-literal=cookie-secret="$cookie" \
    --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  dex_config "$host" "$dexsecret" | kubectl -n "$NS" create secret generic dex-config --from-file=config.yaml=/dev/stdin \
    --dry-run=client -o yaml | kubectl apply -f - >/dev/null

  echo "== Temporal"
  helm upgrade --install --force-conflicts temporal temporal/temporal --version 1.7.0 -n "$NS" -f "$CHARTS/values-temporal.yaml" \
    --set "server.config.persistence.datastores.default.sql.connectAddr=$pghost:5432" \
    --set "server.config.persistence.datastores.visibility.sql.connectAddr=$pghost:5432" \
    --wait --timeout 15m

  echo "== sign-in: Dex, then oauth2-proxy"
  helm upgrade --install --force-conflicts dex dex/dex --version 0.26.0 -n "$NS" -f "$CHARTS/values-dex.yaml" \
    --set-string "podAnnotations.checksum/config=$(dex_config "$host" "$dexsecret" | sha256sum | cut -c1-16)" --wait
  local dexin="http://dex.$NS.svc.cluster.local:5556/dex"
  helm upgrade --install --force-conflicts oauth2-proxy oauth2-proxy/oauth2-proxy --version 10.7.1 -n "$NS" \
    -f "$CHARTS/values-oauth2-proxy.yaml" \
    --set "extraArgs.oidc-issuer-url=https://$host/dex" \
    --set "extraArgs.login-url=https://$host/dex/auth" \
    --set "extraArgs.redeem-url=$dexin/token" \
    --set "extraArgs.oidc-jwks-url=$dexin/keys" \
    --set "extraArgs.redirect-url=https://$host/oauth2/callback" \
    --set "extraArgs.whitelist-domain=$host\\,.$host" \
    --set "extraArgs.cookie-domain=.$host" \
    --wait

  echo "== app"
  helm upgrade --install gaffer "$CHARTS/gaffer" -n "$NS" \
    --set host="$host" --set image.registry="$acr" --set image.tag="$tag" \
    --set acmeEmail="$(envvar ACME_EMAIL deploy/azure.env)" \
    --wait --timeout 15m
  status
}

# dex_config HOST CLIENT_SECRET prints Dex's config: GitHub always, plus Google and Microsoft once
# their credentials are in deploy/azure.env (docs/sign-in.md). It holds secrets, so it only ever
# goes into the dex-config Secret.
dex_config() {
  local host=$1 secret=$2 gid gsecret mid msecret
  gid=$(envvar GOOGLE_CLIENT_ID deploy/azure.env)
  gsecret=$(envvar GOOGLE_CLIENT_SECRET deploy/azure.env)
  mid=$(envvar MICROSOFT_CLIENT_ID deploy/azure.env)
  msecret=$(envvar MICROSOFT_CLIENT_SECRET deploy/azure.env)
  cat <<EOF
issuer: https://$host/dex
storage:
  type: kubernetes
  config:
    inCluster: true
web:
  http: 0.0.0.0:5556
frontend:
  issuer: Let's Go Gaffer
  theme: dark
oauth2:
  skipApprovalScreen: true
enablePasswordDB: false
staticClients:
  - id: oauth2-proxy
    name: Let's Go Gaffer
    secret: $secret
    redirectURIs: ["https://$host/oauth2/callback"]
connectors:
  - type: github
    id: github
    name: GitHub
    config:
      clientID: $(envvar OAUTH_GITHUB_CLIENT_ID deploy/azure.env)
      clientSecret: $(envvar OAUTH_GITHUB_CLIENT_SECRET deploy/azure.env)
      redirectURI: https://$host/dex/callback
EOF
  if [ -n "$gid" ] && [ -n "$gsecret" ]; then
    cat <<EOF
  - type: google
    id: google
    name: Google
    config:
      clientID: $gid
      clientSecret: $gsecret
      redirectURI: https://$host/dex/callback
EOF
  fi
  if [ -n "$mid" ] && [ -n "$msecret" ]; then
    cat <<EOF
  - type: microsoft
    id: microsoft
    name: Microsoft
    config:
      clientID: $mid
      clientSecret: $msecret
      redirectURI: https://$host/dex/callback
      # Work, school and personal (Outlook, Hotmail) accounts alike.
      tenant: common
      emailToLowercase: true
EOF
  fi
}

status() {
  init
  eval "$(out get_credentials)" >/dev/null 2>&1
  kubectl -n "$NS" get pods,hpa
  echo
  echo "https://$(out host)   (Temporal UI: https://$(out host)/temporal)"
}

case "${1:-}" in
  infra) infra ;;
  app) app ;;
  status) status ;;
  *) die "usage: deploy/scripts/deploy.sh infra | app | status" ;;
esac
