// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// The application runs one release named cohereApplicationRelease of the
// chatbot-mesh chart in cohereApplicationNamespace on the shared da-platform, so
// every workload is prefixed "<release>-<chart>-<unit>" (for example
// cohere-demo-chatbot-mesh-rag0). Deriving names this way, and the unit list
// from the chart values, keeps app:seed correct when a second RAG unit lands
// rather than hardcoding rag0.
const demoChartName = "chatbot-mesh"

// applicationKubeconfig writes the platform cluster's kubeconfig once per run
// to a private file and returns its path. Every kubectl and helm call on the
// application path names it and the application namespace explicitly, so none
// resolves the ambient current context, which can name another kind cluster
// (GH-209). The gcp:seed and gcp:verify verbs replace it with the GKE
// cluster's kubeconfig (GH-880).
var applicationKubeconfig = sync.OnceValues(func() (string, error) {
	platform := platformCluster()
	config, err := exec.Command("kind", "get", "kubeconfig", "--name", platform).Output()
	if err != nil {
		return "", fmt.Errorf("%s is not running; run mage platform:up and mage app:up first: %w", platform, err)
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(cache, "cohere-demo", "kube", platform+".yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	return path, os.WriteFile(path, config, 0o600)
})

// applicationTargetArgs prefixes a kubectl or helm argv with the platform
// kubeconfig and the application namespace.
func applicationTargetArgs(kubeconfig string, args ...string) []string {
	return append([]string{"--kubeconfig", kubeconfig, "--namespace", cohereApplicationNamespace}, args...)
}

// applicationKubectlArgs targets args at the running application.
func applicationKubectlArgs(args ...string) ([]string, error) {
	kubeconfig, err := applicationKubeconfig()
	if err != nil {
		return nil, err
	}
	return applicationTargetArgs(kubeconfig, args...), nil
}

// Seed brings the running application from an empty corpus to a grounded state.
// mage app:up deploys the mesh but does not seed, and the rag-server binds
// its collection at startup via get_or_create, so a seed after app:up must
// also restart the rag-server or turns come back ungrounded with no error in
// any log. For each RAG unit declared in the demo chart values, Seed
// port-forwards the unit's in-cluster Chroma to the address the ingest expects,
// ingests the corpus, restarts the rag-server so it rebinds to the seeded
// collection, waits for the rollout, and tears the port-forward down.
//
// Unlike the raw Seed target, which skips silently when Chroma is unreachable
// (its contract is a host-side integration seed), app:seed fails loudly when
// the in-cluster Chroma cannot be reached: a seed that cannot seed must not
// report success.
func (App) Seed() error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	units, err := demoRagUnitNames(root)
	if err != nil {
		return err
	}
	if len(units) == 0 {
		return fmt.Errorf("app:seed: no ragUnits declared in %s", chatbotDemoValuesFile)
	}
	// Which vectors are written is decided by the deployed release, not by this
	// process: the vectors written here have to carry the same embedding
	// identity the chatbot declares for its query vector, or srd002 R3.3
	// excludes every source and the mesh answers from nothing. The release names
	// an identity and the store either holds vectors under it or the seed fails
	// saying so, rather than embedding the corpus to recover (srd015 R5.1, R5.4).
	for _, unit := range units {
		if err := seedDemoRagUnit(root, unit); err != nil {
			return err
		}
	}
	fmt.Printf("app:seed: %d RAG unit(s) seeded from the chunk store and restarted;"+
		" the corpus is queryable and no embedding provider was called\n", len(units))
	return nil
}

// demoSeedEmbeddingProvider names the provider an agent-driven ingest embeds
// through. app:seed no longer chooses one -- it takes vectors from the store --
// so this now serves the host-side `mage seed` and `mage integration:chroma`,
// which still drive the canonical corpus-ingest agent over the ingest fixture.
type demoSeedEmbeddingProvider struct {
	cohere bool
	model  string
}

// environment returns the corpus-ingest overrides the provider needs: the
// embedding identity the child's embed-rest.yaml passes to the bound library.
// The Ollama path keeps the profile's own default.
func (p demoSeedEmbeddingProvider) environment() []string {
	if !p.cohere {
		return nil
	}
	return []string{"CORPUS_EMBEDDING_MODEL=" + p.model}
}

// libraryArgs rebinds the child's embedder root to the provider's library
// (srd058 R2.2). The profile binds Ollama, so the Ollama path passes nothing;
// the providers root the file-selection chat reads is never rebound.
func (p demoSeedEmbeddingProvider) libraryArgs(coreRoot string) []string {
	if !p.cohere {
		return nil
	}
	return []string{"--library", "embedder=" + filepath.Join(coreRoot, "tools", "providers", "cohere")}
}

// seedDemoRagUnit seeds one RAG unit and leaves no port-forward behind. The
// port-forward is torn down before the function returns, including on the error
// paths, so a following mage integration:* target (a host-side mesh on the same
// ports) does not fail with bind: address already in use.
func seedDemoRagUnit(root, unit string) (retErr error) {
	deploy := fmt.Sprintf("%s-%s-%s", cohereApplicationRelease, demoChartName, unit)
	chromaService := deploy + "-chroma"

	stop, err := startChromaPortForward(chromaService)
	if err != nil {
		return fmt.Errorf("app:seed: port-forward svc/%s: %w", chromaService, err)
	}
	defer stop()

	if err := waitHTTPStatus(chromaHeartbeatURL, http.StatusOK, 30*time.Second); err != nil {
		return fmt.Errorf(
			"app:seed: in-cluster Chroma for %s not reachable at %s via port-forward svc/%s (is the application up? run mage app:up first): %w",
			unit, chromaHeartbeatURL, chromaService, err)
	}

	if err := seedDemoRagUnitCorpus(root, unit); err != nil {
		return err
	}

	if err := runDemoKubectl("rollout", "restart", "deploy/"+deploy); err != nil {
		return fmt.Errorf("app:seed: restart %s so it rebinds the seeded collection: %w", deploy, err)
	}
	if err := runDemoKubectl("rollout", "status", "deploy/"+deploy, "--timeout=120s"); err != nil {
		return fmt.Errorf("app:seed: wait for %s rollout: %w", deploy, err)
	}
	fmt.Printf("app:seed: %s seeded and restarted\n", unit)
	return nil
}

// demoValuesPath locates the demo values inside a chart directory, source or
// staged. The install reads the staged copy and the seed reads the source one;
// both name the same topology.
func demoValuesPath(chartDir string) string {
	return filepath.Join(chartDir, "ci", chatbotDemoValuesFile)
}

// seedDemoRagUnitCorpus writes one unit's collection from the chunk store: each
// chunk's stored vector under the identity the deployed release declares, and
// each chunk's stored metadata beside it. The provider is not called, so the
// demo starts without provider spend and without a reachable embedding provider
// (srd015 R5.1, R5.2).
//
// The identity comes from the deployed release rather than from the local
// values, for the reason app:verify reads the release too: the mesh under test
// is what the vectors have to match. demo:up sets each unit's embeddingModel to
// the Cohere model when that tier is enabled, so one field names the identity
// for both families and it is the same field that becomes RAG_EMBEDDING_MODEL
// on the rag-server -- what it reports for the collection it serves (R3.2).
func seedDemoRagUnitCorpus(root, unit string) error {
	collection, err := demoRagUnitCollection(root, unit)
	if err != nil {
		return err
	}
	identity, err := demoDeployedEmbeddingIdentity(unit)
	if err != nil {
		return fmt.Errorf("app:seed: %s: %w", unit, err)
	}
	report, err := seedCollectionFromCorpusStore(root, collection, identity)
	if err != nil {
		return fmt.Errorf("app:seed: seed %s from the chunk store: %w", unit, err)
	}
	fmt.Printf("app:seed: %s: %d chunks written to collection %q at identity %q"+
		" (dimension %d), no embedding request issued\n",
		unit, report.Chunks, report.Collection, report.Identity, report.Dimension)

	// The document collection is seeded beside the retrieval one. It carries
	// each document's envelope and every chunk version, addressed by identifier
	// so a citation resolves to its source, and it is never queried by
	// similarity -- so this costs no embedding request either (srd017 R8.2).
	served, err := seedDocumentsCollection(root, corpusDocumentsCollection)
	if err != nil {
		return fmt.Errorf("app:seed: seed the document collection: %w", err)
	}
	fmt.Printf("app:seed: %d envelopes and %d chunk versions written to"+
		" collection %q, no embedding request issued\n",
		served.Documents, served.Chunks, served.Collection)
	return nil
}

// demoDeployedEmbeddingIdentity reads the embedding model the deployed release
// declares for one RAG unit. That value reaches the rag-server as
// RAG_EMBEDDING_MODEL and is the identity it reports in its query response
// metadata, which is what srd002 R3.3 compares the query vector's family
// against.
//
// A release that cannot be read is an error rather than a default. app:seed
// already requires the cluster it port-forwards to, and guessing the identity
// is how a store built under one family gets seeded under the other -- the
// silent failure this release exists to remove.
func demoDeployedEmbeddingIdentity(unit string) (string, error) {
	out, err := readApplicationValues()
	if err != nil {
		return "", fmt.Errorf("read the deployed release values (is the application up?"+
			" run mage app:up first): %w", err)
	}
	var values struct {
		RAGUnits []struct {
			Name           string `json:"name"`
			EmbeddingModel string `json:"embeddingModel"`
		} `json:"ragUnits"`
	}
	if err := json.Unmarshal(out, &values); err != nil {
		return "", fmt.Errorf("decode the deployed release values: %w", err)
	}
	for _, declared := range values.RAGUnits {
		if strings.TrimSpace(declared.Name) != unit {
			continue
		}
		if model := strings.TrimSpace(declared.EmbeddingModel); model != "" {
			return model, nil
		}
		return "", fmt.Errorf("the deployed release declares no embeddingModel for this unit")
	}
	return "", fmt.Errorf("the deployed release declares no RAG unit named %q", unit)
}

// demoRagUnitCollection names the Chroma collection one unit serves, from the
// demo chart values that already declare the topology.
func demoRagUnitCollection(root, unit string) (string, error) {
	path := demoValuesPath(applicationChartDir(root))
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read demo values %s: %w", path, err)
	}
	var values struct {
		RAGUnits []struct {
			Name       string `yaml:"name"`
			Collection string `yaml:"collection"`
		} `yaml:"ragUnits"`
	}
	if err := yaml.Unmarshal(data, &values); err != nil {
		return "", fmt.Errorf("parse demo values %s: %w", path, err)
	}
	for _, declared := range values.RAGUnits {
		if strings.TrimSpace(declared.Name) != unit {
			continue
		}
		if collection := strings.TrimSpace(declared.Collection); collection != "" {
			return collection, nil
		}
		return "", fmt.Errorf("rag unit %q declares no collection in %s", unit, path)
	}
	return "", fmt.Errorf("rag unit %q is not declared in %s", unit, path)
}

