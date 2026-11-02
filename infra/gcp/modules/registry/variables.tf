# Copyright (c) 2026 Mesh Intelligence Inc.
# SPDX-License-Identifier: MIT-0

variable "project" {
  type        = string
  description = "GCP project; its number names the default node and Artifact Registry service accounts."
}

variable "region" {
  type        = string
  description = "Location of both repositories, matching the cluster's region."
}

variable "name" {
  type        = string
  description = "Prefix of the repository ids: <name>-ghcr and <name>-dockerhub."
}

variable "ghcr_username" {
  type        = string
  description = "GitHub account the read:packages token belongs to."
}

variable "ghcr_token_secret_id" {
  type        = string
  description = "Secret Manager secret holding the token, as projects/<project>/secrets/<id>."
}

variable "node_service_account" {
  type        = string
  description = "Identity the nodes pull as; empty means the project's default compute account."
  default     = ""
}

variable "ghcr_token_ready" {
  type        = bool
  description = "The token secret has an enabled version; until it does, the ghcr.io repository is not created."
}
