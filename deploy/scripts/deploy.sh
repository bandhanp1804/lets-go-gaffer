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

TF=deploy/terraform
NS=gaffer
CHARTS=deploy/helm

tf() { terraform -chdir="$TF" "$@"; }
out() { tf output -raw "$1"; }
die() { echo "deploy: $*" >&2; exit 1; }

# envvar KEY FILE: the value of KEY in FILE, without echoing it.
envvar() { grep -E "^$1=" "$2" 2>/dev/null | head -1 | cut -d= -f2- || true; }

infra() {
  az account show >/dev/null 2>&1 || die "run az login first"
  [ -f "$TF/terraform.tfvars" ] || die "copy $TF/terraform.tfvars.example to terraform.tfvars and fill it in"
  tf init -input=false
  tf apply -input=false
  eval "$(out get_credentials)"
  echo
  echo "Infrastructure is up. The app will be at: https://$(out host)"
  echo "Next: create the GitHub OAuth app (see deploy/azure.env.example) with callback"
  echo "  https://$(out host)/oauth2/callback"
  echo "then fill in deploy/azure.env and run: deploy/scripts/deploy.sh app"
}

app() {
  [ -f deploy/azure.env ] || die "copy deploy/azure.env.example to deploy/azure.env and fill it in"
  local host acr tag pghost pgpass
  host=$(out host); acr=$(out acr_login_server); pghost=$(out postgres_host); pgpass=$(out postgres_password)
  tag=$(git rev-parse --short HEAD)$(git diff --quiet || echo -dirty)
  eval "$(out get_credentials)"

  echo "== images ($tag) for linux/amd64"
  az acr login --name "$(out acr_name)" >/dev/null
  docker buildx build --platform linux/amd64 -f deploy/docker/Dockerfile.gaffer -t "$acr/gaffer:$tag" --push .
  docker buildx build --platform linux/amd64 -f deploy/docker/Dockerfile.web -t "$acr/gaffer-web:$tag" --push .

  echo "== platform: Traefik, cert-manager"
  helm repo add traefik https://traefik.github.io/charts >/dev/null
  helm repo add jetstack https://charts.jetstack.io >/dev/null
  helm repo add oauth2-proxy https://oauth2-proxy.github.io/manifests >/dev/null
  helm repo add temporal https://go.temporal.io/helm-charts >/dev/null
  helm repo update >/dev/null
  helm upgrade --install traefik traefik/traefik --version 42.1.3 -n traefik --create-namespace \
    -f "$CHARTS/values-traefik.yaml" \
    --set "service.annotations.service\.beta\.kubernetes\.io/azure-load-balancer-ipv4=$(out ingress_ip)" \
    --set "service.annotations.service\.beta\.kubernetes\.io/azure-load-balancer-resource-group=$(az aks show -g "$(out resource_group)" -n "$(out cluster_name)" --query nodeResourceGroup -o tsv)" \
    --wait
  helm upgrade --install cert-manager jetstack/cert-manager --version v1.21.3 -n cert-manager --create-namespace \
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
  kubectl -n "$NS" create secret generic gaffer-secrets "${args[@]}" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  kubectl -n "$NS" create secret generic temporal-db --from-literal=password="$pgpass" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  local cookie
  cookie=$(kubectl -n "$NS" get secret oauth2-proxy-secrets -o jsonpath='{.data.cookie-secret}' 2>/dev/null | base64 -d || true)
  [ -n "$cookie" ] || cookie=$(openssl rand -base64 32 | head -c 32)
  kubectl -n "$NS" create secret generic oauth2-proxy-secrets \
    --from-literal=client-id="$(envvar GITHUB_CLIENT_ID deploy/azure.env)" \
    --from-literal=client-secret="$(envvar GITHUB_CLIENT_SECRET deploy/azure.env)" \
    --from-literal=cookie-secret="$cookie" \
    --dry-run=client -o yaml | kubectl apply -f - >/dev/null

  echo "== Temporal"
  helm upgrade --install temporal temporal/temporal --version 1.7.0 -n "$NS" -f "$CHARTS/values-temporal.yaml" \
    --set "server.config.persistence.datastores.default.sql.connectAddr=$pghost:5432" \
    --set "server.config.persistence.datastores.visibility.sql.connectAddr=$pghost:5432" \
    --wait --timeout 15m

  echo "== GitHub login"
  helm upgrade --install oauth2-proxy oauth2-proxy/oauth2-proxy --version 10.7.1 -n "$NS" \
    -f "$CHARTS/values-oauth2-proxy.yaml" \
    --set "extraArgs.github-user=$(envvar GITHUB_USERS deploy/azure.env)" \
    --set "extraArgs.redirect-url=https://$host/oauth2/callback" \
    --set "extraArgs.whitelist-domain=$host" \
    --wait

  echo "== app"
  helm upgrade --install gaffer "$CHARTS/gaffer" -n "$NS" \
    --set host="$host" --set image.registry="$acr" --set image.tag="$tag" \
    --set acmeEmail="$(envvar ACME_EMAIL deploy/azure.env)" \
    --wait --timeout 15m
  status
}

status() {
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
