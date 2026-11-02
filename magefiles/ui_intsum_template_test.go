// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The ministry INTSUM template exists twice: as the declaration in
// agents/chatbot/ui/intsum-template.yaml and as the table the app renders from
// in intsumTemplate.ts. That is the same shape ui.yaml and the panel registry
// are in, and it is checked the same way, because each artifact read alone
// looks correct --
// the declaration lists sections, the app renders them -- and only the two read
// together show the form is rendering a structure the template does not
// declare (srd016 R2.4).
//
// This matters more than the route check does. rel12.2 validates a brief's
// markdown against the template version this artifact names, so a section the
// app renders and the artifact omits is a section no conformance check will
// ever look for.

type intsumTemplateDoc struct {
	Version  string `yaml:"version"`
	Sections []struct {
		ID          string `yaml:"id"`
		Label       string `yaml:"label"`
		Order       int    `yaml:"order"`
		Source      string `yaml:"source"`
		DerivedFrom string `yaml:"derived_from"`
		Required    bool   `yaml:"required"`
		Headings    []string
	} `yaml:"sections"`
}

// intsumSectionRE matches an entry of INTSUM_SECTIONS in intsumTemplate.ts by
// its id and the source that follows it, for example:
//
//	id: "bluf",
//	label: "Bottom Line Up Front",
//	order: 2,
//	source: "composed_markdown",
var intsumSectionRE = regexp.MustCompile(
	`id:\s*"([a-z-]+)",\s*\n\s*label:\s*"([^"]+)",\s*\n\s*order:\s*(\d+),\s*\n\s*source:\s*"([a-z_]+)"`)

var intsumVersionRE = regexp.MustCompile(`INTSUM_TEMPLATE_VERSION\s*=\s*"([^"]+)"`)

func declaredINTSUMTemplate(t *testing.T) intsumTemplateDoc {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(meshUIRoot(t), "intsum-template.yaml"))
	if err != nil {
		t.Fatalf("read intsum-template.yaml: %v", err)
	}
	var doc intsumTemplateDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse intsum-template.yaml: %v", err)
	}
	if len(doc.Sections) == 0 {
		t.Fatal("the template declares no sections; the cross-check would pass vacuously")
	}
	return doc
}

func implementedINTSUMSections(t *testing.T) (map[string]struct {
	label  string
	order  int
	source string
}, string) {
	t.Helper()
	path := filepath.Join(meshUIRoot(t), "app", "src", "intsumTemplate.ts")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read intsumTemplate.ts: %v", err)
	}
	sections := map[string]struct {
		label  string
		order  int
		source string
	}{}
	for _, match := range intsumSectionRE.FindAllStringSubmatch(string(data), -1) {
		order := 0
		for _, digit := range match[3] {
			order = order*10 + int(digit-'0')
		}
		sections[match[1]] = struct {
			label  string
			order  int
			source string
		}{label: match[2], order: order, source: match[4]}
	}
	if len(sections) == 0 {
		t.Fatalf("no INTSUM_SECTIONS entries parsed from %s; the table shape changed and this guard went blind", path)
	}
	version := ""
	if match := intsumVersionRE.FindStringSubmatch(string(data)); match != nil {
		version = match[1]
	}
	return sections, version
}

// TestINTSUMTemplateArtifactDeclaresTheMinistrySections holds the artifact to
// the sections srd016 R2.2 names. The eight are named here rather than counted,
// because a template that declares eight of anything is not the ministry form.
func TestINTSUMTemplateArtifactDeclaresTheMinistrySections(t *testing.T) {
	t.Parallel()
	doc := declaredINTSUMTemplate(t)

	want := []string{
		"classification-banner",
		"header",
		"bluf",
		"threat-activity",
		"source-evaluation",
		"gaps",
		"outlook",
		"citation-index",
	}
	declared := map[string]bool{}
	for _, section := range doc.Sections {
		declared[section.ID] = true
	}
	for _, id := range want {
		if !declared[id] {
			t.Errorf("the template does not declare the %q section that srd016 R2.2 names", id)
		}
	}
	if doc.Version == "" {
		t.Error("the template carries no version; rel12.2 validates a brief against a named version")
	}

	// Every section says where its content comes from, because a composed
	// section and a derived one are absent for different reasons and the form
	// reports which (R3.2).
	for _, section := range doc.Sections {
		switch section.Source {
		case "composed_markdown":
			if len(section.Headings) == 0 {
				t.Errorf("section %q is composed from markdown and declares no headings, so nothing can fill it",
					section.ID)
			}
		case "derived":
			if section.DerivedFrom == "" {
				t.Errorf("section %q is derived and does not say what from", section.ID)
			}
		default:
			t.Errorf("section %q declares source %q, which is neither composed_markdown nor derived",
				section.ID, section.Source)
		}
	}
}

