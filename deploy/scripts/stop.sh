#!/usr/bin/env bash
# Stops the cluster's nodes to save credit. The load balancer, IPs, disks and the (free) database
# keep existing, so start.sh brings everything back at the same address.
set -euo pipefail
TF=deploy/terraform
az aks stop -g "$(terraform -chdir=$TF output -raw resource_group)" -n "$(terraform -chdir=$TF output -raw cluster_name)"
echo "Stopped. Start again with deploy/scripts/start.sh"
