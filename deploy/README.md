# Deploying canopyweb

This directory deploys the review server (`canopyweb`, see [docs/server.md](../docs/server.md)) to AWS with Terraform, and runs it locally against LocalStack.

```
Dockerfile                 the canopyweb image (repository root)
deploy/
  local/                   docker compose: canopyweb, PostgreSQL, LocalStack
  terraform/
    modules/network        VPC, public and private subnets, NAT
    modules/secrets        Secrets Manager: GitHub App, database_key, link_key
    modules/service        ECR, ECS Fargate, ALB (HTTPS), RDS PostgreSQL, IAM, logs
    envs/local             the secrets module, applied to LocalStack
    envs/prod              all three modules, applied to AWS
```

In AWS, the load balancer takes the GitHub App's webhooks and serves the review pages over HTTPS. The tasks run in private subnets and reach GitHub through NAT. Each task gets its secrets from Secrets Manager as `CANOPY_*` environment variables ([docs/configuration.md](../docs/configuration.md)). Terraform generates `database_key`, `link_key` and the database password. The GitHub App's credentials are set by hand so they stay out of the Terraform state.

## Local environment

Needs Docker, Terraform 1.10 or newer, and OpenSSL. Recent LocalStack images need an auth token: export `LOCALSTACK_AUTH_TOKEN` (free accounts have one).

```sh
make local-up     # http://localhost:8080/swagger
make local-down   # stops everything and deletes its data
```

`local-up` starts LocalStack and PostgreSQL and applies `envs/local` to LocalStack. It then reads the secrets back from Secrets Manager and starts canopyweb with them. By default it configures a throw-away GitHub App, so webhooks signed with `local-webhook-secret` are verified, but reviews can't call GitHub. To review real pull requests, use a development GitHub App and forward its webhooks to `http://localhost:8080/webhooks/github` with a tunnel such as smee.io or ngrok:

```sh
export TF_VAR_github_app_id=123456
export TF_VAR_github_private_key="$(cat dev-app.pem)"
export TF_VAR_github_webhook_secret=...
make local-up
```

The free LocalStack does not emulate ECS, RDS, ELB, or ECR, so compose stands in for them locally and `make tf-check` validates the production configuration.

## First deployment

Prerequisites: an S3 bucket for the state (private, versioned, encrypted), a domain, an ACM certificate for it in the deployment's region, and a GitHub App with the permissions and events listed in [docs/server.md](../docs/server.md).

1. Configure and create the infrastructure with no tasks running:

   ```sh
   cd deploy/terraform/envs/prod
   cp backend.hcl.example backend.hcl            # fill in
   cp terraform.tfvars.example terraform.tfvars  # fill in; desired_count = 0
   terraform init -backend-config=backend.hcl
   terraform apply
   ```

2. Set the GitHub App's secrets (names in the `github_secret_names` output):

   ```sh
   aws secretsmanager put-secret-value --secret-id canopy-prod/github/app_id --secret-string 123456
   aws secretsmanager put-secret-value --secret-id canopy-prod/github/private_key --secret-string file://app.pem
   aws secretsmanager put-secret-value --secret-id canopy-prod/github/webhook_secret --secret-string '...'
   ```

3. Build and push the image (the tasks default to ARM64):

   ```sh
   repo=$(terraform output -raw repository_url)
   aws ecr get-login-password | docker login --username AWS --password-stdin "${repo%%/*}"
   make -C ../../../.. image IMAGE="$repo:v0.1.0"
   docker push "$repo:v0.1.0"
   ```

4. Set `image_tag = "v0.1.0"` and `desired_count = 2` in `terraform.tfvars`, then `terraform apply`.

5. Point the domain at the `load_balancer_dns_name` output (CNAME or Route 53 alias), and set the GitHub App's webhook URL to `https://<domain>/webhooks/github`.

## Releasing

Build and push a new tag (ECR tags are immutable), set `image_tag`, and run `terraform apply`. ECS replaces the tasks one by one and rolls back a deployment whose tasks don't become healthy.

## Notes

- The Terraform state contains the generated keys and the database password. Restrict access to the state bucket.
- To change a GitHub App secret, put a new value and force a new deployment (`aws ecs update-service --cluster canopy-prod --service canopy-prod --force-new-deployment`). Tasks read secrets only when they start.
- While the project is in development, a schema change means recreating the database (see docs/server.md).
- Each task opens `review_workers` + 8 database connections. Size `db_instance_class` for tasks × that.
- `ingress_cidrs` can restrict the load balancer to GitHub's hook ranges (`https://api.github.com/meta`) plus your users' ranges, who open the review pages.
