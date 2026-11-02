# Copyright (c) 2026 Mesh Intelligence Inc.
# SPDX-License-Identifier: MIT-0

terraform {
  required_version = ">= 1.9"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 7.46"
    }
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "~> 2.38"
    }
  }
  # bucket and prefix come from gcp:deploy at init: the bootstrap root's
  # bucket, prefix cohere-demo/dev-exposure. Nothing here names a project.
  backend "gcs" {}
}
