// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The conformance chain's unit gates (srd012 R2, R3, R5). The patterns the
// check words carry are declared configuration derived from the markdown
// schema artifact, so the pair gets the same drift guard the schema has
// against the interface artifact; and because the patterns are plain regexes,
// their semantics are pinned here against fixture briefs without booting a
// mesh — the same input, the same verdict, every run (R3.4).

type conformanceDeclarations struct {
	Tools []struct {
		Name   string `yaml:"name"`
		Init   string `yaml:"init"`
		Config struct {
			Schema struct {
				Properties struct {
					Markdown struct {
						Pattern string `yaml:"pattern"`
					} `yaml:"markdown"`
				} `yaml:"properties"`
			} `yaml:"schema"`
			Template string `yaml:"template"`
		} `yaml:"config"`
	} `yaml:"tools"`
}

func loadConformanceDeclarations(t *testing.T) conformanceDeclarations {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "agents", "chatbot", "request-conformance-declarations.yaml"))
	if err != nil {
		t.Fatalf("read conformance declarations: %v", err)
	}
	var doc conformanceDeclarations
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse conformance declarations: %v", err)
	}
	return doc
}

func conformancePattern(t *testing.T, doc conformanceDeclarations, word string) string {
	t.Helper()
	for _, tool := range doc.Tools {
		if tool.Name == word {
			if tool.Config.Schema.Properties.Markdown.Pattern == "" {
				t.Fatalf("%s declares no markdown pattern", word)
			}
			return tool.Config.Schema.Properties.Markdown.Pattern
		}
	}
	t.Fatalf("conformance declarations carry no %s word", word)
	return ""
}

// sectionPresencePattern rebuilds a check word's expected pattern from the
// schema artifact, the construction the declarations document: the section's
// heading aliases as an ATX heading line with non-whitespace content after it.
func sectionPresencePattern(headings []string) string {
	return fmt.Sprintf(`(?s)(?:^|.*\n)#{1,6}[ \t]+(?:%s)[ \t]*\n+[ \t]*[^\s#].*`, strings.Join(headings, "|"))
}

// TestConformancePatternsMatchTheMarkdownSchema is the drift guard between
// the markdown schema artifact and the check words' declared patterns
// (srd012 AC6): the schema stays the sole authority, enforced by test the way
// the schema itself is held to the interface artifact. It also pins the
// verdict legs' template_version to the schema's version, so a verdict names
// what it was actually evaluated against (R2.3).
func TestConformancePatternsMatchTheMarkdownSchema(t *testing.T) {
	t.Parallel()
	schema := loadMarkdownSchema(t)
	doc := loadConformanceDeclarations(t)

	checkWords := map[string]string{
		"bluf":            "check_bluf_section",
		"threat-activity": "check_threat_activity_section",
		"gaps":            "check_gaps_section",
	}
	var orderedRequired []string
	for _, section := range schema.Sections {
		if !section.Required {
			continue
		}
		word, ok := checkWords[section.ID]
		if !ok {
			t.Errorf("schema declares required section %s and the chain has no check word for it", section.ID)
			continue
		}
		delete(checkWords, section.ID)
		want := sectionPresencePattern(section.Headings)
		if got := conformancePattern(t, doc, word); got != want {
			t.Errorf("%s pattern drifted from the schema:\ngot:  %s\nwant: %s", word, got, want)
		}
		orderedRequired = append(orderedRequired, fmt.Sprintf(`#{1,6}[ \t]+(?:%s)[ \t]*`, strings.Join(section.Headings, "|")))
	}
	for id := range checkWords {
		t.Errorf("the chain checks section %s, which the schema does not declare as required", id)
	}

	wantOrder := fmt.Sprintf(`(?s)(?:^|.*\n)%s(?:\n.*)?`, strings.Join(orderedRequired, `\n.*\n`))
	if got := conformancePattern(t, doc, "check_section_order"); got != wantOrder {
		t.Errorf("order pattern drifted from the schema's section order:\ngot:  %s\nwant: %s", got, wantOrder)
	}

	versionToken := fmt.Sprintf(`"template_version": "%s"`, schema.Version)
	for _, leg := range []string{"declare_brief_conformant", "declare_brief_nonconformant", "declare_conformance_unchecked"} {
		for _, tool := range doc.Tools {
			if tool.Name == leg && !strings.Contains(tool.Config.Template, versionToken) {
				t.Errorf("%s does not name the schema version %s in its verdict (R2.3)", leg, schema.Version)
			}
		}
	}
}

// TestConformanceChainReachesNoModel is the R3.2 gate at the declaration
// level: every word on the chain is a pure builtin — composition, structural
// parsing, partitioning, rendering, comparison — and none is a model
// invocation or an outbound call. Purity is also what makes the verdict
// deterministic (R3.4): the same brief meets the same regexes.
func TestConformanceChainReachesNoModel(t *testing.T) {
	t.Parallel()
	doc := loadConformanceDeclarations(t)
	allowed := map[string]bool{
		"compose": true, "parse_structured": true, "partition": true,
		"render_each": true, "value_predicate": true,
	}
	if len(doc.Tools) == 0 {
		t.Fatal("the conformance declarations carry no words; the gate would pass vacuously")
	}
	for _, tool := range doc.Tools {
		if !allowed[tool.Init] {
			t.Errorf("chain word %s uses init %q; the check must reach no model and no boundary (srd012 R3.2)", tool.Name, tool.Init)
		}
	}
}

