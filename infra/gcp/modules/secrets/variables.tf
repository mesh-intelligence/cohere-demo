# Copyright (c) 2026 Mesh Intelligence Inc.
# SPDX-License-Identifier: MIT-0

variable "project" {
  type        = string
  description = "GCP project; its number names the Workload Identity pool principal."
}

variable "application" {
  type        = string
  description = "Prefix of every secret id: <application>-<name>."
}

variable "namespace" {
  type        = string
  description = "Namespace of the KSA that reads the secrets through GKE secret sync."
}

variable "ksa" {
  type        = string
  description = "Kubernetes service account that reads the secrets."
}

variable "secrets" {
  type        = list(string)
  description = "Secret names; each becomes <application>-<name>."
}

variable "not_for_pods" {
  type        = list(string)
  description = "Names whose reader is not a pod (for example the registry token, read by Artifact Registry); no KSA grant is made for them."
  default     = []
}

variable "prevent_destroy" {
  type        = bool
  description = "Keep the secrets through terraform destroy."
}
