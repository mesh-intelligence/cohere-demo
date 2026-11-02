# Copyright (c) 2026 Mesh Intelligence Inc.
# SPDX-License-Identifier: MIT-0

output "host" {
  description = "The chatbot's public host."
  value       = local.host
}

output "address" {
  value = var.address
}