// TestConformancePatternsJudgeFixtureBriefs pins the declared patterns'
// behavior on fixture briefs: a conforming brief passes every check, each
// defect class fails exactly the checks that name it, and a brief omitting
// the optional section still passes (srd012 R2.2, R3.3; the suite's pass,
// missing-section, and optional-section cases at the pattern level).
func TestConformancePatternsJudgeFixtureBriefs(t *testing.T) {
	t.Parallel()
	doc := loadConformanceDeclarations(t)
	patterns := map[string]*regexp.Regexp{}
	for _, word := range []string{"check_bluf_section", "check_threat_activity_section", "check_gaps_section", "check_section_order"} {
		compiled, err := regexp.Compile("^(?:" + conformancePattern(t, doc, word) + ")$")
		if err != nil {
			t.Fatalf("compile %s pattern: %v", word, err)
		}
		patterns[word] = compiled
	}

	conforming := "# Bottom Line Up Front\n\nStrikes continued.\n\n# Threat Activity\n\nMissile waves on the grid.\n\n# Gaps\n\nNo BDA on the western sites.\n\n# Outlook\n\nMore of the same.\n"
	withoutOptional := "## BLUF\n\nThe line.\n\n## Assessment\n\nThe body.\n\n## Intelligence Gaps\n\nThe gaps.\n"
	missingGaps := "# Bottom Line Up Front\n\nThe line.\n\n# Threat Activity\n\nThe body.\n"
	emptyBluf := "# BLUF\n\n# Threat Activity\n\nThe body.\n\n# Gaps\n\nThe gaps.\n"
	reordered := "# Gaps\n\nThe gaps.\n\n# Bottom Line Up Front\n\nThe line.\n\n# Threat Activity\n\nThe body.\n"

	cases := []struct {
		name   string
		brief  string
		expect map[string]bool
	}{
		{"conforming brief passes every check", conforming, map[string]bool{
			"check_bluf_section": true, "check_threat_activity_section": true,
			"check_gaps_section": true, "check_section_order": true,
		}},
		{"omitting the optional section still conforms", withoutOptional, map[string]bool{
			"check_bluf_section": true, "check_threat_activity_section": true,
			"check_gaps_section": true, "check_section_order": true,
		}},
		{"missing required section fails its check and the order", missingGaps, map[string]bool{
			"check_bluf_section": true, "check_threat_activity_section": true,
			"check_gaps_section": false, "check_section_order": false,
		}},
		{"unpopulated required section fails its check", emptyBluf, map[string]bool{
			"check_bluf_section": false, "check_threat_activity_section": true,
			"check_gaps_section": true,
		}},
		{"present sections out of order fail only the order check", reordered, map[string]bool{
			"check_bluf_section": true, "check_threat_activity_section": true,
			"check_gaps_section": true, "check_section_order": false,
		}},
	}
	for _, tc := range cases {
		for word, want := range tc.expect {
			if got := patterns[word].MatchString(tc.brief); got != want {
				t.Errorf("%s: %s = %v, want %v", tc.name, word, got, want)
			}
		}
		// Determinism (R3.4): the second evaluation of the same brief agrees.
		for word, want := range tc.expect {
			if got := patterns[word].MatchString(tc.brief); got != want {
				t.Errorf("%s: %s changed verdict on re-evaluation", tc.name, word)
			}
		}
	}
}

// TestConformanceVerdictCarriesNoApprovalLanguage covers the suite's
// no-approval case at the metadata level: no verdict leg's template describes
// a brief as approved, verified, or reviewed, and every leg carries the scope
// statement of what conformance does not assert (srd012 R5.3, AC5).
func TestConformanceVerdictCarriesNoApprovalLanguage(t *testing.T) {
	t.Parallel()
	doc := loadConformanceDeclarations(t)
	var legs int
	for _, tool := range doc.Tools {
		if !strings.HasPrefix(tool.Name, "declare_brief_") && tool.Name != "declare_conformance_unchecked" {
			continue
		}
		legs++
		lower := strings.ToLower(tool.Config.Template)
		for _, approval := range []string{"approved", "verified", "reviewed", "validated as true", "accurate"} {
			if strings.Contains(lower, approval) {
				t.Errorf("%s renders %q; conformance is structure, not review", tool.Name, approval)
			}
		}
		if !strings.Contains(tool.Config.Template, `"asserts"`) {
			t.Errorf("%s carries no asserts scope statement (srd012 R5.3)", tool.Name)
		}
	}
	if legs != 3 {
		t.Fatalf("found %d verdict legs, want conformant, non_conformant, and unchecked", legs)
	}
}
