# Copyright (c) 2026 Mesh Intelligence Inc.
# SPDX-License-Identifier: MIT-0

# The development environment's public edge. A separate root from envs/dev
# because the Kubernetes provider plans against a live cluster and a
# namespace the release creates: gcp:deploy applies it after the release, and
# gcp:down destroys it before the cluster goes.

locals {
  application = "cohere-demo"
  namespace   = "app-cohere-demo"
  # The chart's fullname for release cohere-demo, and its chatbot.ports.chat.
  service   = "cohere-demo-chatbot-mesh-chatbot"
  chat_port = 18080
}

provider "google" {
  project = var.project
  region  = var.region
}

data "google_client_config" "this" {}

data "google_container_cluster" "this" {
  name     = var.cluster
  location = var.region
}

provider "kubernetes" {
  host                   = "https://${data.google_container_cluster.this.endpoint}"
  token                  = data.google_client_config.this.access_token
  cluster_ca_certificate = base64decode(data.google_container_cluster.this.master_auth[0].cluster_ca_certificate)
}

module "exposure" {
  source            = "../../../modules/exposure"
  project           = var.project
  application       = local.application
  namespace         = local.namespace
  service           = local.service
  chat_port         = local.chat_port
  address_name      = var.address_name
  address           = var.address
  host              = var.host
  iap_members       = var.iap_members
  iap_client_id     = var.iap_client_id
  oauth_secret_name = "${local.application}-iap-oauth"
}
