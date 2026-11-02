// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestPseudoVersionRevisionReadsTheCommit covers what the image tag depends on:
// a pseudo-version names a commit and a release version does not, so only the
// first can identify the agent-core content an image ships.
func TestPseudoVersionRevisionReadsTheCommit(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		version string
		want    string
	}{
		{"tagged pseudo-version", "v0.20260803.1-0.20260826184552-4ac041645e99", "4ac041645e99"},
		{"untagged pseudo-version", "v0.0.0-20260826184552-4ac041645e99", "4ac041645e99"},
		{"release version names no commit", "v0.20260826.0", ""},
		{"workspace version names no commit", "(devel)", ""},
		{"empty", "", ""},
		{"trailing field is not hexadecimal", "v0.0.0-20260826184552-zzzzzzzzzzzz", ""},
		{"trailing field is too short", "v0.0.0-20260826184552-4ac0416", ""},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := pseudoVersionRevision(testCase.version); got != testCase.want {
				t.Errorf("pseudoVersionRevision(%q) = %q, want %q", testCase.version, got, testCase.want)
			}
		})
	}
}

// TestAssertOneCommitRejectsASplitPin proves the guard the deps:pin target
// exists for: three modules from one repository at three commits compiles
// declarations from one revision against a runtime from another, and go.mod
// shows it only as three unequal pseudo-versions.
func TestAssertOneCommitRejectsASplitPin(t *testing.T) {
	same := map[string]string{}
	for _, module := range declarativeAgentsModules {
		same[module] = "4ac041645e99"
	}
	if err := assertOneCommit(same); err != nil {
		t.Fatalf("assertOneCommit on one commit = %v, want nil", err)
	}

	split := map[string]string{}
	for _, module := range declarativeAgentsModules {
		split[module] = "4ac041645e99"
	}
	split[catalogModule] = "eb6c09f4aaaa"
	err := assertOneCommit(split)
	if err == nil {
		t.Fatal("assertOneCommit on mixed commits = nil, want an error")
	}
	for _, want := range []string{"4ac041645e99", "eb6c09f4aaaa", catalogModule} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q; a split pin must say which modules disagree", err, want)
		}
	}
}

// TestRecordPinnedReleaseRewritesTheReleaseComment proves go.mod keeps naming a
// release after a pin. Without it the file carries three pseudo-versions and no
// reader can tell which upstream release is in the build.
func TestRecordPinnedReleaseRewritesTheReleaseComment(t *testing.T) {
	goMod := filepath.Join(t.TempDir(), "go.mod")
	original := "module example.com/demo\n\n" +
		"// declarative-agents v0.20260820.0 (eb6c09f4aaaa); the shipped UIs take\n" +
		"// @mesh-intelligence/ui-kit published with the same release\nrequire (\n\tfoo v1.0.0\n)\n"
	if err := os.WriteFile(goMod, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := recordPinnedRelease(goMod, "v0.20260826.0", "4ac041645e999769cc2f"); err != nil {
		t.Fatalf("recordPinnedRelease: %v", err)
	}
	updated, err := os.ReadFile(goMod)
	if err != nil {
		t.Fatal(err)
	}
	want := "// declarative-agents v0.20260826.0 (4ac041645e99); the shipped UIs take\n"
	if !strings.Contains(string(updated), want) {
		t.Errorf("go.mod does not carry %q after pinning:\n%s", want, updated)
	}
	if strings.Contains(string(updated), "v0.20260820.0") {
		t.Errorf("the superseded release name survived the rewrite:\n%s", updated)
	}
	if !strings.Contains(string(updated), "require (") {
		t.Errorf("the rewrite dropped the require block:\n%s", updated)
	}
}

// TestRecordPinnedReleaseRequiresTheComment proves a pin cannot silently leave
// go.mod without a release name: moving the versions and losing the comment is
// the state that makes the file unreadable.
func TestRecordPinnedReleaseRequiresTheComment(t *testing.T) {
	goMod := filepath.Join(t.TempDir(), "go.mod")
	if err := os.WriteFile(goMod, []byte("module example.com/demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := recordPinnedRelease(goMod, "v0.20260826.0", "4ac041645e99"); err == nil {
		t.Fatal("recordPinnedRelease with no release comment = nil, want an error")
	}
}

// TestResolveModuleFindsTheDeclarativeAgentsModules proves the magefiles locate
// their platform dependency the same way the compiler does. The directory must
// exist and hold a go.mod whichever way the build resolved it — the workspace
// checkout or the module cache.
func TestResolveModuleFindsTheDeclarativeAgentsModules(t *testing.T) {
	applicationRoot := filepath.Clean("..")
	for _, module := range declarativeAgentsModules {
		resolution, err := resolveModule(applicationRoot, module)
		if err != nil {
			t.Fatalf("resolveModule(%s): %v", module, err)
		}
		if resolution.Dir == "" {
			t.Fatalf("resolveModule(%s) returned no directory", module)
		}
		if _, err := os.Stat(filepath.Join(resolution.Dir, "go.mod")); err != nil {
			t.Errorf("resolved %s to %s, which has no go.mod: %v", module, resolution.Dir, err)
		}
	}
}

// TestDemoCoreRootResolvesThroughTheToolchain proves the agent-core root the
// mage targets build from is the module the toolchain resolved, not a directory
// guessed from the sibling layout. agentCoreAvailable is the check every caller
// makes, so the resolved root has to satisfy it.
func TestDemoCoreRootResolvesThroughTheToolchain(t *testing.T) {
	applicationRoot := filepath.Clean("..")
	coreRoot := demoCoreRoot(applicationRoot)
	if !agentCoreAvailable(coreRoot) {
		t.Fatalf("demoCoreRoot resolved to %s, which buildAgent cannot compile from", coreRoot)
	}
	resolution, err := resolveModule(applicationRoot, agentCoreModule)
	if err != nil {
		t.Fatalf("resolveModule: %v", err)
	}
	if configuredCoreRoot(applicationRoot) == "" && coreRoot != resolution.Dir {
		t.Errorf("demoCoreRoot = %s, want the toolchain-resolved %s", coreRoot, resolution.Dir)
	}
}

// TestCachedAgentCoreBuildsWithoutASibling proves the pinned agent-core builds
// inside its module-cache copy with no sibling directory, the clean-machine
// case declarative-agents #103 fixed; the side-by-side staging this repository
// carried until then is gone (GH-802).
func TestCachedAgentCoreBuildsWithoutASibling(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the agent")
	}
	resolution, err := resolveModule(filepath.Clean(".."), agentCoreModule)
	if err != nil {
		t.Fatalf("resolveModule: %v", err)
	}
	if resolution.Workspace() {
		t.Skip("agent-core resolved to a workspace checkout")
	}
	build := exec.Command("go", "build", "-o", filepath.Join(t.TempDir(), "agent"), "./cmd/agent")
	build.Dir = resolution.Dir
	build.Env = append(os.Environ(), "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build ./cmd/agent in %s: %v\n%s", resolution.Dir, err, out)
	}
}

