#!/usr/bin/env bash
# One-time setup so GitHub Actions can deploy. Run from the repository root, logged in to both
# `az` and `gh`. Safe to rerun: every step checks before it creates.
#
#   1. Terraform state: a storage account (Entra ID access only, no keys) in its own resource
#      group, and deploy/terraform/backend.hcl naming it.
#   2. Two identities that GitHub logs in as with OIDC (short-lived tokens, nothing stored):
#        gaffer-github-plan    pull requests; reads the subscription and the state
#        gaffer-github-deploy  only the `production` environment; Contributor, and allowed to
#                              grant exactly one role (AcrPull, which Terraform gives the cluster)
#   3. GitHub: the `production` environment (main branch only), its secrets copied from .env and
#      deploy/azure.env without printing them, and the repository variables.
set -euo pipefail

REPO=${REPO:-$(gh repo view --json nameWithOwner -q .nameWithOwner)}
LOCATION=${LOCATION:-northcentralus}
STATE_RG=gaffer-tfstate
SUB=$(az account show --query id -o tsv)
TENANT=$(az account show --query tenantId -o tsv)
ME=$(az ad signed-in-user show --query id -o tsv)
ACR_PULL=7f951dff-4eb8-4b57-a6de-6e2de0c55ca0 # built-in AcrPull role id

envvar() { grep -E "^$1=" "$2" 2>/dev/null | head -1 | cut -d= -f2- || true; }
role() { # role ASSIGNEE ROLE SCOPE [extra args]: assign unless already assigned
  local who=$1 name=$2 scope=$3; shift 3
  if [ -z "$(az role assignment list --assignee "$who" --role "$name" --scope "$scope" --query '[0].id' -o tsv)" ]; then
    az role assignment create --assignee-object-id "$who" --assignee-principal-type "${PTYPE:-ServicePrincipal}" \
      --role "$name" --scope "$scope" "$@" -o none
  fi
}

echo "== 1. Terraform state"
az group create -n "$STATE_RG" -l "$LOCATION" -o none
SA=$(az storage account list -g "$STATE_RG" --query '[0].name' -o tsv)
if [ -z "$SA" ]; then
  SA=gaffertf$(openssl rand -hex 4)
  az storage account create -n "$SA" -g "$STATE_RG" -l "$LOCATION" --sku Standard_LRS --kind StorageV2 \
    --min-tls-version TLS1_2 --allow-blob-public-access false --allow-shared-key-access false -o none
fi
SA_ID=$(az storage account show -n "$SA" -g "$STATE_RG" --query id -o tsv)
PTYPE=User role "$ME" "Storage Blob Data Contributor" "$SA_ID"
for i in $(seq 1 12); do # the role takes a minute to reach the storage service
  az storage container create -n tfstate --account-name "$SA" --auth-mode login -o none 2>/dev/null && break
  sleep 10
done
cat > deploy/terraform/backend.hcl <<EOF
resource_group_name  = "$STATE_RG"
storage_account_name = "$SA"
container_name       = "tfstate"
key                  = "gaffer.tfstate"
use_azuread_auth     = true
EOF

echo "== 2. identities for GitHub"
app() { # app NAME SUBJECT -> prints the app's client id
  local id
  id=$(az ad app list --display-name "$1" --query '[0].appId' -o tsv)
  [ -n "$id" ] || id=$(az ad app create --display-name "$1" --query appId -o tsv)
  az ad sp show --id "$id" -o none 2>/dev/null || az ad sp create --id "$id" -o none
  if [ -z "$(az ad app federated-credential list --id "$id" --query "[?subject=='$2'].name" -o tsv)" ]; then
    az ad app federated-credential create --id "$id" --parameters "{\"name\":\"github\",\"issuer\":\"https://token.actions.githubusercontent.com\",\"subject\":\"$2\",\"audiences\":[\"api://AzureADTokenExchange\"]}" -o none
  fi
  echo "$id"
}
PLAN_ID=$(app gaffer-github-plan "repo:$REPO:pull_request")
DEPLOY_ID=$(app gaffer-github-deploy "repo:$REPO:environment:production")
PLAN_SP=$(az ad sp show --id "$PLAN_ID" --query id -o tsv)
DEPLOY_SP=$(az ad sp show --id "$DEPLOY_ID" --query id -o tsv)

