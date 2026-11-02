# Copyright (c) 2026 Mesh Intelligence Inc.
# SPDX-License-Identifier: MIT-0

output "ghcr_path" {
  description = "Image path prefix for ghcr.io images, e.g. <path>/mesh-intelligence/declarative-agents/agent-core:<tag>; empty until the token is stored."
  value       = var.ghcr_token_ready ? "${var.region}-docker.pkg.dev/${var.project}/${google_artifact_registry_repository.ghcr[0].repository_id}" : ""
}

output "dockerhub_path" {
  description = "Image path prefix for Docker Hub images, e.g. <path>/chromadb/chroma:<tag>."
  value       = "${var.region}-docker.pkg.dev/${var.project}/${google_artifact_registry_repository.dockerhub.repository_id}"
}
