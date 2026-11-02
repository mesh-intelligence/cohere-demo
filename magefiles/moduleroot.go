// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// The declarative-agents modules this application consumes as a platform. They
// are one monorepo pinned at a single commit in go.mod; go.work repoints them
// at the sibling checkout for development.
const (
	agentCoreModule = "github.com/mesh-intelligence/declarative-agents/agent-core"
	catalogModule   = "github.com/mesh-intelligence/declarative-agents/applications/catalog"
	magefilesModule = "github.com/mesh-intelligence/declarative-agents/magefiles"
)

// moduleResolution is where a dependency actually resolved and what version the
// toolchain called it. With go.work active Dir is the sibling checkout and
// Version reads "(devel)"; with GOWORK=off Dir is the read-only module cache
// copy and Version is the pinned pseudo-version. Both are buildable: `go build`
// writes its output and intermediates outside the module directory, so a
// read-only cache root compiles the agent binary the same as a checkout does.
type moduleResolution struct {
	Path    string
	Dir     string
	Version string
}

// Workspace reports whether the module resolved to a live checkout rather than
// the immutable module cache. A checkout can differ from what go.mod pins,
// which is what makes provenance worth printing.
func (r moduleResolution) Workspace() bool {
	return r.Version == "" || r.Version == "(devel)"
}

// String renders the resolution for provenance output.
func (r moduleResolution) String() string {
	version := r.Version
	if version == "" {
		version = "(devel)"
	}
	return fmt.Sprintf("%s %s", r.Dir, version)
}

var (
	moduleResolutionMu    sync.Mutex
	moduleResolutionCache = map[string]moduleResolution{}
)

// resolveModule asks the Go toolchain where a module resolved, so the magefiles
// locate their platform dependency the same way the compiler does instead of
// guessing at a sibling directory. The answer follows whatever the build is
// already configured to use — the workspace checkout, or the module cache under
// GOWORK=off — which is what keeps `mage` and `go build` from disagreeing.
func resolveModule(applicationRoot, module string) (moduleResolution, error) {
	key := applicationRoot + "\x00" + module
	moduleResolutionMu.Lock()
	defer moduleResolutionMu.Unlock()
	if cached, ok := moduleResolutionCache[key]; ok {
		return cached, nil
	}
	cmd := exec.Command("go", "list", "-m", "-f", "{{.Dir}}\t{{.Version}}", module)
	cmd.Dir = applicationRoot
	out, err := cmd.Output()
	if err != nil {
		return moduleResolution{}, fmt.Errorf(
			"resolve module %s from %s: %w%s", module, applicationRoot, err, commandStderr(err))
	}
	dir, version, _ := strings.Cut(strings.TrimSpace(string(out)), "\t")
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return moduleResolution{}, fmt.Errorf(
			"resolve module %s from %s: the toolchain reported no directory; run `go mod download %s`",
			module, applicationRoot, module)
	}
	resolution := moduleResolution{
		Path: module, Dir: filepath.Clean(dir), Version: strings.TrimSpace(version),
	}
	moduleResolutionCache[key] = resolution
	return resolution, nil
}

// commandStderr surfaces what the failed command printed. exec.ExitError keeps
// it, and without it a resolution failure reports only an exit status.
func commandStderr(err error) string {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || len(exitErr.Stderr) == 0 {
		return ""
	}
	return ": " + strings.TrimSpace(string(exitErr.Stderr))
}

// resolveAgentCore resolves the agent-core module for the callers that must
// report why resolution failed rather than degrade: the self-governance audit
// gate and the demo:up provenance line. Everything else goes through
// demoCoreRoot, which keeps today's fall-back behaviour.
func resolveAgentCore(applicationRoot string) (moduleResolution, error) {
	if coreRoot := configuredCoreRoot(applicationRoot); coreRoot != "" {
		return moduleResolution{Path: agentCoreModule, Dir: coreRoot, Version: "(demo.yaml core_root)"}, nil
	}
	return resolveModule(applicationRoot, agentCoreModule)
}

// pinnedModuleVersion reports the version go.mod pins, ignoring the workspace.
// With go.work active the toolchain answers "(devel)" for a module it resolved
// to a checkout, which hides what the repository actually committed to.
func pinnedModuleVersion(applicationRoot, module string) string {
	cmd := exec.Command("go", "list", "-m", "-f", "{{.Version}}", module)
	cmd.Dir = applicationRoot
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// agentCoreProvenance describes what a run builds against: the resolved
// directory and the lines naming it. A demo is a claim about a specific
// platform version, so the run records which one; and because go.work silently
// prefers a local checkout over the pin, a checkout that has moved away from
// what go.mod pins is called out rather than left to be discovered from
// surprising behaviour.
func agentCoreProvenance(applicationRoot string) (dir string, lines []string, err error) {
	resolution, err := resolveAgentCore(applicationRoot)
	if err != nil {
		return "", nil, err
	}
	lines = []string{fmt.Sprintf("demo: agent-core resolved to %s", resolution)}
	if !resolution.Workspace() {
		return resolution.Dir, lines, nil
	}
	pinned := pseudoVersionRevision(pinnedModuleVersion(applicationRoot, agentCoreModule))
	head := gitCommit(resolution.Dir)
	if pinned == "" || head == "unknown" || strings.HasPrefix(head, pinned) {
		return resolution.Dir, lines, nil
	}
	return resolution.Dir, append(lines, fmt.Sprintf(
		"demo: the workspace checkout is at %s but go.mod pins %s; this run builds the checkout",
		head[:min(len(head), 12)], pinned)), nil
}

// reportAgentCoreProvenance prints the provenance and returns the resolved
// agent-core directory for the caller to build from.
func reportAgentCoreProvenance(applicationRoot string) (string, error) {
	dir, lines, err := agentCoreProvenance(applicationRoot)
	if err != nil {
		return "", err
	}
	for _, line := range lines {
		fmt.Println(line)
	}
	return dir, nil
}

// pseudoVersionRevision returns the commit a Go pseudo-version encodes, or ""
// for a version that carries none. Pseudo-versions end in a timestamp and a
// 12-character revision ("v0.0.0-20260826184552-4ac041645e99"), and that
// trailing field is the only part that names content.
func pseudoVersionRevision(version string) string {
	fields := strings.Split(strings.TrimSpace(version), "-")
	if len(fields) < 2 {
		return ""
	}
	revision := fields[len(fields)-1]
	if !isHex(revision) || len(revision) < 12 {
		return ""
	}
	return revision
}

func isHex(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}

// agentCoreRevision names the agent-core content the runtime image is built
// from. The image ships agent-core, so tagging it with the application's own
// commit labels the image with a revision that says nothing about what is
// inside it: two different agent-core pins would produce the same tag. Under
// the pin the revision comes from the pseudo-version, under go.work from the
// resolved checkout's HEAD.
func agentCoreRevision(applicationRoot string) (string, error) {
	resolution, err := resolveAgentCore(applicationRoot)
	if err != nil {
		return "", err
	}
	if revision := pseudoVersionRevision(resolution.Version); revision != "" {
		return revision, nil
	}
	if revision := gitCommit(resolution.Dir); revision != "unknown" && isHex(revision) {
		return revision, nil
	}
	return "", fmt.Errorf(
		"resolve agent-core revision: %s resolved to %s at version %q, which names no commit; "+
			"the runtime image tag must identify the agent-core content it ships",
		resolution.Path, resolution.Dir, resolution.Version)
}
