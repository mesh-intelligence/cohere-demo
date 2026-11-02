# Copyright (c) 2026 Mesh Intelligence Inc.
# SPDX-License-Identifier: MIT-0

output "bucket" {
  description = "State bucket name; each environment root's gcs backend takes it with prefix cohere-demo/<environment>."
  value       = google_storage_bucket.tfstate.name
}

output "ingress_address_name" {
  description = "Name of the chatbot's reserved global address; the exposure root's Gateway binds it."
  value       = google_compute_global_address.ingress.name
}

output "ingress_address" {
  description = "The reserved address itself; the default host is its sslip.io name."
  value       = google_compute_global_address.ingress.address
}

output "corpus_bucket" {
  description = "Private corpus bucket name; mage gcp:syncData mirrors the private data checkout into it and restores from it."
  value       = google_storage_bucket.corpus.name
}
