#!/usr/bin/env bash
# Deletes everything Terraform made: the cluster, the registry and the database with its data.
set -euo pipefail
read -r -p "This deletes the cluster, registry and database for good. Type 'destroy' to go on: " ok
[ "$ok" = destroy ] || { echo "Nothing deleted."; exit 1; }
terraform -chdir=deploy/terraform destroy
