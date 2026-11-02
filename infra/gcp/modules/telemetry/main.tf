# Copyright (c) 2026 Mesh Intelligence Inc.
# SPDX-License-Identifier: MIT-0

# Where the collector writes traces and metrics: one bucket, one Google
# service account with objectAdmin on that bucket alone, and the Workload
# Identity binding that lets the application namespace's default KSA act as
# it. Pods reach the bucket with no stored key.

terraform {
  required_providers {
    google = {
      source = "hashicorp/google"
    }
    time = {
      source = "hashicorp/time"
    }
  }
}

resource "google_project_service" "required" {
  for_each = toset([
    "storage.googleapis.com",
    "iam.googleapis.com",
  ])
  service            = each.value
  disable_on_destroy = false
}

resource "google_storage_bucket" "telemetry" {
  name                        = "${var.project}-${var.application}-telemetry"
  location                    = var.region
  uniform_bucket_level_access = true
  public_access_prevention    = "enforced"
  force_destroy               = var.force_destroy

  versioning {
    enabled = var.versioning
  }

  dynamic "lifecycle_rule" {
    for_each = var.retention_days > 0 ? [var.retention_days] : []
    content {
      condition {
        age = lifecycle_rule.value
      }
      action {
        type = "Delete"
      }
    }
  }

  depends_on = [google_project_service.required]
}

resource "google_service_account" "objectstore" {
  account_id   = "${var.application}-objectstore"
  display_name = "${var.application} telemetry objectstore"
  depends_on   = [google_project_service.required]
}

# IAM answers a service account's create before the account propagates, and
# a binding made a second later is rejected as naming an unknown member. The
# wait runs only when the account is created.
resource "time_sleep" "service_account_propagation" {
  create_duration = "30s"
  triggers = {
    account = google_service_account.objectstore.email
  }
}

resource "google_storage_bucket_iam_member" "object_admin" {
  bucket = google_storage_bucket.telemetry.name
  role   = "roles/storage.objectAdmin"
  member = "serviceAccount:${time_sleep.service_account_propagation.triggers.account}"
}

resource "google_service_account_iam_member" "workload_identity" {
  service_account_id = google_service_account.objectstore.name
  role               = "roles/iam.workloadIdentityUser"
  member             = "serviceAccount:${var.project}.svc.id.goog[${var.namespace}/${var.ksa}]"
  depends_on         = [time_sleep.service_account_propagation]
}
