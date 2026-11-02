# Copyright (c) 2026 Mesh Intelligence Inc.
# SPDX-License-Identifier: MIT-0

variable "project" {
  type        = string
  description = "GCP project the environment runs in."
}

variable "region" {
  type        = string
  description = "Region of the cluster, registries, and bucket."
}

variable "cluster" {
  type        = string
  description = "Cluster name."
  default     = "cohere-demo-dev"
}

variable "node_service_account" {
  type        = string
  description = "Identity the nodes pull as; empty means the project's default compute account."
  default     = ""
}

variable "ghcr_username" {
  type        = string
  description = "GitHub account the ghcr-pull token belongs to."
}



variable "ghcr_token_ready" {
  type        = bool
  description = "Set by gcp:up from a version check on the ghcr-pull secret; the ghcr.io repository waits for it."
  default     = false
}
