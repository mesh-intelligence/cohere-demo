// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mesh-intelligence/declarative-agents/magefiles/kindrig"
)

// These bind the applier's declared helm flags to the helm the applier runs,
// which the pinned CLI donor delivers (GH-739, GH-2222).
//
// The flags are major-version-specific. helm 3 takes --atomic and --dry-run;
// helm 4 deprecates both and spells them --rollback-on-failure and
// --dry-run=client. Neither spelling works on the other major, so the
// declarations and the donor's helm version are one decision recorded in two
// places, and nothing else holds them together.
//
// The failure this prevents is quiet and expensive. --atomic is what rolls a
// failed upgrade back, and apply-machine.yaml routes Applying + ToolFailed
// straight to Failed with no compensating rollback *because* of it. Bump the
// donor to helm 4 and the flag first warns, then eventually goes; the apply
// stops self-rolling-back and that leg leaves the release on a failed revision,
// with every test still green -- integration:applier drives fake CLIs, which
// accept any flags at all.

// helmFlagsByMajor is what each helm major calls the two behaviors the applier
// depends on: rolling a failed upgrade back, and validating without applying.
var helmFlagsByMajor = map[int]struct{ rollback, dryRun string }{
	3: {rollback: "--atomic", dryRun: "--dry-run"},
	4: {rollback: "--rollback-on-failure", dryRun: "--dry-run=client"},
}

// pinnedHelmMajor reads the helm major the applier runs: the version the
// pinned CLI donor carries (GH-2222), which kindrig.VerifyCLIDonor checks inside
// the running pod.
func pinnedHelmMajor(t *testing.T) int {
	t.Helper()
	version := strings.TrimPrefix(kindrig.CLIDonorHelmVersion, "v")
	major, err := strconv.Atoi(strings.SplitN(version, ".", 2)[0])
	if err != nil {
		t.Fatalf("parse helm major from %q: %v", kindrig.CLIDonorHelmVersion, err)
	}
	return major
}

// TestApplierCLIDonorIsTheRigPin holds the chart's default donor to the image
// kindrig pins and verifies. The helm-flag guard reads the version from kindrig,
// so a chart that pinned a different donor would ship a helm the guard never
// checked.
func TestApplierCLIDonorIsTheRigPin(t *testing.T) {
	var values struct {
		Applier struct {
			CLIDonor struct {
				Image struct{ Repository, Tag, Digest string } `yaml:"image"`
			} `yaml:"cliDonor"`
		} `yaml:"applier"`
	}
	readIntakeYAML(t, filepath.Join(findChartDir(t), "values.yaml"), &values)
	image := values.Applier.CLIDonor.Image
	got := fmt.Sprintf("%s:%s@%s", image.Repository, image.Tag, image.Digest)
	if got != kindrig.CLIDonorImage {
		t.Fatalf("applier.cliDonor.image = %s, want the kindrig pin %s", got, kindrig.CLIDonorImage)
	}
}

// TestApplierHelmFlagsMatchTheShippedHelm proves the declared flags are the ones
// the pinned helm actually takes, in both directions: the right spelling present
// and the other major's absent.
func TestApplierHelmFlagsMatchTheShippedHelm(t *testing.T) {
	major := pinnedHelmMajor(t)
	want, known := helmFlagsByMajor[major]
	if !known {
		t.Fatalf("the CLI donor pins helm %d, whose flag spellings this guard does not know; "+
			"decide what it calls the self-rollback and the dry-run, and add it to helmFlagsByMajor", major)
	}

	var decls execDeclarations
	readIntakeYAML(t, filepath.Join(agentDir(t, "applier"), "exec-declarations.yaml"), &decls)
	args := map[string][]string{}
	for _, tool := range decls.Tools {
		args[tool.Name] = tool.Args
	}

	upgrade, ok := args["helm_upgrade"]
	if !ok {
		t.Fatal("the applier declares no helm_upgrade word")
	}
	if !containsString(upgrade, want.rollback) {
		t.Errorf("helm_upgrade does not pass %s, which helm %d calls the self-rollback; "+
			"apply-machine.yaml routes a failed apply straight to Failed because the upgrade rolls itself back, "+
			"so without it a failed apply leaves the release on the failed revision",
			want.rollback, major)
	}
	dryRun, ok := args["helm_dry_run"]
	if !ok {
		t.Fatal("the applier declares no helm_dry_run word")
	}
	if !containsString(dryRun, want.dryRun) {
		t.Errorf("helm_dry_run does not pass %s, which helm %d calls the validate-without-applying flag",
			want.dryRun, major)
	}

	// The other major's spellings must not appear: helm rejects an unknown flag
	// outright, so a half-migrated declaration fails every apply on the cluster
	// while the fake-CLI tracer stays green.
	for otherMajor, other := range helmFlagsByMajor {
		if otherMajor == major {
			continue
		}
		for word, declared := range map[string][]string{"helm_upgrade": upgrade, "helm_dry_run": dryRun} {
			for _, flag := range []string{other.rollback, other.dryRun} {
				if flag == want.rollback || flag == want.dryRun {
					continue // a spelling both majors share is not evidence of drift
				}
				if containsString(declared, flag) {
					t.Errorf("%s passes %s, which is helm %d's spelling, but the image ships helm %d",
						word, flag, otherMajor, major)
				}
			}
		}
	}
}

// TestApplierHelmFlagGuardCoversTheDeclaredWords proves the guard reads the
// words it claims to. A guard that silently matched nothing would pass forever.
func TestApplierHelmFlagGuardCoversTheDeclaredWords(t *testing.T) {
	var decls execDeclarations
	readIntakeYAML(t, filepath.Join(agentDir(t, "applier"), "exec-declarations.yaml"), &decls)
	var helmWords int
	for _, tool := range decls.Tools {
		if tool.Binary == "helm" {
			helmWords++
		}
	}
	if helmWords < 3 {
		t.Errorf("the applier declares %d helm words; the guard expects at least the dry-run, the upgrade, "+
			"and the rollback, so a word may have been dropped or renamed", helmWords)
	}
	// helm_rollback runs no version-specific flag today; if it grows one, this
	// guard has to learn about it rather than let it drift.
	for _, tool := range decls.Tools {
		if tool.Name != "helm_rollback" {
			continue
		}
		for _, arg := range tool.Args {
			if strings.HasPrefix(arg, "--") {
				t.Errorf("helm_rollback now passes %s; add it to the version guard if its spelling is major-specific", arg)
			}
		}
	}
}
