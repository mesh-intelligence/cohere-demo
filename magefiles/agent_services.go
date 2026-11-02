// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"github.com/mesh-intelligence/declarative-agents/magefiles/helmlib"
)

// The chart renders its collector through the shared agent-services library
// chart (GH-788), the way every mesh-intelligence application chart does. The
// pinned declarative-agents magefiles module embeds that library at its
// release (declarative-agents #329), so chart preparation vendors it untracked
// into helm/charts/agent-services from the build itself: no fetch, no cache,
// no git credentials (GH-851). A go.work build against a sibling checkout
// embeds that checkout's copy, the same way it builds that checkout's code.

// vendorAgentServices replaces chartDir/charts/agent-services with the
// library chart the pinned magefiles module carries.
func vendorAgentServices(chartDir string) error {
	return helmlib.VendorEmbedded(chartDir)
}
