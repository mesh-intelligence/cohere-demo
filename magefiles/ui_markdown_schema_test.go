// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The ministry template's markdown structural schema (srd012 R2), held in
// correspondence with the interface artifact beside it. ARCHITECTURE decision
// 12 keeps the template in three representations deliberately, so each pair
// that can drift gets a guard: ui_intsum_template_test.go holds the interface
// artifact against the app's table, and this holds the markdown schema
// against the interface artifact's composed-markdown sections. A section
// present in one and absent from the other is a brief the form fills and the
// verdict rejects, or the reverse — either way a defect no single artifact
// shows.

type markdownSchemaDoc struct {
	ID                     string `yaml:"id"`
	Version                string `yaml:"version"`
	WhatConformanceAsserts string `yaml:"what_conformance_asserts"`
	Sections               []struct {
		ID       string   `yaml:"id"`
		Label    string   `yaml:"label"`
		Order    int      `yaml:"order"`
		Required bool     `yaml:"required"`
		Headings []string `yaml:"headings"`
		Content  string   `yaml:"content"`
	} `yaml:"sections"`
}

func loadMarkdownSchema(t *testing.T) markdownSchemaDoc {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(meshUIRoot(t), "intsum-markdown-schema.yaml"))
	if err != nil {
		t.Fatalf("read intsum-markdown-schema.yaml: %v", err)
	}
	var doc markdownSchemaDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse intsum-markdown-schema.yaml: %v", err)
	}
	if len(doc.Sections) == 0 {
		t.Fatal("the markdown schema declares no sections; every check would pass vacuously")
	}
	return doc
}

// TestMarkdownSchemaIsVersionedStructureOnlyData covers the suite's
// schema-shape case (srd012 R2.2, R2.3, R2.5): versioned, required and
// optional distinguished, and no rendering vocabulary anywhere in the file.
func TestMarkdownSchemaIsVersionedStructureOnlyData(t *testing.T) {
	t.Parallel()
	doc := loadMarkdownSchema(t)
	if strings.TrimSpace(doc.Version) == "" {
		t.Error("the schema carries no version; a verdict could not name what it was evaluated against (R2.3)")
	}
	var required, optional int
	for _, section := range doc.Sections {
		if section.Required {
			required++
		} else {
			optional++
		}
		if len(section.Headings) == 0 {
			t.Errorf("section %s declares no headings; the check could not locate it in a brief", section.ID)
		}
		if strings.TrimSpace(section.Content) == "" {
			t.Errorf("section %s states no content requirement (R2.1)", section.ID)
		}
	}
	if required == 0 || optional == 0 {
		t.Errorf("schema declares %d required and %d optional sections; both must exist or the"+
			" optional-section-passes case asserts nothing (R2.2)", required, optional)
	}

	raw, err := os.ReadFile(filepath.Join(meshUIRoot(t), "intsum-markdown-schema.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	lower := strings.ToLower(string(raw))
	for _, rendering := range []string{"font", "spacing", "margin", "page-geometry", "page geometry", "px", "pt:", "typeface", "line-height"} {
		if strings.Contains(lower, rendering) {
			t.Errorf("the schema mentions %q; rendering concerns belong to the representations that render (R2.5)", rendering)
		}
	}
	if !strings.Contains(lower, "does not assert") {
		t.Error("what_conformance_asserts does not disclaim accuracy and support; a passing verdict must state what it cannot claim (srd012 R5)")
	}
}

// TestMarkdownSchemaMatchesTheTemplateArtifact is the ARCHITECTURE decision 12
// drift guard between the markdown and interface representations: the schema's
// sections are exactly the interface artifact's composed-markdown sections —
// same ids, same relative order, same required flags, same heading sets — in
// both directions.
func TestMarkdownSchemaMatchesTheTemplateArtifact(t *testing.T) {
	t.Parallel()
	schema := loadMarkdownSchema(t)
	template := declaredINTSUMTemplate(t)

	type sectionShape struct {
		order    int
		required bool
		headings string
	}
	templateComposed := map[string]sectionShape{}
	var templateOrder []string
	for _, section := range template.Sections {
		if section.Source != "composed_markdown" {
			continue
		}
		headings := append([]string(nil), section.Headings...)
		templateComposed[section.ID] = sectionShape{
			order:    len(templateOrder),
			required: section.Required,
			headings: strings.Join(headings, "|"),
		}
		templateOrder = append(templateOrder, section.ID)
	}
	if len(templateComposed) == 0 {
		t.Fatal("the interface artifact declares no composed-markdown sections; the correspondence would pass vacuously")
	}

	for index, section := range schema.Sections {
		shape, ok := templateComposed[section.ID]
		if !ok {
			t.Errorf("schema section %s has no composed-markdown counterpart in intsum-template.yaml", section.ID)
			continue
		}
		if shape.order != index {
			t.Errorf("section %s sits at schema position %d and template position %d; the representations disagree on order", section.ID, index, shape.order)
		}
		if shape.required != section.Required {
			t.Errorf("section %s: schema required=%v, template required=%v", section.ID, section.Required, shape.required)
		}
		if got := strings.Join(section.Headings, "|"); got != shape.headings {
			t.Errorf("section %s headings drifted:\nschema:   %s\ntemplate: %s", section.ID, got, shape.headings)
		}
	}
	for id := range templateComposed {
		found := false
		for _, section := range schema.Sections {
			if section.ID == id {
				found = true
			}
		}
		if !found {
			t.Errorf("template composed-markdown section %s is absent from the markdown schema", id)
		}
	}
}
