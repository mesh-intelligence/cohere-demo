// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/magefile/mage/mg"
	"github.com/mesh-intelligence/cobbler-scaffold/pkg/orchestrator"
)

// Cobbler groups the measure and stitch targets.
type Cobbler mg.Namespace

// Generator groups the code-generation trail lifecycle targets.
type Generator mg.Namespace

// Scaffold groups the scaffold install/uninstall targets.
type Scaffold mg.Namespace

// Prompt groups prompt preview targets.
type Prompt mg.Namespace

// Metrics groups the orchestrator's pipeline stats targets (LOC, tokens,
// outcomes, releases, generation runs). It is named Metrics rather than Stats
// because the vendored chatbot-mesh magefiles already define a top-level
// Stats target (magefiles/stats.go) that reports agent ownership; the two
// share one package main, so the identifiers must not collide.
type Metrics mg.Namespace

// Tests: run directly with go test:
//
//	go test -tags=usecase -v -count=1 -timeout 1800s ./tests/rel01.0/...          # all
//	go test -tags=usecase -v ./tests/rel01.0/uc001/                               # one UC
//	go test -tags=usecase -bench=. -benchtime=1x -run=^$ ./tests/rel01.0/uc008/   # benchmarks

// baseCfg holds the configuration loaded from configuration.yaml.
var baseCfg orchestrator.Config

func init() {
	if _, err := os.Stat(orchestrator.DefaultConfigFile); errors.Is(err, os.ErrNotExist) {
		if err := orchestrator.WriteDefaultConfig(orchestrator.DefaultConfigFile); err != nil {
			panic(fmt.Sprintf("creating %s: %v", orchestrator.DefaultConfigFile, err))
		}
		fmt.Fprintf(os.Stderr, "created default %s\n", orchestrator.DefaultConfigFile)
	}
	var err error
	baseCfg, err = orchestrator.LoadConfig(orchestrator.DefaultConfigFile)
	if err != nil {
		panic(fmt.Sprintf("loading %s: %v", orchestrator.DefaultConfigFile, err))
	}
}

// newOrch creates an Orchestrator from the base config.
func newOrch() *orchestrator.Orchestrator {
	return orchestrator.New(baseCfg)
}

// logf prints a timestamped log line to stderr.
func logf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(os.Stderr, "[%s] %s\n", time.Now().Format(time.RFC3339), msg)
}

// --- Top-level targets ---
//
// Build, Install, and Tag operate on a compiled binary. cohere-demo has no
// main_package, so the orchestrator skips them (Build/Install no-op, BuildAll
// finds no cmd/); they are retained for parity with the scaffold and stay
// inert until a binary target exists.

// Init initializes the project.
func Init() error { return newOrch().Generator.Init() }

// Reset performs a full reset: cobbler and generator.
func Reset() error { return newOrch().Generator.FullReset() }

// Build compiles the project binary.
func Build() error { return newOrch().Builder.Build() }

// Lint runs golangci-lint on the project, against a cache scoped to this
// checkout. See lintWithScopedCache for why it does not delegate to the
// orchestrator's Builder.Lint.
func Lint() error { return lintWithScopedCache() }

// Install runs go install for the main package.
func Install() error { return newOrch().Builder.Install() }

// Clean removes build artifacts.
func Clean() error { return newOrch().Builder.Clean() }

// Credentials extracts Claude credentials from the macOS Keychain.
func Credentials() error { return newOrch().Builder.ExtractCredentials() }

// The orchestrator's cross-artifact analyzer is deliberately not exposed as a
// mage target. This application's specification gate is `mage audit`, which
// runs the critic declarative agent against the schema its SRDs are authored
// to; the orchestrator's analyzer models a narrower SRD struct and reports
// fields that schema uses as errors. It still runs as the pre-cycle analysis
// inside cobbler:measure, where it feeds the measure prompt rather than
// gating the corpus (GH-56).

// Tag creates a documentation release tag (v0.YYYYMMDD.N) and builds the container image.
func Tag() error { return newOrch().Releaser.Tag() }

// --- Scaffold targets ---

// Pop removes orchestrator-managed files from the target repository:
// magefiles/orchestrator.go, docs/constitutions/, docs/prompts/, and
// configuration.yaml. Pass "." for the current directory.
func (Scaffold) Pop(target string) error { return newOrch().Scaffolder.Uninstall(target) }

// --- Cobbler targets ---

// Measure assesses project state and proposes new tasks via Claude.
func (Cobbler) Measure() error { return newOrch().Measure.Measure() }

// Stitch picks ready tasks and invokes Claude to execute them.
func (Cobbler) Stitch() error { return newOrch().Stitch.Stitch() }

// Reset removes the cobbler scratch directory.
func (Cobbler) Reset() error { return newOrch().ClaudeRunner.CobblerReset() }

// --- Generator targets ---

// Start begins a new generation trail.
func (Generator) Start() error { return newOrch().Generator.GeneratorStart() }

// Run executes measure + stitch cycles using the generation.cycles value in configuration.yaml.
// Use RunN to override the cycle count for a single invocation.
func (Generator) Run() error { return newOrch().Generator.GeneratorRun(0) }

// RunN executes exactly n cycles of measure + stitch within the current generation.
// Pass n > 0 to override generation.cycles in configuration.yaml for this run only.
func (Generator) RunN(n int) error { return newOrch().Generator.GeneratorRun(n) }

// Resume recovers from an interrupted run and continues.
func (Generator) Resume() error { return newOrch().Generator.GeneratorResume() }

// Stop completes a generation trail and merges it into main.
func (Generator) Stop() error { return newOrch().Generator.GeneratorStop() }

// List shows active branches and past generations.
func (Generator) List() error { return newOrch().Generator.GeneratorList() }

// Switch commits current work and checks out another generation branch.
func (Generator) Switch() error { return newOrch().Generator.GeneratorSwitch() }

// Reset destroys generation branches, worktrees, and Go source directories.
func (Generator) Reset() error { return newOrch().Generator.GeneratorReset() }

// --- Metrics targets ---

// Loc prints Go lines of code and documentation word counts.
func (Metrics) Loc() error { return newOrch().Stats.PrintStats() }

// Tokens enumerates prompt-attached files and counts tokens via the Anthropic API.
func (Metrics) Tokens() error { return newOrch().TokenStats() }

// Outcomes prints a summary table of task outcome trailers from git history.
func (Metrics) Outcomes() error { return newOrch().Stats.Outcomes() }

// Generator prints a live status report for the current generation run.
func (Metrics) Generator() error { return newOrch().Stats.GeneratorStats() }

// Releases prints a table of roadmap releases with SRD and requirement counts.
func (Metrics) Releases() error { return newOrch().Stats.ReleaseStats() }

// Run prints aggregate statistics for a completed generation run.
func (Metrics) Run(name string) error { return newOrch().Stats.RunStats(name) }

// Compare prints a side-by-side comparison of two generation runs.
func (Metrics) Compare(name1, name2 string) error {
	return newOrch().Stats.CompareRunStats(name1, name2)
}

// --- Prompt targets ---

// Measure prints the assembled measure prompt to stdout.
func (Prompt) Measure() error { return newOrch().DumpMeasurePrompt() }

// Stitch prints the assembled stitch prompt to stdout.
func (Prompt) Stitch() error { return newOrch().DumpStitchPrompt() }