// demoRagUnitNames reads the RAG unit names from the demo chart values, the
// single source of truth for the topology.
func demoRagUnitNames(root string) ([]string, error) {
	return demoRagUnitNamesFrom(demoValuesPath(applicationChartDir(root)))
}

func demoRagUnitNamesFrom(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read demo values %s: %w", path, err)
	}
	var values struct {
		RAGUnits []struct {
			Name string `yaml:"name"`
		} `yaml:"ragUnits"`
	}
	if err := yaml.Unmarshal(data, &values); err != nil {
		return nil, fmt.Errorf("parse demo values %s: %w", path, err)
	}
	names := make([]string, 0, len(values.RAGUnits))
	for _, unit := range values.RAGUnits {
		if name := strings.TrimSpace(unit.Name); name != "" {
			names = append(names, name)
		}
	}
	return names, nil
}

// startChromaPortForward forwards the in-cluster Chroma Service to the local
// address the ingest expects (127.0.0.1:8000, from chromaHeartbeatURL). It
// returns a stop function that terminates the forward; the caller defers it so
// no forward survives the seed.
func startChromaPortForward(service string) (func(), error) {
	args, err := applicationKubectlArgs("port-forward", "svc/"+service, "8000:8000")
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("kubectl", args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	stop := func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
	}
	return stop, nil
}

