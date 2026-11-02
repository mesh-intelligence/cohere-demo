# Copyright (c) 2026 Mesh Intelligence Inc.
# SPDX-License-Identifier: MIT-0

terraform {
  required_version = ">= 1.9"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 7.46"
    }
    time = {
      source  = "hashicorp/time"
      version = "~> 0.13"
    }
  }
  # bucket and prefix come from gcp:up at init: the bootstrap root's bucket,
  # prefix cohere-demo/prod. Nothing here names a project.
  backend "gcs" {}
}

provider "google" {
  project = var.project
  region  = var.region
}