// TestINTSUMTemplateMatchesTheAppTable fails when the declaration and the app
// disagree in either direction, for the reason the route cross-check exists: a
// section declared and not rendered is absent from the form, and a section
// rendered and not declared is one no conformance check will look for.
func TestINTSUMTemplateMatchesTheAppTable(t *testing.T) {
	t.Parallel()
	doc := declaredINTSUMTemplate(t)
	implemented, version := implementedINTSUMSections(t)

	if version != doc.Version {
		t.Errorf("template version: the artifact declares %q, the app carries %q", doc.Version, version)
	}

	declared := map[string]bool{}
	for _, section := range doc.Sections {
		declared[section.ID] = true
		got, ok := implemented[section.ID]
		if !ok {
			t.Errorf("the template declares section %q that the app does not render", section.ID)
			continue
		}
		if got.label != section.Label {
			t.Errorf("section %q label: template has %q, app has %q", section.ID, section.Label, got.label)
		}
		if got.order != section.Order {
			t.Errorf("section %q order: template has %d, app has %d", section.ID, section.Order, got.order)
		}
		if got.source != section.Source {
			t.Errorf("section %q source: template has %q, app has %q", section.ID, section.Source, got.source)
		}
	}

	var undeclared []string
	for id := range implemented {
		if !declared[id] {
			undeclared = append(undeclared, id)
		}
	}
	sort.Strings(undeclared)
	if len(undeclared) > 0 {
		t.Errorf("the app renders sections the template does not declare: %v", undeclared)
	}
}

// TestINTSUMTemplateHeadingsCoverTheComposedBrief keeps the template's declared
// headings in step with what composition is actually asked for.
//
// The chatbot's composition prompt names the headings the provider writes. A
// template whose aliases do not cover them fills nothing from a real turn, and
// the form would be empty against a mesh that is working -- which reads as a
// broken panel rather than as a template that names different sections. This is
// the check that would have caught it: the deployed prompt asks for Assessment,
// Key Judgements, Supporting Reporting, and Gaps, and none of those four is the
// name of a section srd016 R2.2 declares.
func TestINTSUMTemplateHeadingsCoverTheComposedBrief(t *testing.T) {
	t.Parallel()
	doc := declaredINTSUMTemplate(t)

	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(root), "agents", "chatbot", "rest.yaml"))
	if err != nil {
		t.Fatalf("read the chatbot rest declarations: %v", err)
	}

	declared := map[string]bool{}
	for _, section := range doc.Sections {
		for _, heading := range section.Headings {
			declared[heading] = true
		}
	}

	// The headings the deployed composition prompt asks the provider for. Read
	// from the declaration rather than hard-coded, so a prompt edit that renames
	// one fails here instead of silently emptying the form.
	// Non-greedy to the sentence's period: the list wraps onto a continuation
	// line, and stopping at the newline reads half of it while running past the
	// period picks up the next bullet.
	promptHeadings := regexp.MustCompile(
		`(?s)Structure the brief as markdown with the ministry headings:\s*(.*?)\.`).
		FindStringSubmatch(string(data))
	if promptHeadings == nil {
		t.Skip("the composition prompt no longer names ministry headings inline; the template is the source now")
	}
	for _, heading := range splitPromptHeadings(promptHeadings[1]) {
		if !declared[heading] {
			t.Errorf("the composition prompt asks for the %q heading and no template section declares it,"+
				" so a brief carrying it fills nothing", heading)
		}
	}
}

// splitPromptHeadings pulls the heading names out of the prompt's comma-and-"and"
// list, dropping the trailing period the sentence ends with.
func splitPromptHeadings(list string) []string {
	cleaned := regexp.MustCompile(`\s+`).ReplaceAllString(list, " ")
	cleaned = regexp.MustCompile(`\.$`).ReplaceAllString(strings.TrimSpace(cleaned), "")
	var headings []string
	for _, part := range strings.Split(cleaned, ",") {
		part = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(part), "and "))
		if part != "" {
			headings = append(headings, part)
		}
	}
	return headings
}
