#!/usr/bin/env bash
# Pull request previews: each PR gets its own namespace (pr-N), database (gaffer_pr_N) and address
# (https://pr-N.<production host>) on the production cluster, behind the same GitHub login.
#
#   deploy/scripts/preview.sh up N SHA   # build the PR's images, then create or update its preview
#   deploy/scripts/preview.sh down N     # remove the namespace and drop the database
#   deploy/scripts/preview.sh sweep      # remove previews whose PR is no longer open (needs gh)
#
# A new preview's database is a copy of production's, without anyone's saved settings, and the
# preview gets its own encryption key, so no production token is ever readable there. Previews
# run the web app and the API only: no Temporal, no scheduled news pulls.
#
# Reads PREVIEW_DOMAIN (production's host), ACR_NAME, AKS_RESOURCE_GROUP and AKS_NAME from the
# environment (GitHub repository variables), so it needs no Terraform state.
set -euo pipefail
# Paths below are from the repository root: run from there, whatever folder this was started in.
cd "$(dirname "$0")/../.."

PROD_NS=gaffer
CHART=deploy/helm/gaffer
MAX_PREVIEWS=${MAX_PREVIEWS:-3}

die() { echo "preview: $*" >&2; exit 1; }
need() { [ -n "${!1:-}" ] || die "$1 is not set"; }

creds() {
  need AKS_RESOURCE_GROUP; need AKS_NAME
  az aks get-credentials -g "$AKS_RESOURCE_GROUP" -n "$AKS_NAME" --overwrite-existing -o none
}

# psql_job NAME SCRIPT: runs a shell SCRIPT (psql, pg_dump) as the database admin in a pod in
# production's namespace, where the admin password is (the temporal-db Secret). Nothing leaves the
# cluster. The script travels base64-encoded, so it needs no quoting.
psql_job() {
  local name=$1 script=$2 host phase
  host=$(kubectl -n "$PROD_NS" get secret gaffer-secrets -o jsonpath='{.data.GAFFER_DSN}' | base64 -d | sed -E 's|.*@([^:/]+).*|\1|')
  kubectl -n "$PROD_NS" delete pod "$name" --ignore-not-found >/dev/null
  kubectl -n "$PROD_NS" apply -f - >/dev/null <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: $name
  labels: { gaffer/job: preview-db }
spec:
  restartPolicy: Never
  containers:
    - name: psql
      image: postgres:16.15-alpine3.24
      command: ["sh", "-c", "echo \"\$SCRIPT\" | base64 -d | sh -e -o pipefail"]
      env:
        - { name: SCRIPT, value: "$(printf '%s' "$script" | base64 | tr -d '\n')" }
        - { name: PGHOST, value: "$host" }
        - { name: PGUSER, value: gaffer }
        - { name: PGSSLMODE, value: require }
        - name: PGPASSWORD
          valueFrom: { secretKeyRef: { name: temporal-db, key: password } }
EOF
  for _ in $(seq 1 120); do
    phase=$(kubectl -n "$PROD_NS" get pod "$name" -o jsonpath='{.status.phase}' 2>/dev/null || true)
    [ "$phase" = Succeeded ] || [ "$phase" = Failed ] && break
    sleep 5
  done
  if [ "$phase" != Succeeded ]; then
    kubectl -n "$PROD_NS" logs "$name" >&2 || true
    kubectl -n "$PROD_NS" delete pod "$name" --ignore-not-found >/dev/null
    die "$name did not succeed ($phase)"
  fi
  kubectl -n "$PROD_NS" delete pod "$name" --ignore-not-found >/dev/null
}

