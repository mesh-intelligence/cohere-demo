# Copyright (c) 2026 Mesh Intelligence Inc.
# SPDX-License-Identifier: MIT-0

variable "project" {
  type        = string
  description = "GCP project; prefixes the globally unique bucket name."
}

variable "region" {
  type        = string
  description = "Bucket location."
}

variable "application" {
  type        = string
  description = "Application name; the bucket is <project>-<application>-telemetry and the account <application>-objectstore."
}

variable "namespace" {
  type        = string
  description = "Kubernetes namespace whose KSA may act as the service account."
}

variable "ksa" {
  type        = string
  description = "Kubernetes service account in that namespace."
}

variable "force_destroy" {
  type        = bool
  description = "Let terraform destroy remove the bucket with objects still in it."
}

variable "versioning" {
  type        = bool
  description = "Keep noncurrent object versions."
}

variable "retention_days" {
  type        = number
  description = "Delete objects older than this many days; 0 keeps them indefinitely."
}
