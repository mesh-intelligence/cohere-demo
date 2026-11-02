# Copyright (c) 2026 Mesh Intelligence Inc.
# SPDX-License-Identifier: MIT-0

# Two Artifact Registry remote repositories the nodes pull every image
# through, so nothing is built or uploaded at deploy time and nodes reach
# neither ghcr.io nor Docker Hub directly (eng08). The ghcr.io repository
# authenticates with a GitHub read:packages token that Artifact Registry reads
# from Secret Manager; the cluster never holds it. Docker Hub is pulled
# anonymously and cached, which also keeps the nodes clear of its rate limit.

terraform {
  required_providers {
    google = {
      source = "hashicorp/google"
    }
  }
}

resource "google_project_service" "artifact_registry" {
  service            = "artifactregistry.googleapis.com"
  disable_on_destroy = false
}

data "google_project" "this" {
  project_id = var.project
}

locals {
  # Empty means the project's default compute account, which Autopilot nodes
  # pull as.
  node_service_account    = var.node_service_account != "" ? var.node_service_account : "${data.google_project.this.number}-compute@developer.gserviceaccount.com"
  artifact_registry_agent = "service-${data.google_project.this.number}@gcp-sa-artifactregistry.iam.gserviceaccount.com"
}

# Artifact Registry, not a pod, presents the token upstream, so its service
# agent is the one reader of the token secret.
resource "google_secret_manager_secret_iam_member" "ghcr_token_reader" {
  secret_id  = var.ghcr_token_secret_id
  role       = "roles/secretmanager.secretAccessor"
  member     = "serviceAccount:${local.artifact_registry_agent}"
  depends_on = [google_project_service.artifact_registry]
}

# Artifact Registry reads the token version when it creates the repository,
# whatever disable_upstream_validation says (found on the first live gcp:up),
# so the repository exists only once gcp:secrets has stored the token:
# gcp:up passes ghcr_token_ready from a version check on the secret.
resource "google_artifact_registry_repository" "ghcr" {
  count         = var.ghcr_token_ready ? 1 : 0
  repository_id = "${var.name}-ghcr"
  location      = var.region
  format        = "DOCKER"
  mode          = "REMOTE_REPOSITORY"
  description   = "Pull-through cache of ghcr.io for ${var.name}"

  remote_repository_config {
    description = "ghcr.io"
    docker_repository {
      custom_repository {
        uri = "https://ghcr.io"
      }
    }
    upstream_credentials {
      username_password_credentials {
        username                = var.ghcr_username
        password_secret_version = "${var.ghcr_token_secret_id}/versions/latest"
      }
    }
  }

  depends_on = [google_secret_manager_secret_iam_member.ghcr_token_reader]
}

resource "google_artifact_registry_repository" "dockerhub" {
  repository_id = "${var.name}-dockerhub"
  location      = var.region
  format        = "DOCKER"
  mode          = "REMOTE_REPOSITORY"
  description   = "Pull-through cache of Docker Hub for ${var.name}"

  remote_repository_config {
    description = "Docker Hub"
    docker_repository {
      public_repository = "DOCKER_HUB"
    }
  }

  depends_on = [google_project_service.artifact_registry]
}

# Creating a repository is not granting it: nodes without read pull nothing
# and every pod sits in ImagePullBackOff behind a 403.
resource "google_artifact_registry_repository_iam_member" "node_read" {
  for_each = merge(
    { dockerhub = google_artifact_registry_repository.dockerhub.name },
    { for repository in google_artifact_registry_repository.ghcr : "ghcr" => repository.name },
  )
  repository = each.value
  location   = var.region
  role       = "roles/artifactregistry.reader"
  member     = "serviceAccount:${local.node_service_account}"
}