up() {
  local n=$1 sha=$2
  [[ "$n" =~ ^[0-9]+$ ]] || die "PR number must be a number"
  need PREVIEW_DOMAIN; need ACR_NAME
  local ns=pr-$n db=gaffer_pr_$n host=pr-$n.$PREVIEW_DOMAIN tag=pr-$n-${sha:0:7}
  local acr="$ACR_NAME.azurecr.io"
  creds

  if ! kubectl get namespace "$ns" >/dev/null 2>&1; then
    local count
    count=$(kubectl get namespaces -o name | grep -c '^namespace/pr-' || true)
    [ "$count" -lt "$MAX_PREVIEWS" ] || die "already $count previews (the most is $MAX_PREVIEWS): close a PR or raise MAX_PREVIEWS"
  fi

  echo "== images ($tag)"
  az acr login --name "$ACR_NAME" >/dev/null
  docker buildx build --platform linux/amd64 -f deploy/docker/Dockerfile.gaffer -t "$acr/gaffer:$tag" --push .
  docker buildx build --platform linux/amd64 -f deploy/docker/Dockerfile.web -t "$acr/gaffer-web:$tag" --push .

  if ! kubectl get namespace "$ns" >/dev/null 2>&1; then
    echo "== database $db: a copy of production, without saved settings"
    psql_job "seed-$ns" "
psql -d postgres -v ON_ERROR_STOP=1 -c 'DROP DATABASE IF EXISTS $db WITH (FORCE)' -c 'CREATE DATABASE $db'
pg_dump -Fc -d gaffer | pg_restore --no-owner --exit-on-error -d $db
for t in user_settings alerts watchlist; do
  # Empty whichever exist: production may not have run a newer migration yet.
  if [ \"\$(psql -d $db -tAc \"select to_regclass('\$t')\")\" = \"\$t\" ]; then psql -d $db -v ON_ERROR_STOP=1 -c \"TRUNCATE \$t\"; fi
done
"
    kubectl create namespace "$ns" >/dev/null
    kubectl label namespace "$ns" gaffer/preview=true gaffer/pr="$n" >/dev/null
  fi

  echo "== secrets"
  # The API keys come from production's Secret; the database and the encryption key are the preview's own.
  local prod_dsn dsn key args=()
  prod_dsn=$(kubectl -n "$PROD_NS" get secret gaffer-secrets -o jsonpath='{.data.GAFFER_DSN}' | base64 -d)
  dsn=$(printf '%s' "$prod_dsn" | sed -E "s|/gaffer\?|/$db?|; s|pool_max_conns=[0-9]+|pool_max_conns=3|")
  key=$(kubectl -n "$ns" get secret gaffer-secrets -o jsonpath='{.data.GAFFER_DATA_KEY}' 2>/dev/null | base64 -d || true)
  [ -n "$key" ] || key=$(openssl rand -base64 32)
  for k in ANTHROPIC_API_KEY ANTHROPIC_MODEL VOYAGE_API_KEY VOYAGE_MODEL GAFFER_TEAM_ID; do
    v=$(kubectl -n "$PROD_NS" get secret gaffer-secrets -o jsonpath="{.data.$k}" 2>/dev/null | base64 -d || true)
    [ -n "$v" ] && args+=(--from-literal="$k=$v")
  done
  kubectl -n "$ns" create secret generic gaffer-secrets --from-literal=GAFFER_DSN="$dsn" --from-literal=GAFFER_DATA_KEY="$key" "${args[@]}" \
    --dry-run=client -o yaml | kubectl apply -f - >/dev/null

  # Traces and logs go to production's Datadog Agent, filed under env pr-N, when it is installed.
  local otlp=""
  kubectl get namespace datadog >/dev/null 2>&1 && otlp=http://datadog.datadog.svc.cluster.local:4318

  echo "== app at https://$host"
  helm upgrade --install "gaffer-pr-$n" "$CHART" -n "$ns" \
    --set preview=true --set host="$host" --set image.registry="$acr" --set image.tag="$tag" \
    --set telemetry.otlpEndpoint="$otlp" \
    --set env.TEMPORAL_ADDRESS="" --set env.GAFFER_NEWS_REFRESH=off \
    --set api.autoscaling.max=2 --set web.autoscaling.max=2 \
    --wait --timeout 10m
  echo "https://$host" > "${PREVIEW_URL_FILE:-/dev/null}"
  echo "Preview ready: https://$host"
}

down() {
  local n=$1
  [[ "$n" =~ ^[0-9]+$ ]] || die "PR number must be a number"
  creds
  helm uninstall "gaffer-pr-$n" -n "pr-$n" --wait >/dev/null 2>&1 || true
  kubectl delete namespace "pr-$n" --ignore-not-found --wait=true >/dev/null
  psql_job "drop-pr-$n" "psql -d postgres -v ON_ERROR_STOP=1 -c 'DROP DATABASE IF EXISTS gaffer_pr_$n WITH (FORCE)'"
  echo "Preview pr-$n removed."
}

sweep() {
  creds
  for ns in $(kubectl get namespaces -l gaffer/preview=true -o jsonpath='{.items[*].metadata.name}'); do
    n=${ns#pr-}
    state=$(gh pr view "$n" --json state -q .state 2>/dev/null || echo UNKNOWN)
    if [ "$state" != OPEN ]; then
      echo "PR #$n is $state: removing its preview"
      down "$n"
    fi
  done
}

case "${1:-}" in
  up) [ $# -eq 3 ] || die "usage: preview.sh up N SHA"; up "$2" "$3" ;;
  down) [ $# -eq 2 ] || die "usage: preview.sh down N"; down "$2" ;;
  sweep) sweep ;;
  *) die "usage: deploy/scripts/preview.sh up N SHA | down N | sweep" ;;
esac
