// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func helmPublishTestRoot(t *testing.T, tag string) string {
	t.Helper()
	root := t.TempDir()
	writePackageTestFile(t, filepath.Join(root, "go.mod"),
		"module example\n\n// declarative-agents "+tag+" (0123456789ab)\nrequire ()\n")
	return root
}

// Publish derives the version from the pin, packages into the push's own
// staging directory, pushes to the public OCI repository, and reads the chart
// back; a version already upstream is left alone and packages nothing.
func TestPublishChatbotChartPushesThePinDerivedVersion(t *testing.T) {
	root := helmPublishTestRoot(t, "v0.20261003.0")
	version := "0.1.0-v0.20261003.0"

	var calls [][]string
	var packed []string
	pushed := false
	helm := func(args ...string) ([]byte, error) {
		calls = append(calls, args)
		switch args[0] {
		case "show":
			if pushed {
				return []byte("name: chatbot-mesh"), nil
			}
			return nil, errors.New("not found")
		case "push":
			pushed = true
			return []byte("pushed"), nil
		}
		return nil, errors.New("unexpected")
	}
	pack := func(destination, packVersion string) error {
		packed = append(packed, packVersion)
		return os.WriteFile(filepath.Join(destination, "chatbot-mesh-"+packVersion+".tgz"), []byte("tgz"), 0o644)
	}
	if err := publishChatbotChart(helm, pack, root); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if len(packed) != 1 || packed[0] != version {
		t.Errorf("packed versions = %v, want one %s", packed, version)
	}
	var push []string
	for _, call := range calls {
		if call[0] == "push" {
			push = call
		}
	}
	joined := strings.Join(push, " ")
	if !strings.Contains(joined, "chatbot-mesh-"+version+".tgz") || !strings.Contains(joined, chartOCIRepository) {
		t.Errorf("push call = %s, want the versioned archive to %s", joined, chartOCIRepository)
	}

	// The same pin publishes nothing twice.
	packed = nil
	if err := publishChatbotChart(helm, pack, root); err != nil {
		t.Fatalf("republish: %v", err)
	}
	if len(packed) != 0 {
		t.Error("an already published version was repackaged")
	}
}

// A push the registry does not read back afterwards fails the publish.
func TestPublishChatbotChartRequiresReadback(t *testing.T) {
	root := helmPublishTestRoot(t, "v0.20261003.0")
	helm := func(args ...string) ([]byte, error) {
		if args[0] == "push" {
			return []byte("pushed"), nil
		}
		return nil, errors.New("not found")
	}
	pack := func(destination, version string) error {
		return os.WriteFile(filepath.Join(destination, "chatbot-mesh-"+version+".tgz"), []byte("tgz"), 0o644)
	}
	err := publishChatbotChart(helm, pack, root)
	if err == nil || !strings.Contains(err.Error(), "not readable after the push") {
		t.Fatalf("err = %v, want the readback failure named", err)
	}
}
