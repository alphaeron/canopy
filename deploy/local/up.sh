#!/bin/sh
# up.sh — starts the local environment (compose.yaml). It starts LocalStack
# and PostgreSQL, applies deploy/terraform/envs/local to LocalStack, reads
# the secrets back from Secrets Manager as ECS would inject them, and starts
# canopyweb with them at http://localhost:8080.
#
# Without TF_VAR_github_app_id, TF_VAR_github_private_key, and
# TF_VAR_github_webhook_secret, it configures a throw-away GitHub App: the
# server runs and verifies webhooks signed with the secret
# "local-webhook-secret", but cannot call GitHub.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
tf="$here/../terraform/envs/local"
data="$here/.data"
compose() { docker compose -f "$here/compose.yaml" "$@"; }

compose up -d --wait localstack postgres

if [ -z "${TF_VAR_github_private_key:-}" ]; then
	mkdir -p "$data"
	[ -f "$data/github-app.pem" ] || openssl genrsa -out "$data/github-app.pem" 2048 2>/dev/null
	TF_VAR_github_private_key=$(cat "$data/github-app.pem")
	TF_VAR_github_app_id=${TF_VAR_github_app_id:-1}
	TF_VAR_github_webhook_secret=${TF_VAR_github_webhook_secret:-local-webhook-secret}
	export TF_VAR_github_private_key TF_VAR_github_app_id TF_VAR_github_webhook_secret
fi

terraform -chdir="$tf" init -input=false >/dev/null
terraform -chdir="$tf" apply -input=false -auto-approve

for pair in $(terraform -chdir="$tf" output -raw secret_env_pairs); do
	env=${pair%%=*}
	value=$(compose run --rm -T awscli secretsmanager get-secret-value \
		--secret-id "${pair#*=}" --query SecretString --output text)
	export "$env=$value"
done

compose up -d --build --wait canopyweb
echo "canopyweb is running at http://localhost:8080 (logs: docker compose -f $here/compose.yaml logs -f canopyweb)"
