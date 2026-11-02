# Copyright (c) 2026 Mesh Intelligence Inc.
# SPDX-License-Identifier: MIT-0

# Secret Manager secrets for one application: Terraform owns each secret and
# who may read it, never its value. State holds managed values in plain text,
# so no secret version resource belongs here; the operator's gcp:secrets verb
# adds each value over stdin.
#
# prevent_destroy must be a literal, so the module carries two blocks of the
# same resource and var.prevent_destroy decides which one holds the secrets.
# Flipping it on an existing environment moves every secret between the two
# addresses, which is a state move (terraform state mv), not a recreate.

terraform {
  required_providers {
    google = {
      source = "hashicorp/google"
    }
  }
}

resource "google_project_service" "secret_manager" {
  service            = "secretmanager.googleapis.com"
  disable_on_destroy = false
}

data "google_project" "this" {
  project_id = var.project
}

locals {
  ids = { for name in var.secrets : name => "${var.application}-${name}" }

  # GKE secret sync reads as the KSA itself, so the grant names the KSA's
  # Workload Identity principal rather than a Google service account.
  ksa_principal = "principal://iam.googleapis.com/projects/${data.google_project.this.number}/locations/global/workloadIdentityPools/${var.project}.svc.id.goog/subject/ns/${var.namespace}/sa/${var.ksa}"
}

resource "google_secret_manager_secret" "disposable" {
  for_each  = var.prevent_destroy ? {} : local.ids
  secret_id = each.value

  replication {
    auto {}
  }

  depends_on = [google_project_service.secret_manager]
}

resource "google_secret_manager_secret" "protected" {
  for_each  = var.prevent_destroy ? local.ids : {}
  secret_id = each.value

  replication {
    auto {}
  }

  lifecycle {
    prevent_destroy = true
  }

  depends_on = [google_project_service.secret_manager]
}

locals {
  secret_ids = merge(
    { for name, secret in google_secret_manager_secret.disposable : name => secret.id },
    { for name, secret in google_secret_manager_secret.protected : name => secret.id },
  )
}

resource "google_secret_manager_secret_iam_member" "pod_reader" {
  for_each  = { for name, id in local.secret_ids : name => id if !contains(var.not_for_pods, name) }
  secret_id = each.value
  role      = "roles/secretmanager.secretAccessor"
  member    = local.ksa_principal
}
