// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// chatbotProviderRestFiles are the chatbot REST files that expand provider
// library fragments: the query embedding, and the rerank with the harvest's
// document embedding.
var chatbotProviderRestFiles = []string{"embed-rest.yaml", "provider-rest.yaml"}

// copyChatbotProfile copies agents/chatbot into dstDir, passing each file
// through rewrite, and returns the copied profile.yaml. Relative references
// inside the copied files resolve against the copy's own directory; the launch
// still runs from the application root, so repository-relative paths keep
// resolving.
func copyChatbotProfile(applicationRoot, dstDir string, rewrite func(string) string) (string, error) {
	srcDir := filepath.Join(applicationRoot, "agents", "chatbot")
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return "", fmt.Errorf("read chatbot profile: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		content, err := os.ReadFile(filepath.Join(srcDir, entry.Name()))
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(dstDir, entry.Name()), []byte(rewrite(string(content))), 0o644); err != nil {
			return "", err
		}
	}
	return filepath.Join(dstDir, "profile.yaml"), nil
}

// pinChatbotProviderEndpoint points every provider library expansion in a
// copied chatbot profile at endpoint. A library fragment reads no environment:
// its endpoint and allowlist are its allow arguments, the library's own
// endpoint by default (srd058 R1.6), so a proof that stands a mock or a
// counting proxy where the provider calls go names it here, as the chart does
// for a deployment.
func pinChatbotProviderEndpoint(profileDir, endpoint string) error {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("parse provider endpoint %q: %w", endpoint, err)
	}
	port := parsed.Port()
	if port == "" {
		port = map[string]string{"https": "443", "http": "80"}[parsed.Scheme]
	}
	allow := [][2]string{{"allow_scheme", parsed.Scheme}, {"allow_host", parsed.Hostname()}, {"allow_port", port}}
	for _, name := range chatbotProviderRestFiles {
		if err := pinExpansionArgs(filepath.Join(profileDir, name), allow); err != nil {
			return err
		}
	}
	return nil
}

// pinExpansionArgs sets the given arguments on every fragment expansion in one
// REST file, replacing any the file already passes.
func pinExpansionArgs(path string, args [][2]string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	expand := mappingValue(doc.Content[0], "expand")
	if expand == nil || len(expand.Content) == 0 {
		return fmt.Errorf("%s expands no provider fragment to pin", path)
	}
	for _, expansion := range expand.Content {
		target := mappingValue(expansion, "args")
		if target == nil {
			return fmt.Errorf("%s has an expansion with no args to pin", path)
		}
		for _, arg := range args {
			if value := mappingValue(target, arg[0]); value != nil {
				value.SetString(arg[1])
				continue
			}
			key := &yaml.Node{Kind: yaml.ScalarNode, Value: arg[0]}
			value := &yaml.Node{Kind: yaml.ScalarNode, Value: arg[1]}
			target.Content = append(target.Content, key, value)
		}
	}
	var out bytes.Buffer
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err := encoder.Encode(&doc); err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	return os.WriteFile(path, out.Bytes(), 0o644)
}

// mappingValue is the value node under key in a YAML mapping, or nil.
func mappingValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}
