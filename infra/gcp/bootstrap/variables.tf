# Copyright (c) 2026 Mesh Intelligence Inc.
# SPDX-License-Identifier: MIT-0

variable "project" {
  type        = string
  description = "GCP project that holds the state bucket and every environment the bucket serves."
}

variable "region" {
  type        = string
  description = "Location of the state bucket."
}
