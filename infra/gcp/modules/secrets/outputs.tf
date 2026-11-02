# Copyright (c) 2026 Mesh Intelligence Inc.
# SPDX-License-Identifier: MIT-0

output "ids" {
  description = "Secret name to resource id (projects/<project>/secrets/<id>)."
  value       = local.secret_ids
}
