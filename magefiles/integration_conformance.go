// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// The rel12.2 tracer (rel12.2-uc001, srd012). It proves the conformance check
// against the deployed mesh the way an analyst meets it: live turns, whose
// briefs a live provider composes.
//
// A live provider decides its own structure, so the tracer does not gamble on
// which verdict a turn earns. Its core assertion is verdict truth: for every
// turn, the deployed verdict must equal the verdict the declared patterns
// yield when this harness re-evaluates them against the returned brief -- the
// same construction the unit tests pin against fixtures, now held against
// production output. On top of that, each leg steers composition toward one
// outcome: the flag leg asks for a reply no template could match and must see
// non_conformant with the recomputed defects; the pass leg asks for the
// template's own sections and retries, paced, until a brief conforms.

const (
	conformanceChatEndpoint = "/api/v1/chat"
	conformanceTurnTimeout  = 5 * time.Minute
	// Paced apart to stay under provider rate limits; a rate-limited chat leg
	// degrades to the local tier, which composes unheaded prose and turns
	// every steering attempt into a flag leg.
	conformancePace = 45 * time.Second

	conformanceFlagTasking = "Reply with exactly the two words: acknowledged, standing by. Use no headings and no other text."
	conformancePassTasking = "Prepare a command-staff brief on Russian strikes on Ukrainian infrastructure in December 2022 and the air-defence response." +
		" Structure the brief as markdown with exactly these three sections in this order, each as a # heading with a short paragraph under it:" +
		" Bottom Line Up Front, Threat Activity, Gaps."
)

type conformanceVerdict struct {
	Verdict         string   `json:"verdict"`
	TemplateVersion string   `json:"template_version"`
	Defects         []string `json:"defects"`
	Asserts         string   `json:"asserts"`
}

type conformanceTurn struct {
	Answer   string `json:"answer"`
	Metadata struct {
		AnsweredBy  string              `json:"answered_by"`
		Conformance *conformanceVerdict `json:"conformance"`
	} `json:"metadata"`
}

// conformanceCheckPatterns loads the declared check patterns and their defect
// strings from the conformance declarations, the same artifact the deployed
// profile packages, so the tracer re-evaluates exactly what the mesh runs.
type conformanceCheck struct {
	pattern *regexp.Regexp
	defect  string
}

