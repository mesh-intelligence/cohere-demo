# Copyright (c) 2026 Mesh Intelligence Inc.
# SPDX-License-Identifier: MIT-0

variable "project" {
  type        = string
  description = "GCP project the environment runs in."
}

variable "region" {
  type        = string
  description = "Region of the cluster."
}

variable "cluster" {
  type        = string
  description = "Cluster the chatbot runs on."
  default     = "cohere-demo-dev"
}

variable "address_name" {
  type        = string
  description = "Name of the bootstrap root's reserved global address."
}

variable "address" {
  type        = string
  description = "The bootstrap root's reserved address."
}

variable "host" {
  type        = string
  description = "Public host of the chatbot; empty means the reserved address's sslip.io name."
  default     = ""
}

variable "iap_members" {
  type        = list(string)
  description = "IAM principals IAP admits to the chatbot (user:, group:, serviceAccount:, domain:)."
}

variable "iap_client_id" {
  type        = string
  description = "Client id of the custom OAuth client, from the operator's keys.json."
}
