// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// selectorObject is a rendered manifest read for its selector and, for a
// workload, the labels its pods carry.
type selectorObject struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		Selector yaml.Node `yaml:"selector"`
		Template struct {
			Metadata struct {
				Labels map[string]string `yaml:"labels"`
			} `yaml:"metadata"`
		} `yaml:"template"`
	} `yaml:"spec"`
}

// matchLabels reads a Service's flat selector or a workload's matchLabels.
func (object selectorObject) matchLabels(t *testing.T) map[string]string {
	t.Helper()
	var flat map[string]string
	if object.Kind == "Service" {
		if err := object.Spec.Selector.Decode(&flat); err != nil {
			t.Fatalf("%s %s selector: %v", object.Kind, object.Metadata.Name, err)
		}
		return flat
	}
	var structured struct {
		MatchLabels map[string]string `yaml:"matchLabels"`
	}
	if err := object.Spec.Selector.Decode(&structured); err != nil {
		t.Fatalf("%s %s selector: %v", object.Kind, object.Metadata.Name, err)
	}
	return structured.MatchLabels
}

// TestEverySelectorMatchesOneWorkload is GH-736. A rag unit's Chroma pod
// carries the same name, instance, and rag-unit labels as its rag-server, and
// the rag-server Deployment and Service selected on only those, so both
// matched the Chroma pod: kubectl resolved it for logs and port-forwards of
// the rag-server, and only the named target ports kept it out of the
// Service's endpoints. Every Service and workload selector in the rendered
// chart must match the pods of exactly one workload.
func TestEverySelectorMatchesOneWorkload(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm not on PATH")
	}
	chartDir := findChartDir(t)
	staged, cleanup, err := stageSmokeChart(chartDir, filepath.Dir(chartDir))
	if err != nil {
		t.Fatalf("stage chart: %v", err)
	}
	defer cleanup()
	out, err := exec.Command("helm", "template", "rel", staged, "--namespace", "nsy").CombinedOutput()
	if err != nil {
		t.Fatalf("helm template: %v\n%s", err, out)
	}

	var objects []selectorObject
	decoder := yaml.NewDecoder(strings.NewReader(string(out)))
	for {
		var object selectorObject
		if err := decoder.Decode(&object); err != nil {
			break
		}
		objects = append(objects, object)
	}
	type workload struct {
		name   string
		labels map[string]string
	}
	var workloads []workload
	for _, object := range objects {
		switch object.Kind {
		case "Deployment", "StatefulSet", "DaemonSet":
			workloads = append(workloads, workload{object.Kind + "/" + object.Metadata.Name, object.Spec.Template.Metadata.Labels})
		}
	}
	if len(workloads) < 5 {
		t.Fatalf("found %d workloads; the check would pass vacuously", len(workloads))
	}

	checked := 0
	for _, object := range objects {
		switch object.Kind {
		case "Service", "Deployment", "StatefulSet", "DaemonSet":
		default:
			continue
		}
		selector := object.matchLabels(t)
		if len(selector) == 0 {
			continue
		}
		var matched []string
		for _, candidate := range workloads {
			if labelsMatch(selector, candidate.labels) {
				matched = append(matched, candidate.name)
			}
		}
		sort.Strings(matched)
		if len(matched) != 1 {
			t.Errorf("%s/%s selects %v, which matches %d workloads' pods, want exactly one: %v",
				object.Kind, object.Metadata.Name, selector, len(matched), matched)
		}
		checked++
	}
	if checked < 5 {
		t.Fatalf("checked %d selectors; the check would pass vacuously", checked)
	}
}

func labelsMatch(selector, labels map[string]string) bool {
	for key, value := range selector {
		if labels[key] != value {
			return false
		}
	}
	return true
}
