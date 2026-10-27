#!/usr/bin/env bash
# Starts a stopped cluster. Pods come back on their own; give it a few minutes.
set -euo pipefail
TF=deploy/terraform
az aks start -g "$(terraform -chdir=$TF output -raw resource_group)" -n "$(terraform -chdir=$TF output -raw cluster_name)"
deploy/scripts/deploy.sh status
