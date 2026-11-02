# Copyright (c) 2026 Mesh Intelligence Inc.
# SPDX-License-Identifier: MIT-0

# The Terraform state bucket every environment root keeps its state in. It
# cannot keep its own state in the bucket it creates, so this root alone runs
# on local state (gitignored). A machine without that local state imports the
# bucket by name before applying, so the root is safe to run from anywhere.
# Retiring the demo destroys this root last; force_destroy lets it go with old
# state objects still inside.

terraform {
  required_version = ">= 1.9"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 7.46"
    }
  }
}

provider "google" {
  project = var.project
  region  = var.region
}

resource "google_project_service" "storage" {
  service            = "storage.googleapis.com"
  disable_on_destroy = false
}

resource "google_storage_bucket" "tfstate" {
  name                        = "${var.project}-tfstate"
  location                    = var.region
  uniform_bucket_level_access = true
  public_access_prevention    = "enforced"
  force_destroy               = true

  # State history: a bad apply can be rolled back to the previous object.
  versioning {
    enabled = true
  }

  depends_on = [google_project_service.storage]
}

resource "google_project_service" "compute" {
  service            = "compute.googleapis.com"
  disable_on_destroy = false
}

# The chatbot's public address. It outlives every environment so the sslip.io
# host and its certificate stay the same from one session to the next;
# retiring the demo (gcp:bootstrapDown) releases it with the state bucket.
resource "google_compute_global_address" "ingress" {
  name       = "cohere-demo-ingress"
  depends_on = [google_project_service.compute]
}

# The private corpus bucket: the operator-independent copy of the private data
# checkout (mesh-intelligence/cohere-demo-data, GH-910). mage gcp:syncData
# rsyncs the checkout up and restores a fresh workstation from it. It lives in
# this root rather than an environment root because the data outlives every
# environment the way the address does: gcp:down leaves it, gcp:bootstrapDown
# removes it with the rest. The content is all-rights-reserved and scraped
# third-party material admitted to a private demonstration only, so public
# access is prevented at the bucket; force_destroy is safe because the private
# repository, not this bucket, is the source of truth.
resource "google_storage_bucket" "corpus" {
  name                        = "${var.project}-corpus"
  location                    = var.region
  uniform_bucket_level_access = true
  public_access_prevention    = "enforced"
  force_destroy               = true

  depends_on = [google_project_service.storage]
}
