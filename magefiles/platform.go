// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/magefile/mage/mg"
	"github.com/mesh-intelligence/declarative-agents/magefiles/kindrig"
	"github.com/mesh-intelligence/declarative-agents/magefiles/reusedeps"
)

// Platform groups the targets that manage the shared da-platform cluster the
// namespaced integration scenarios run on (GH-756).
type Platform mg.Namespace

// Up brings up a persistent da-platform and leaves it running, or re-proves a
// healthy one already listed. A platform no integration run created is one no
// integration run deletes, so concurrent runs from several applications can
// share it without one ending the others' scenarios (GH-761).
func (Platform) Up() error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	cluster, err := kindrig.UpPlatform(kindrig.PlatformOptions{
		Version: platformVersion(),
		EvidenceDirectory: filepath.Join(root, "build", "kind-evidence",
			platformCluster()+"-up-"+time.Now().UTC().Format("20060102T150405Z")),
	})
	if err != nil {
		return err
	}
	fmt.Printf("platform: %s is up and conformant; integration runs reuse it without deleting it\n",
		cluster.Name)
	return nil
}

// Down deletes da-platform and no other cluster.
func (Platform) Down() error {
	if foreign := platformForeignNamespaces(platformCluster()); len(foreign) > 0 {
		return fmt.Errorf("platform:down: %s still holds scenario namespaces %v from another application;"+
			" wait for that run to finish", platformCluster(), foreign)
	}
	return kindrig.DownPlatform(kindrig.DefaultRun, kindrig.PlatformCommandBinding, platformVersion())
}

// platformVersion is the declarative-agents release this repository's platform
// is built from: the release go.mod records, else the pinned revision. kindrig
// runs from the module cache here, so its own default, the version of the
// checkout in the working directory, would name this repository's revision
// instead of the platform's (upstream GH-43).
var platformVersion = sync.OnceValue(func() string {
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err == nil {
		if content, readErr := os.ReadFile(strings.TrimSpace(string(out))); readErr == nil {
			pins, recorded := reusedeps.ReadPins(content)
			if recorded != "" {
				return recorded
			}
			if len(pins) > 0 {
				return pins[0].Revision
			}
		}
	}
	version, _ := kindrig.PlatformVersion(".")
	return version
})

// platformCluster names the da-platform cluster of platformVersion.
func platformCluster() string {
	return kindrig.PlatformClusterName(platformVersion())
}