func conformanceCheckPatterns(root string) ([]conformanceCheck, error) {
	data, err := os.ReadFile(filepath.Join(root, "agents", "chatbot", "request-conformance-declarations.yaml"))
	if err != nil {
		return nil, err
	}
	var doc struct {
		Tools []struct {
			Name   string `yaml:"name"`
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
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	patterns := map[string]string{}
	defects := map[string]string{}
	for _, tool := range doc.Tools {
		if p := tool.Config.Schema.Properties.Markdown.Pattern; p != "" {
			patterns[tool.Name] = p
		}
		if strings.HasPrefix(tool.Name, "record_") && strings.HasSuffix(tool.Name, "_defect") {
			var rendered struct {
				Defect string `json:"defect"`
			}
			if err := json.Unmarshal([]byte(tool.Config.Template), &rendered); err != nil {
				return nil, fmt.Errorf("parse %s defect template: %w", tool.Name, err)
			}
			defects[strings.TrimSuffix(strings.TrimPrefix(tool.Name, "record_"), "_defect")] = rendered.Defect
		}
	}
	pairs := []struct{ check, leg string }{
		{"check_bluf_section", "bluf"},
		{"check_threat_activity_section", "threat_activity"},
		{"check_gaps_section", "gaps"},
		{"check_section_order", "order"},
	}
	var checks []conformanceCheck
	for _, pair := range pairs {
		pattern, ok := patterns[pair.check]
		if !ok {
			return nil, fmt.Errorf("declarations carry no pattern for %s", pair.check)
		}
		compiled, err := regexp.Compile("^(?:" + pattern + ")$")
		if err != nil {
			return nil, fmt.Errorf("compile %s: %w", pair.check, err)
		}
		defect, ok := defects[pair.leg]
		if !ok {
			return nil, fmt.Errorf("declarations carry no defect string for %s", pair.leg)
		}
		checks = append(checks, conformanceCheck{pattern: compiled, defect: defect})
	}
	return checks, nil
}

// expectedDefects re-evaluates the declared patterns against a brief, which is
// what the deployed chain does; determinism (srd012 R3.4) is exactly this
// agreement holding on every turn.
func expectedDefects(checks []conformanceCheck, brief string) []string {
	var out []string
	for _, check := range checks {
		if !check.pattern.MatchString(brief) {
			out = append(out, check.defect)
		}
	}
	return out
}

func runConformanceTurn(base, tasking string) (conformanceTurn, error) {
	var turn conformanceTurn
	body, err := json.Marshal(map[string]interface{}{"message": tasking, "history": []interface{}{}})
	if err != nil {
		return turn, err
	}
	request, err := http.NewRequest(http.MethodPost, base+conformanceChatEndpoint, bytes.NewReader(body))
	if err != nil {
		return turn, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Analyst-Clearance", "secret")
	client := &http.Client{Timeout: conformanceTurnTimeout}
	response, err := client.Do(request)
	if err != nil {
		return turn, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return turn, fmt.Errorf("chat returned status %d", response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(&turn); err != nil {
		return turn, err
	}
	return turn, nil
}

// assertVerdictTrue holds the deployed verdict to the re-evaluated one: same
// verdict, same defect set, the schema version named, the asserts statement
// present and free of approval language.
func assertVerdictTrue(checks []conformanceCheck, turn conformanceTurn, leg string) error {
	verdict := turn.Metadata.Conformance
	if verdict == nil {
		return fmt.Errorf("%s: the turn carries no conformance verdict", leg)
	}
	if verdict.TemplateVersion == "" {
		return fmt.Errorf("%s: the verdict names no template version", leg)
	}
	lowered := strings.ToLower(verdict.Asserts)
	for _, approval := range []string{"approved", "verified", "reviewed"} {
		if strings.Contains(lowered, approval) {
			return fmt.Errorf("%s: the asserts statement reads as %s", leg, approval)
		}
	}
	if verdict.Verdict == "unchecked" {
		return fmt.Errorf("%s: the check did not run on a live turn", leg)
	}
	want := expectedDefects(checks, turn.Answer)
	wantVerdict := "conformant"
	if len(want) > 0 {
		wantVerdict = "non_conformant"
	}
	if verdict.Verdict != wantVerdict {
		return fmt.Errorf("%s: deployed verdict %q disagrees with the declared patterns, which say %q for this brief",
			leg, verdict.Verdict, wantVerdict)
	}
	got := append([]string(nil), verdict.Defects...)
	sort.Strings(got)
	expected := append([]string(nil), want...)
	sort.Strings(expected)
	if strings.Join(got, "\n") != strings.Join(expected, "\n") {
		return fmt.Errorf("%s: deployed defects disagree with the declared patterns:\ndeployed: %v\nexpected: %v",
			leg, got, expected)
	}
	return nil
}

// Conformance proves the rel12.2 template-conformance release against the
// deployed mesh: the flagged leg, the pass leg, verdict truth on both, and
// cross-turn determinism on the flagged leg. It skips with the cause named
// when the mesh is absent, matching the other live targets.
func (Integration) Conformance() error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	checks, err := conformanceCheckPatterns(root)
	if err != nil {
		return err
	}
	base := envOrDefault("CHATBOT_UI_BASE", chatbotUIDefaultBase)
	if err := chatbotUIReachable(base); err != nil {
		fmt.Printf("SKIP integration:conformance: %s does not serve the mesh (%v);"+
			" run mage app:up and mage app:seed first\n", base, err)
		return nil
	}

	// The flagged leg (suite cases 2-4): a reply no template could match must
	// earn non_conformant with the recomputed defects, the brief must arrive
	// unmodified in shape (still the bare reply, no injected headings), and
	// the same tasking twice must earn the same verdict (R3.4).
	fmt.Println("integration:conformance: flagged leg")
	flagged, err := runConformanceTurn(base, conformanceFlagTasking)
	if err != nil {
		return err
	}
	if err := assertVerdictTrue(checks, flagged, "flagged leg"); err != nil {
		return err
	}
	if flagged.Metadata.Conformance.Verdict != "non_conformant" {
		return fmt.Errorf("flagged leg: a headingless reply earned verdict %q; the steering failed to produce the flag case",
			flagged.Metadata.Conformance.Verdict)
	}
	if strings.Contains(flagged.Answer, "# Bottom Line Up Front") {
		return fmt.Errorf("flagged leg: the surfaced brief carries template headings the provider was told not to write; something edited the draft")
	}
	time.Sleep(conformancePace)
	fmt.Println("integration:conformance: determinism leg")
	flaggedAgain, err := runConformanceTurn(base, conformanceFlagTasking)
	if err != nil {
		return err
	}
	if err := assertVerdictTrue(checks, flaggedAgain, "determinism leg"); err != nil {
		return err
	}
	if flaggedAgain.Metadata.Conformance.Verdict != flagged.Metadata.Conformance.Verdict {
		return fmt.Errorf("determinism leg: the same tasking earned %q then %q",
			flagged.Metadata.Conformance.Verdict, flaggedAgain.Metadata.Conformance.Verdict)
	}

	// The pass leg (suite case 1): steer composition onto the template and
	// retry, paced, until a brief conforms. Every attempt must still be
	// verdict-true; the retries absorb the provider degrading to the local
	// tier, which composes unheaded prose.
	fmt.Println("integration:conformance: pass leg")
	var passed bool
	for attempt := 1; attempt <= 3; attempt++ {
		time.Sleep(conformancePace)
		turn, err := runConformanceTurn(base, conformancePassTasking)
		if err != nil {
			return err
		}
		if err := assertVerdictTrue(checks, turn, fmt.Sprintf("pass leg attempt %d", attempt)); err != nil {
			return err
		}
		if turn.Metadata.Conformance.Verdict == "conformant" {
			fmt.Printf("integration:conformance: pass leg conformant on attempt %d (answered_by %s)\n",
				attempt, turn.Metadata.AnsweredBy)
			passed = true
			break
		}
		fmt.Printf("integration:conformance: pass leg attempt %d earned %v (answered_by %s); pacing and retrying\n",
			attempt, turn.Metadata.Conformance.Defects, turn.Metadata.AnsweredBy)
	}
	if !passed {
		return fmt.Errorf("pass leg: no steered turn composed a conforming brief in 3 paced attempts;" +
			" every verdict was true of its brief, so the check holds and composition is what missed the template")
	}

	fmt.Println("integration:conformance PASS - a conforming brief passes naming the template version," +
		" a malformed one is flagged with the exact defects the declared patterns yield, the flagged brief" +
		" is surfaced unmodified, and the same tasking earns the same verdict twice")
	return nil
}
