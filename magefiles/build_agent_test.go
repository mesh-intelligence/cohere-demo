// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Two runs on one host building at once must neither share a binary nor
// remove each other's (GH-773): each build gets its own directory, and one
// run's cleanup leaves the other's binary in place.
func TestConcurrentAgentBuildsDoNotShareABinary(t *testing.T) {
	coreRoot := fakeAgentModule(t)
	type build struct {
		binary  string
		cleanup func()
		err     error
	}
	builds := make([]build, 2)
	var wg sync.WaitGroup
	for i := range builds {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			binary, cleanup, err := buildAgent(coreRoot)
			builds[i] = build{binary, cleanup, err}
		}(i)
	}
	wg.Wait()
	for i, b := range builds {
		if b.err != nil {
			t.Fatalf("build %d: %v", i, b.err)
		}
	}
	if builds[0].binary == builds[1].binary {
		t.Fatalf("both builds wrote %s", builds[0].binary)
	}

	builds[0].cleanup()
	if _, err := os.Stat(builds[0].binary); !os.IsNotExist(err) {
		t.Errorf("cleanup left %s behind (err %v)", builds[0].binary, err)
	}
	if _, err := os.Stat(builds[1].binary); err != nil {
		t.Errorf("one run's cleanup removed the other's binary: %v", err)
	}
	builds[1].cleanup()
}

// fakeAgentModule is a module whose ./cmd/agent builds in a moment, standing
// in for an agent-core checkout.
func fakeAgentModule(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/fakeagent\n\ngo 1.22\n")
	write("cmd/agent/main.go", "package main\n\nfunc main() {}\n")
	return root
}
