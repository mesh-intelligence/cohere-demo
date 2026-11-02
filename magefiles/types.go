// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

// The platform's typed-tools migration, mounted as the types namespace
// (types:migrate, types:check) so this consumer runs the fleet's one
// converter (declarative-agents srd065 R7).

import (
	//mage:import types
	_ "github.com/mesh-intelligence/declarative-agents/magefiles/reusetypes"
)