// TestConfiguredCoreRootIsUnsetByDefault holds the shipped configuration to
// toolchain resolution. A committed core_root would pin every developer to one
// checkout layout again, which is what GH-110 removed.
func TestConfiguredCoreRootIsUnsetByDefault(t *testing.T) {
	if override := configuredCoreRoot(filepath.Clean("..")); override != "" {
		t.Errorf("demo.yaml sets core_root to %q; it ships unset so the toolchain resolves it", override)
	}
}

// TestAgentCoreRevisionNamesTheDependencyNotTheApplication is the point of the
// image-identity change: the runtime image ships agent-core, so its tag has to
// move when agent-core moves and stay put when only this repository moves.
func TestAgentCoreRevisionNamesTheDependencyNotTheApplication(t *testing.T) {
	applicationRoot := filepath.Clean("..")
	revision, err := agentCoreRevision(applicationRoot)
	if err != nil {
		t.Fatalf("agentCoreRevision: %v", err)
	}
	if len(revision) < 12 || !isHex(revision) {
		t.Fatalf("agentCoreRevision = %q, want at least 12 hexadecimal characters", revision)
	}
	if applicationCommit := gitCommit(applicationRoot); applicationCommit != "unknown" &&
		strings.EqualFold(revision, applicationCommit) {
		t.Errorf(
			"agentCoreRevision = %s, the application's own commit; the tag must identify agent-core content",
			revision)
	}
}

// TestAgentCoreProvenanceNamesWhatTheRunBuilds proves a demo run records its
// platform version. Without it the run is a claim about "the demo" with nothing
// saying which agent-core produced it.
func TestAgentCoreProvenanceNamesWhatTheRunBuilds(t *testing.T) {
	applicationRoot := filepath.Clean("..")
	dir, lines, err := agentCoreProvenance(applicationRoot)
	if err != nil {
		t.Fatalf("agentCoreProvenance: %v", err)
	}
	if len(lines) == 0 {
		t.Fatal("agentCoreProvenance produced no output; a demo run must record what it built against")
	}
	if !strings.Contains(lines[0], dir) {
		t.Errorf("provenance %q does not name the resolved directory %q", lines[0], dir)
	}
	resolution, err := resolveAgentCore(applicationRoot)
	if err != nil {
		t.Fatal(err)
	}
	version := resolution.Version
	if version == "" {
		version = "(devel)"
	}
	if !strings.Contains(lines[0], version) {
		t.Errorf("provenance %q does not name the version %q", lines[0], version)
	}
}

