# Copyright (c) 2026 Mesh Intelligence Inc.
# SPDX-License-Identifier: MIT-0

# One GKE Autopilot cluster for one environment, with the APIs the rest of the
# environment needs. Autopilot runs Workload Identity Federation by default,
# which the telemetry and secrets modules bind against.

terraform {
  required_providers {
    google = {
      source = "hashicorp/google"
    }
  }
}

resource "google_project_service" "required" {
  for_each = toset([
    "container.googleapis.com",
    "iap.googleapis.com",
    # gcloud iap web add-iam-policy-binding resolves the project through it.
    "cloudresourcemanager.googleapis.com",
  ])
  service            = each.value
  disable_on_destroy = false
}

resource "google_container_cluster" "this" {
  name                = var.name
  location            = var.region
  enable_autopilot    = true
  deletion_protection = var.deletion_protection

  release_channel {
    channel = var.release_channel
  }

  # The chatbot is exposed through a Gateway (modules/exposure), whose
  # HTTPRoute strips the Google front end and IAP headers the agent's REST
  # server would refuse; classic Ingress cannot rewrite request headers.
  gateway_api_config {
    channel = "CHANNEL_STANDARD"
  }

  # Secret Manager reaches pods two ways: the CSI add-on mounts a secret as a
  # file, and secret sync copies it into a Kubernetes Secret the chart reads
  # through secretKeyRef, refreshed at the rotation interval without a
  # redeploy. The values stay in Secret Manager.
  secret_manager_config {
    enabled = true
  }
  secret_sync_config {
    enabled = true
    rotation_config {
      enabled = true
    }
  }

  depends_on = [google_project_service.required]
}
