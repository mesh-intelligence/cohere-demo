# Copyright (c) 2026 Mesh Intelligence Inc.
# SPDX-License-Identifier: MIT-0

# The production environment: the same modules as dev, kept up. The cluster
# refuses destroy, the telemetry bucket keeps 90 days of versioned objects,
# and the secrets survive terraform destroy. Unapplied until a production
# project exists; gcp:up refuses environment prod while it names the dev
# project.

locals {
  environment = "prod"
  application = "cohere-demo"
  namespace   = "app-cohere-demo"
  ksa         = "default"
  # Secret sync reads Secret Manager as its own KSA. The default KSA carries
  # the Workload Identity annotation for the telemetry bucket, and an
  # annotated KSA impersonates that Google service account, which reads no
  # secret.
  secret_sync_ksa = "secret-sync"

  # The environment's choices. Every other line of this file is identical
  # across environments; compare envs/dev/main.tf with envs/prod/main.tf.
  cluster_deletion_protection = true
  cluster_release_channel     = "REGULAR"
  telemetry_force_destroy     = false
  telemetry_versioning        = true
  telemetry_retention_days    = 90
  secrets_prevent_destroy     = true

  secrets = [
    "cohere-api-key",
    "serpapi-api-key",
    "iap-client-id",
    "iap-client-secret",
    "ghcr-pull",
  ]
}

module "cluster" {
  source              = "../../modules/cluster"
  name                = var.cluster
  region              = var.region
  deletion_protection = local.cluster_deletion_protection
  release_channel     = local.cluster_release_channel
}

module "secrets" {
  source          = "../../modules/secrets"
  project         = var.project
  application     = local.application
  namespace       = local.namespace
  ksa             = local.secret_sync_ksa
  secrets         = local.secrets
  not_for_pods    = ["ghcr-pull"]
  prevent_destroy = local.secrets_prevent_destroy
}

module "registry" {
  source               = "../../modules/registry"
  project              = var.project
  region               = var.region
  name                 = local.application
  ghcr_username        = var.ghcr_username
  ghcr_token_secret_id = module.secrets.ids["ghcr-pull"]
  ghcr_token_ready     = var.ghcr_token_ready
  node_service_account = var.node_service_account
}

module "telemetry" {
  source         = "../../modules/telemetry"
  project        = var.project
  region         = var.region
  application    = local.application
  namespace      = local.namespace
  ksa            = local.ksa
  force_destroy  = local.telemetry_force_destroy
  versioning     = local.telemetry_versioning
  retention_days = local.telemetry_retention_days
}
