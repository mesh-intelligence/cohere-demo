# Copyright (c) 2026 Mesh Intelligence Inc.
# SPDX-License-Identifier: MIT-0

# The chatbot's public edge: a GKE Gateway on the reserved global address, a
# Certificate Manager certificate for the host, an HTTPRoute to the chatbot,
# IAP on the chatbot backend, the backend's health check, and who IAP admits.
# Only the chatbot is routed; the control plane, observer, and collector stay
# internal.
#
# The route rewrites no headers. The load balancer refuses to remove any
# x-goog-* header, and IAP adds its headers at the backend, after the route
# runs (found live in #883). The chatbot's routes declare the headers this
# edge adds instead (helm/ci/gcp-values.yaml, ingress.gke.transportHeaders).

locals {
  host = var.host != "" ? var.host : "${replace(var.address, ".", "-")}.sslip.io"
  name = "${var.application}-chatbot"
}

resource "google_project_service" "required" {
  for_each           = toset(["certificatemanager.googleapis.com", "iap.googleapis.com"])
  service            = each.value
  disable_on_destroy = false
}

# Managed with no DNS authorization: the load balancer answers the issuance
# challenge, so an sslip.io host needs no DNS record of ours.
resource "google_certificate_manager_certificate" "chatbot" {
  name = local.name
  managed {
    domains = [local.host]
  }
  depends_on = [google_project_service.required]
}

resource "google_certificate_manager_certificate_map" "chatbot" {
  name       = local.name
  depends_on = [google_project_service.required]
}

resource "google_certificate_manager_certificate_map_entry" "chatbot" {
  name         = local.name
  map          = google_certificate_manager_certificate_map.chatbot.name
  certificates = [google_certificate_manager_certificate.chatbot.id]
  hostname     = local.host
}

# Project scope: a grant on the one backend would need the backend service
# name GKE generates after the Gateway programs. The project is this
# application's own, so project scope admits nobody extra.
resource "google_iap_web_iam_member" "members" {
  for_each   = toset(var.iap_members)
  project    = var.project
  role       = "roles/iap.httpsResourceAccessor"
  member     = each.value
  depends_on = [google_project_service.required]
}

resource "kubernetes_manifest" "gateway" {
  manifest = {
    apiVersion = "gateway.networking.k8s.io/v1"
    kind       = "Gateway"
    metadata = {
      name        = "chatbot"
      namespace   = var.namespace
      annotations = { "networking.gke.io/certmap" = google_certificate_manager_certificate_map.chatbot.name }
    }
    spec = {
      gatewayClassName = "gke-l7-global-external-managed"
      listeners        = [{ name = "https", protocol = "HTTPS", port = 443 }]
      addresses        = [{ type = "NamedAddress", value = var.address_name }]
    }
  }
  depends_on = [google_certificate_manager_certificate_map_entry.chatbot]
}

resource "kubernetes_manifest" "route" {
  manifest = {
    apiVersion = "gateway.networking.k8s.io/v1"
    kind       = "HTTPRoute"
    metadata   = { name = "chatbot", namespace = var.namespace }
    spec = {
      parentRefs = [{ name = "chatbot" }]
      hostnames  = [local.host]
      rules = [{
        backendRefs = [{ name = var.service, port = var.chat_port }]
      }]
    }
  }
  depends_on = [kubernetes_manifest.gateway]
}

resource "kubernetes_manifest" "backend_policy" {
  manifest = {
    apiVersion = "networking.gke.io/v1"
    kind       = "GCPBackendPolicy"
    metadata   = { name = "chatbot", namespace = var.namespace }
    spec = {
      default = {
        iap = {
          enabled            = true
          clientID           = var.iap_client_id
          oauth2ClientSecret = { name = var.oauth_secret_name }
        }
      }
      targetRef = { group = "", kind = "Service", name = var.service }
    }
  }
}

# Gateway infers no health check from the pod's probes. The SPA answers 200 at
# /ui/; / is a redirect, which a load-balancer health check counts as down.
resource "kubernetes_manifest" "health_check" {
  manifest = {
    apiVersion = "networking.gke.io/v1"
    kind       = "HealthCheckPolicy"
    metadata   = { name = "chatbot", namespace = var.namespace }
    spec = {
      default = {
        config = {
          type            = "HTTP"
          httpHealthCheck = { requestPath = "/ui/", port = var.chat_port }
        }
      }
      targetRef = { group = "", kind = "Service", name = var.service }
    }
  }
}
