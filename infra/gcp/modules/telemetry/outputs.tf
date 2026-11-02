# Copyright (c) 2026 Mesh Intelligence Inc.
# SPDX-License-Identifier: MIT-0

output "bucket_url" {
  description = "Collector storage bucket URL; no endpoint override, so ambient identity resolves it."
  value       = "gs://${google_storage_bucket.telemetry.name}"
}

output "service_account" {
  description = "Email the namespace KSA is annotated with."
  value       = google_service_account.objectstore.email
}
