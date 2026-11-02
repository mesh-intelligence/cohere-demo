# Copyright (c) 2026 Mesh Intelligence Inc.
# SPDX-License-Identifier: MIT-0

variable "name" {
  type        = string
  description = "Cluster name."
}

variable "region" {
  type        = string
  description = "Region of the regional Autopilot control plane."
}

variable "deletion_protection" {
  type        = bool
  description = "Refuse terraform destroy on the cluster. Off where the cluster is torn down between sessions."
}

variable "release_channel" {
  type        = string
  description = "GKE release channel: RAPID, REGULAR, or STABLE."
  validation {
    condition     = contains(["RAPID", "REGULAR", "STABLE"], var.release_channel)
    error_message = "release_channel must be RAPID, REGULAR, or STABLE."
  }
}
