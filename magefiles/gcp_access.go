// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// gcpAccess is cohere-demo's own section of the gitignored gcp.yaml (eng07:
// optional, literal defaults, no environment variables). The allowlist names
// real accounts, which is why it never lives in a committed file.
type gcpAccess struct {
	// Host is the chatbot's public name. Empty uses <address>.sslip.io, which
	// resolves to the reserved address with no DNS record to create.
	Host string `yaml:"host"`
	// IAPMembers are the IAM principals IAP admits, e.g. user:someone@example.com.
	IAPMembers []string `yaml:"iap_members"`
}

// loadGcpAccess reads the cohere_demo section of gcp.yaml beside root.
func loadGcpAccess(root string) (gcpAccess, error) {
	var file struct {
		CohereDemo gcpAccess `yaml:"cohere_demo"`
	}
	data, err := os.ReadFile(filepath.Join(root, gcpConfigFile))
	if err != nil {
		if os.IsNotExist(err) {
			return gcpAccess{}, nil
		}
		return gcpAccess{}, fmt.Errorf("read %s: %w", gcpConfigFile, err)
	}
	if err := yaml.Unmarshal(data, &file); err != nil {
		return gcpAccess{}, fmt.Errorf("parse %s: %w", gcpConfigFile, err)
	}
	return file.CohereDemo, nil
}

// requireIAPMembers refuses a public endpoint nobody may use, and members that
// are not IAM principals.
func requireIAPMembers(access gcpAccess) error {
	if len(access.IAPMembers) == 0 {
		return fmt.Errorf("gcp:deploy: %s names no cohere_demo.iap_members; IAP would admit nobody."+
			" Add e.g. user:<your-account> under cohere_demo.iap_members", gcpConfigFile)
	}
	for _, member := range access.IAPMembers {
		kind, _, found := strings.Cut(member, ":")
		switch {
		case !found:
			return fmt.Errorf("gcp:deploy: IAP member %q is not an IAM principal (want user:, group:, serviceAccount:, or domain:)", member)
		case kind != "user" && kind != "group" && kind != "serviceAccount" && kind != "domain":
			return fmt.Errorf("gcp:deploy: IAP member %q has unsupported kind %q", member, kind)
		}
	}
	return nil
}

// gcpExposureHost is the configured host, or the sslip.io name of the
// address. The exposure module resolves the same default (#885).
func gcpExposureHost(access gcpAccess, address string) string {
	if host := strings.TrimSpace(access.Host); host != "" {
		return host
	}
	return strings.ReplaceAll(address, ".", "-") + ".sslip.io"
}
