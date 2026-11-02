# Copyright (c) 2026 Mesh Intelligence Inc.
# SPDX-License-Identifier: MIT-0

# Everything the gcp:* verbs read back after apply.

output "environment" {
  value = local.environment
}

output "cluster_name" {
  value = module.cluster.name
}

output "cluster_location" {
  value = module.cluster.location
}

output "ghcr_path" {
  value = module.registry.ghcr_path
}

output "dockerhub_path" {
  value = module.registry.dockerhub_path
}

output "telemetry_bucket_url" {
  value = module.telemetry.bucket_url
}

output "objectstore_service_account" {
  value = module.telemetry.service_account
}

output "secret_ids" {
  value = module.secrets.ids
}
