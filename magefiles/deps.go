// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/magefile/mage/mg"
	"github.com/mesh-intelligence/declarative-agents/magefiles/reusedeps"
)

// Deps groups the targets that move this application's platform dependency.
type Deps mg.Namespace

// declarativeAgentsModules are pinned together. They are one repository, and a
// build that mixes commits across them compiles declarations from one revision
// against a runtime from another.
var declarativeAgentsModules = reusedeps.PinnedModules

const declarativeAgentsRepository = reusedeps.Repository

// Verify holds this repository to one declarative-agents release: the three
// pins name one commit, a release tag names that commit, and every chart
// values file sets the published image's tag to it.
func (Deps) Verify() error {
	return reusedeps.Verify()
}

// Doctor reports whether this machine resolves the declarative-agents modules
// privately: GOPRIVATE covers the organization and git can list the
// repository. Each failure names the command or credential that fixes it.
func (Deps) Doctor() error {
	return reusedeps.Doctor()
}

// Pin repoints the declarative-agents modules at one upstream release or
// commit. Upstream tags releases at the repository root, and Go does not
// resolve a root tag for a module in a subdirectory, so a release name cannot
// be written into go.mod directly: this target resolves the name to a commit,
// pins all three modules at it, and records the release it came from in a
// go.mod comment so the file still says which release is in the build.
//
//	mage deps:pin v0.20260826.0
//	mage deps:pin 4ac04164
func (Deps) Pin(ref string) (err error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return fmt.Errorf("deps:pin: a release tag or commit is required, for example `mage deps:pin v0.20260826.0`")
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	// A pin that fails part way leaves go.mod at mixed or downgraded versions
	// the caller would have to repair by hand, so any failure puts the module
	// files back as they were (GH-744).
	restore, err := snapshotModuleFiles(root)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			if restoreErr := restore(); restoreErr != nil {
				err = fmt.Errorf("%w; restoring go.mod and go.sum also failed: %v", err, restoreErr)
			}
		}
	}()
	commit, err := resolveUpstreamRef(root, ref)
	if err != nil {
		return err
	}
	fmt.Printf("deps:pin: %s resolves to %s\n", ref, commit)
	for _, module := range declarativeAgentsModules {
		fmt.Printf("deps:pin: pinning %s\n", module)
		if err := goGet(root, module+"@"+commit); err != nil {
			return fmt.Errorf("deps:pin: pin %s at %s: %w", module, commit, err)
		}
	}
	pinned, err := pinnedRevisions(root)
	if err != nil {
		return err
	}
	if err := assertOneCommit(pinned); err != nil {
		return err
	}
	if err := recordPinnedRelease(filepath.Join(root, "go.mod"), ref, commit); err != nil {
		return err
	}
	fmt.Printf("deps:pin: all three modules pinned at %s (%s)\n", ref, commit)
	return nil
}

// snapshotModuleFiles records go.mod and go.sum and returns a function that
// writes them back.
func snapshotModuleFiles(root string) (func() error, error) {
	names := []string{"go.mod", "go.sum"}
	saved := make(map[string][]byte, len(names))
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			return nil, fmt.Errorf("deps:pin: snapshot %s: %w", name, err)
		}
		saved[name] = data
	}
	return func() error {
		for _, name := range names {
			if err := os.WriteFile(filepath.Join(root, name), saved[name], 0o644); err != nil {
				return err
			}
		}
		return nil
	}, nil
}

// resolveUpstreamRef turns a release tag or a commit into a full commit. The
// resolved checkout answers when the workspace has one; otherwise upstream is
// asked directly, so pinning works from a clone that has never had the
// sibling checkout. ls-remote lists refs and so answers for a tag or branch;
// a commit, short or full, is resolved by the Go toolchain itself, which is
// what go get will do with it next (GH-744).
func resolveUpstreamRef(applicationRoot, ref string) (string, error) {
	if resolution, err := resolveModule(applicationRoot, agentCoreModule); err == nil {
		if commit := revParse(resolution.Dir, ref); commit != "" {
			return commit, nil
		}
	}
	commit, err := lsRemote(ref)
	if err == nil {
		return commit, nil
	}
	if resolved, listErr := moduleCommit(applicationRoot, ref); listErr == nil {
		return resolved, nil
	}
	return "", fmt.Errorf("deps:pin: resolve %q against %s: %w; name a release tag or a commit upstream has",
		ref, declarativeAgentsRepository, err)
}

