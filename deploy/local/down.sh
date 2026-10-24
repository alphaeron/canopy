#!/bin/sh
# down.sh — stops the local environment and deletes its data: the database,
# LocalStack's resources, and the Terraform state that described them.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
docker compose -f "$here/compose.yaml" --profile tools down -v --remove-orphans
rm -f "$here/../terraform/envs/local/terraform.tfstate" "$here/../terraform/envs/local/terraform.tfstate.backup"