// demoHelmValuesArgs builds the argv that reads the deployed release's values,
// pinning the kubeconfig and namespace for the same reason runDemoKubectl does.
// Helm otherwise resolves the ambient current context, so an operator whose
// context names another cluster reads that cluster's release -- or no release
// at all -- while every kubectl call on the same path addresses the platform.
// app:seed then fails saying the application is down when it is up, and
// app:verify silently downgrades to "provider disabled" and skips the tier
// assertions it exists to make (GH-209).
//
// Both readers share this argv so the pin cannot come back on one and not the
// other.
func demoHelmValuesArgs(kubeconfig string) []string {
	return applicationTargetArgs(kubeconfig,
		"get", "values", cohereApplicationRelease, "--all", "--output", "json")
}

// readApplicationValues reads the running release's values.
func readApplicationValues() ([]byte, error) {
	kubeconfig, err := applicationKubeconfig()
	if err != nil {
		return nil, err
	}
	return exec.Command("helm", demoHelmValuesArgs(kubeconfig)...).Output()
}

// runDemoKubectl runs kubectl against the running application and surfaces the
// combined output on failure.
func runDemoKubectl(args ...string) error {
	full, err := applicationKubectlArgs(args...)
	if err != nil {
		return err
	}
	output, err := exec.Command("kubectl", full...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("kubectl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}