// moduleCommit asks the Go toolchain which upstream commit ref names, through
// agent-core's module path: the answer's origin carries the full hash.
func moduleCommit(root, ref string) (string, error) {
	cmd := exec.Command("go", "list", "-m", "-json", agentCoreModule+"@"+ref)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	var module struct {
		Origin struct {
			Hash string `json:"Hash"`
		} `json:"Origin"`
	}
	if err := json.Unmarshal(out, &module); err != nil {
		return "", err
	}
	if module.Origin.Hash == "" {
		return "", fmt.Errorf("go list reported no commit for %s", ref)
	}
	return module.Origin.Hash, nil
}

func revParse(dir, ref string) string {
	cmd := exec.Command("git", "rev-parse", ref+"^{commit}")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func lsRemote(ref string) (string, error) {
	out, err := exec.Command(
		"git", "ls-remote", declarativeAgentsRepository, ref, ref+"^{}").Output()
	if err != nil {
		return "", err
	}
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	var commit string
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		// An annotated tag lists both the tag object and, as "<ref>^{}", the
		// commit it points at. The dereferenced line is the one to pin.
		commit = fields[0]
		if strings.HasSuffix(fields[1], "^{}") {
			return commit, nil
		}
	}
	if commit == "" {
		return "", fmt.Errorf("no such ref")
	}
	return commit, nil
}

func goGet(root, target string) error {
	cmd := exec.Command("go", "get", target)
	cmd.Dir = root
	// go get resolves against the module graph, not the workspace; leaving
	// GOWORK on makes it a no-op against the local checkout.
	cmd.Env = append(os.Environ(), "GOWORK=off")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// pinnedRevisions reads back what go.mod now requires for each module.
func pinnedRevisions(root string) (map[string]string, error) {
	pinned := make(map[string]string, len(declarativeAgentsModules))
	for _, module := range declarativeAgentsModules {
		version := pinnedModuleVersion(root, module)
		if version == "" {
			return nil, fmt.Errorf("deps:pin: %s has no recorded version after pinning", module)
		}
		revision := pseudoVersionRevision(version)
		if revision == "" {
			return nil, fmt.Errorf(
				"deps:pin: %s is at %q, which names no commit; expected a pseudo-version", module, version)
		}
		pinned[module] = revision
	}
	return pinned, nil
}

// assertOneCommit refuses a split pin. Three modules from one repository at
// three commits is the state this target exists to prevent, and it is invisible
// in go.mod without comparing the pseudo-version revisions by eye.
func assertOneCommit(pinned map[string]string) error {
	var first, firstModule string
	for _, module := range declarativeAgentsModules {
		revision := pinned[module]
		if first == "" {
			first, firstModule = revision, module
			continue
		}
		if revision != first {
			return fmt.Errorf(
				"deps:pin: modules landed at different commits — %s at %s, %s at %s; "+
					"go.mod is left as written, re-run with a single ref",
				firstModule, first, module, revision)
		}
	}
	return nil
}

// pinnedReleaseRE reads the release comment recordPinnedRelease writes above
// the require block: "declarative-agents <tag> (<commit>)".
var pinnedReleaseRE = regexp.MustCompile(`declarative-agents (v\S+) \(([0-9a-f]{12,40})\)`)

// pinnedDeclarativeAgentsRelease returns the release tag and commit go.mod pins.
func pinnedDeclarativeAgentsRelease(applicationRoot string) (tag, commit string, err error) {
	data, err := os.ReadFile(filepath.Join(applicationRoot, "go.mod"))
	if err != nil {
		return "", "", err
	}
	match := pinnedReleaseRE.FindStringSubmatch(string(data))
	if match == nil {
		return "", "", fmt.Errorf("go.mod records no declarative-agents release comment; run mage deps:pin")
	}
	return match[1], match[2], nil
}

// recordPinnedRelease rewrites the release comment above the require block, so
// a reader of go.mod sees the release name rather than three pseudo-versions.
func recordPinnedRelease(goModPath, ref, commit string) error {
	source, err := os.ReadFile(goModPath)
	if err != nil {
		return fmt.Errorf("deps:pin: read %s: %w", goModPath, err)
	}
	short := commit
	if len(short) > 12 {
		short = short[:12]
	}
	marker := "// declarative-agents "
	replacement := fmt.Sprintf("%s%s (%s)", marker, ref, short)
	lines := strings.Split(string(source), "\n")
	for index, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), marker) {
			// The comment continues past the release onto the kit it ships
			// with; only the release and commit are this target's to change.
			if _, rest, found := strings.Cut(line, ")"); found {
				replacement += rest
			}
			lines[index] = replacement
			return os.WriteFile(goModPath, []byte(strings.Join(lines, "\n")), 0o644)
		}
	}
	return fmt.Errorf(
		"deps:pin: no %q comment found in %s; the pins moved but the release name was not recorded",
		strings.TrimSpace(marker), goModPath)
}
