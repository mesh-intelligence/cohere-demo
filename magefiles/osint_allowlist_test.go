// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The approved-domain allowlist and the air-gap toggle (srd011 R1.2-R1.5,
// R6): the search client's network policy is the declared allowlist, a
// disabled deployment renders no external search host anywhere, and no
// request-supplied value can select a host. Suite cases 2, 4, and 12 as
// rendered-chart pins; the client-side drop and the disabled turn are the
// live halves.

// TestOSINTPolicyHostsEqualTheDeclaredAllowlist is srd011 R1.2: with
// osint.allowedHosts declared, the rendered network policy carries exactly
// that list -- not the endpoint's own host -- so an endpoint steered outside
// the allowlist is dropped by the client rather than approved by derivation
// (suite case 2's deployment shape).
func TestOSINTPolicyHostsEqualTheDeclaredAllowlist(t *testing.T) {
	t.Parallel()
	render := renderOSINTChart(t,
		"--set", "osint.searchBaseURL=http://engine.example:8090/api/search",
		"--set", "osint.allowedHosts={approved.example,second.example}")
	deployed := deployedOSINTRest(t, render)
	limits, ok := deployed.Rest.Limits["osint_search"]
	if !ok {
		t.Fatal("the rendered rest.yaml declares no osint_search limits")
	}
	hosts := strings.Join(limits.Network.Hosts, ",")
	if hosts != "approved.example,second.example" {
		t.Errorf("rendered policy hosts are [%s], not the declared allowlist", hosts)
	}
	client := deployed.Rest.Clients["osint"]
	if client.BaseURL != "http://engine.example:8090" {
		t.Errorf("client base_url is %q; the endpoint stays values-driven while the allowlist governs", client.BaseURL)
	}
}

// TestOSINTDisabledRenderCarriesNoSearchHost is srd011 R6.2 and suite case
// 12: with the branch disabled, no search host -- fixture, engine, or
// allowlisted -- appears anywhere in the rendered policy or client, the
// client collapses to the loopback address, and the rendered declarations
// carry the disabled toggle operand so the machine takes the retrieval-only
// leg. The capability is removed, not merely skipped.
func TestOSINTDisabledRenderCarriesNoSearchHost(t *testing.T) {
	t.Parallel()
	render := renderOSINTChart(t,
		"--set", "osint.enabled=false",
		"--set", "osint.searchBaseURL=http://engine.example:8090/api/search",
		"--set", "osint.allowedHosts={approved.example}")
	for _, host := range []string{"engine.example", "approved.example", "osint-fixture"} {
		if strings.Contains(render, host) {
			t.Errorf("the disabled render still names search host %q", host)
		}
	}
	deployed := deployedOSINTRest(t, render)
	limits, ok := deployed.Rest.Limits["osint_search"]
	if !ok {
		t.Fatal("the disabled render must keep the osint_search limits; the word registers at load")
	}
	for _, host := range limits.Network.Hosts {
		if host != "127.0.0.1" && host != "localhost" {
			t.Errorf("disabled policy still permits host %q", host)
		}
	}
	if client := deployed.Rest.Clients["osint"]; !strings.HasPrefix(client.BaseURL, "http://127.0.0.1:") {
		t.Errorf("disabled client base_url is %q, not the loopback collapse", client.BaseURL)
	}
	if strings.Contains(render, "left: osint-branch-enabled") {
		t.Error("the disabled render still carries the enabled toggle operand")
	}
	// The declarations ride the staged profiles tree, which a raw-chart render
	// does not carry, so the rewrite is pinned by mechanism: the committed
	// file holds the enabled operand exactly once and never the disabled one,
	// and the ConfigMap template performs exactly that replacement.
	decl := string(readAgentYAML(t, "chatbot", "request-osint-declarations.yaml"))
	if strings.Count(decl, "left: osint-branch-enabled") != 1 || strings.Contains(decl, "left: osint-branch-disabled") {
		t.Error("the toggle operand is not unique in the committed declarations; the rendered rewrite would miss or overreach")
	}
	tpl := readChartFile(t, "templates", "profiles-configmap.yaml")
	if !strings.Contains(tpl, `replace "left: osint-branch-enabled" "left: osint-branch-disabled"`) {
		t.Error("the profiles ConfigMap does not rewrite the toggle operand for a disabled deployment")
	}
}

// readChartFile reads a file from the chart directory.
func readChartFile(t *testing.T, parts ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{findChartDir(t)}, parts...)...)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read chart file: %v", err)
	}
	return string(data)
}

// TestOSINTHostCannotBeSelectedByRequestFields is srd011 R1.5 as a
// declaration pin (suite case 4): the search operation binds no runtime path,
// header, or base URL selector, so no model output, tasking, history, or body
// field has a declared route to the host, method, path, or policy. The only
// runtime binding is the tasking, which TestOSINTEgressCarriesTaskingOnly
// pins by value.
func TestOSINTHostCannotBeSelectedByRequestFields(t *testing.T) {
	t.Parallel()
	committed := decodeOSINTRest(t, "committed", readAgentYAML(t, "chatbot", "rest.yaml"))
	deployed := deployedOSINTRest(t, renderOSINTChart(t))
	for label, doc := range map[string]osintRestDoc{"committed": committed, "deployed": deployed} {
		op, ok := doc.Rest.Clients["osint"].Operations["search"]
		if !ok {
			t.Errorf("%s osint client declares no search operation", label)
			continue
		}
		if op.BaseURLSource != "" || op.BaseURLSelector != "" {
			t.Errorf("%s search operation binds a runtime base URL (%q/%q); the host is fixed configuration",
				label, op.BaseURLSource, op.BaseURLSelector)
		}
		if len(op.Params.Path) != 0 || len(op.Params.Headers) != 0 {
			t.Errorf("%s search operation binds runtime path or header params: %v %v",
				label, op.Params.Path, op.Params.Headers)
		}
	}
}
