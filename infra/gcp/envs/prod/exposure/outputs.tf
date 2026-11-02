# Copyright (c) 2026 Mesh Intelligence Inc.
# SPDX-License-Identifier: MIT-0

# What gcp:deploy reads back after apply.

output "host" {
  value = module.exposure.host
}

output "address" {
  value = module.exposure.address
}
