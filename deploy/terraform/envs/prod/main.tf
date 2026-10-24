# The production deployment of canopyweb. See deploy/README.md for the first
# deployment and for releasing a new image.

terraform {
  required_version = ">= 1.10"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.0"
    }
    random = {
      source  = "hashicorp/random"
      version = "~> 3.6"
    }
  }

  # The state holds the generated keys and the database password: keep the
  # bucket private, encrypted, and versioned. Configured with
  # `terraform init -backend-config=backend.hcl` (see backend.hcl.example).
  backend "s3" {}
}

provider "aws" {
  region = var.region

  default_tags {
    tags = {
      Project     = "canopy"
      Environment = var.environment
      ManagedBy   = "terraform"
    }
  }
}

locals {
  name = "canopy-${var.environment}"
}

module "network" {
  source = "../../modules/network"

  name               = local.name
  single_nat_gateway = var.single_nat_gateway
}

module "secrets" {
  source = "../../modules/secrets"

  name = local.name
}

module "service" {
  source = "../../modules/service"

  name               = local.name
  vpc_id             = module.network.vpc_id
  public_subnet_ids  = module.network.public_subnet_ids
  private_subnet_ids = module.network.private_subnet_ids
  certificate_arn    = var.certificate_arn
  ingress_cidrs      = var.ingress_cidrs
  public_url         = var.public_url
  secret_arns        = module.secrets.env_secret_arns
  environment        = var.canopy_environment
  image_tag          = var.image_tag
  desired_count      = var.desired_count
  db_instance_class  = var.db_instance_class
  db_multi_az        = var.db_multi_az
}
