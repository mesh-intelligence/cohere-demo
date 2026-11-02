# Copyright (c) 2026 Mesh Intelligence Inc.
# SPDX-License-Identifier: MIT-0

variable "project" {
  type        = string
  description = "GCP project the load balancer and IAP policy live in."
}

variable "application" {
  type        = string
  description = "Application name; prefixes the certificate resources."
}

variable "namespace" {
  type        = string
  description = "Namespace of the chatbot Service; the Gateway and its policies live beside it."
}

variable "service" {
  type        = string
  description = "The chatbot Service the route, IAP policy, and health check target."
}

variable "chat_port" {
  type        = number
  description = "The chatbot Service's chat port."
}

variable "address_name" {
  type        = string
  description = "Name of the reserved global address the Gateway serves on (the bootstrap root's)."
}

variable "address" {
  type        = string
  description = "The reserved address; the default host is its sslip.io name."
}

variable "host" {
  type        = string
  description = "Public host of the chatbot; empty means the address's sslip.io name, which needs no DNS record."
  default     = ""
}

variable "iap_members" {
  type        = list(string)
  description = "IAM principals IAP admits (user:, group:, serviceAccount:, domain:)."
  validation {
    condition     = length(var.iap_members) > 0
    error_message = "iap_members is empty; IAP would admit nobody."
  }
  validation {
    condition     = alltrue([for m in var.iap_members : can(regex("^(user|group|serviceAccount|domain):.+$", m))])
    error_message = "every iap_members entry is an IAM principal: user:, group:, serviceAccount:, or domain:."
  }
}

variable "iap_client_id" {
  type        = string
  description = "Client id of the custom OAuth client IAP signs in with. An identifier, not a secret: the secret stays in Secret Manager."
}

variable "oauth_secret_name" {
  type        = string
  description = "Kubernetes Secret (synced from Secret Manager) whose client_secret key holds the OAuth client secret."
}