// TestAgentCoreProvenanceFlagsAPinMismatch covers the case go.work makes easy to
// hit and hard to notice: the workspace checkout has moved away from the commit
// go.mod pins, so the run builds something other than what the repository
// records. The mismatch has to be stated, not inferred from odd behaviour.
func TestAgentCoreProvenanceFlagsAPinMismatch(t *testing.T) {
	applicationRoot := filepath.Clean("..")
	resolution, err := resolveAgentCore(applicationRoot)
	if err != nil {
		t.Fatalf("resolveAgentCore: %v", err)
	}
	if !resolution.Workspace() {
		t.Skip("agent-core resolved to the module cache, where no checkout can drift from the pin")
	}
	head := gitCommit(resolution.Dir)
	pinned := pseudoVersionRevision(pinnedModuleVersion(applicationRoot, agentCoreModule))
	if head == "unknown" || pinned == "" {
		t.Skip("no checkout HEAD or no pinned pseudo-version to compare")
	}
	_, lines, err := agentCoreProvenance(applicationRoot)
	if err != nil {
		t.Fatal(err)
	}
	mismatched := !strings.HasPrefix(head, pinned)
	reported := len(lines) > 1 && strings.Contains(strings.Join(lines, "\n"), "go.mod pins")
	if mismatched != reported {
		t.Errorf(
			"checkout %s vs pin %s: mismatch=%v but provenance reported=%v\n%s",
			head[:12], pinned, mismatched, reported, strings.Join(lines, "\n"))
	}
}

// TestStagedCopiesAreWritableFromAReadOnlySource covers the pin's sharpest
// edge: the module cache is read-only, staging copies permissions along with
// content, and a staged tree that inherits read-only files fails the later
// staging steps that rewrite them. The source must stay untouched.
func TestStagedCopiesAreWritableFromAReadOnlySource(t *testing.T) {
	source := t.TempDir()
	nested := filepath.Join(source, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	readOnly := filepath.Join(nested, "declarations.yaml")
	if err := os.WriteFile(readOnly, []byte("tools: []\n"), 0o444); err != nil {
		t.Fatal(err)
	}

	destination := filepath.Join(t.TempDir(), "staged")
	if err := copyDirContents(source, destination); err != nil {
		t.Fatalf("copyDirContents: %v", err)
	}
	staged := filepath.Join(destination, "nested", "declarations.yaml")
	if err := os.WriteFile(staged, []byte("tools: [rewritten]\n"), 0o644); err != nil {
		t.Fatalf("staged copy is not writable: %v", err)
	}
	info, err := os.Stat(readOnly)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o200 != 0 {
		t.Errorf("source %s became writable; staging must not modify what it copies from", readOnly)
	}
}

// TestInventoryFileStagesWritableFromAReadOnlySource is the same guarantee for
// the per-file staging path the composition manifest uses.
func TestInventoryFileStagesWritableFromAReadOnlySource(t *testing.T) {
	source := filepath.Join(t.TempDir(), "profile.yaml")
	content := []byte("name: corpus-ingest\n")
	if err := os.WriteFile(source, content, 0o444); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	destination := filepath.Join(t.TempDir(), "staged", "profile.yaml")
	if err := copyChatbotInventoryFile(
		source, destination, fmt.Sprintf("sha256:%x", sum)); err != nil {
		t.Fatalf("copyChatbotInventoryFile: %v", err)
	}
	if err := os.WriteFile(destination, []byte("name: rewritten\n"), 0o644); err != nil {
		t.Fatalf("staged inventory file is not writable: %v", err)
	}
}

// TestChatbotIntegrationImagesCarryTheAgentCoreRevision closes the loop from the
// resolved dependency to the tag the cluster pulls.
func TestChatbotIntegrationImagesCarryTheAgentCoreRevision(t *testing.T) {
	applicationRoot := filepath.Clean("..")
	images, err := resolveChatbotIntegrationImages(applicationRoot)
	if err != nil {
		t.Fatalf("resolveChatbotIntegrationImages: %v", err)
	}
	revision, err := agentCoreRevision(applicationRoot)
	if err != nil {
		t.Fatalf("agentCoreRevision: %v", err)
	}
	want := strings.ToLower(revision[:12])
	if images.Revision != want {
		t.Errorf("image revision = %q, want the agent-core revision %q", images.Revision, want)
	}
	// The runtime reference is upstream's typed identity
	// (localhost/declarative-agents/runtime/agent-core:git-<rev>-linux-<arch>,
	// pre-fork upstream GH-2506), so the revision sits inside the typed tag.
	for _, image := range []string{images.Runtime} {
		if !strings.Contains(image, ":git-"+want+"-linux-") {
			t.Errorf("image %q does not carry the typed agent-core revision tag git-%s-linux-<arch>", image, want)
		}
	}
}