role "$PLAN_SP" Reader "/subscriptions/$SUB"
role "$PLAN_SP" "Azure Kubernetes Service Cluster User Role" "/subscriptions/$SUB"
role "$PLAN_SP" "Storage Blob Data Reader" "$SA_ID"

role "$DEPLOY_SP" Contributor "/subscriptions/$SUB"
role "$DEPLOY_SP" "Storage Blob Data Contributor" "$SA_ID"
# May create and delete role assignments, but only of the AcrPull role.
COND="((!(ActionMatches{'Microsoft.Authorization/roleAssignments/write'})) OR (@Request[Microsoft.Authorization/roleAssignments:RoleDefinitionId] ForAnyOfAnyValues:GuidEquals {$ACR_PULL})) AND ((!(ActionMatches{'Microsoft.Authorization/roleAssignments/delete'})) OR (@Resource[Microsoft.Authorization/roleAssignments:RoleDefinitionId] ForAnyOfAnyValues:GuidEquals {$ACR_PULL}))"
role "$DEPLOY_SP" "Role Based Access Control Administrator" "/subscriptions/$SUB" --condition "$COND" --condition-version 2.0

echo "== 3. GitHub"
GH_USER=$(gh api user -q .login)
GH_USER_ID=$(gh api user -q .id)
# Required reviewers need a paid plan on a private repository; ask for them, and fall back to a
# branch-only environment (deploys then start by hand, which is the approval).
if ! gh api -X PUT "repos/$REPO/environments/production" --input - >/dev/null 2>&1 <<EOF
{"reviewers":[{"type":"User","id":$GH_USER_ID}],"deployment_branch_policy":{"protected_branches":false,"custom_branch_policies":true}}
EOF
then
  gh api -X PUT "repos/$REPO/environments/production" --input - >/dev/null <<EOF
{"deployment_branch_policy":{"protected_branches":false,"custom_branch_policies":true}}
EOF
fi
gh api -X POST "repos/$REPO/environments/production/deployment-branch-policies" -f name=main -f type=branch >/dev/null 2>&1 || true

gh variable set AZURE_TENANT_ID -b "$TENANT"
gh variable set AZURE_SUBSCRIPTION_ID -b "$SUB"
gh variable set AZURE_PLAN_CLIENT_ID -b "$PLAN_ID"
gh variable set ALERT_EMAIL -b "$(envvar alert_email deploy/terraform/terraform.tfvars | tr -d '" ')"
gh variable set OAUTH_GITHUB_USERS -b "$(envvar OAUTH_GITHUB_USERS deploy/azure.env)"
model=$(envvar ANTHROPIC_MODEL .env); [ -z "$model" ] || gh variable set ANTHROPIC_MODEL -b "$model"

# Secrets go in on stdin, never on the command line or the screen.
setsecret() { [ -n "$2" ] && printf '%s' "$2" | gh secret set "$1" --env production; }
setsecret AZURE_CLIENT_ID "$DEPLOY_ID"
for k in ANTHROPIC_API_KEY VOYAGE_API_KEY GAFFER_TEAM_ID NTFY_TOPIC; do setsecret "$k" "$(envvar "$k" .env)" || true; done
for k in OAUTH_GITHUB_CLIENT_ID OAUTH_GITHUB_CLIENT_SECRET; do setsecret "$k" "$(envvar "$k" deploy/azure.env)" || true; done

echo
echo "Done. Deploy with Actions > Deploy > Run workflow (on main)."
echo "Commit deploy/terraform/backend.hcl, then move the state: terraform -chdir=deploy/terraform init -migrate-state -backend-config=backend.hcl"
