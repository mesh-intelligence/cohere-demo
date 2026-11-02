// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// corpus:seedCluster is the in-cluster half of the GH-928 decision (GH-937):
// app:seed needs mage, and mage needs the private Go modules, so a stranger
// could install the chart and still not ground it. This target runs from the
// published seeder image -- the compiled magefiles binary with the publishable
// case baked in -- as a Helm job inside the cluster, so seeding needs no Go
// toolchain, no modules, and no org access. The seed semantics are app:seed's
// own: stored vectors under the deployed identity, the document collection
// beside the retrieval ones, no embedding provider called (srd015 R5.1, R5.2).
const (
	seedClusterUnitsEnv    = "SEED_UNITS"
	seedClusterCaseRootEnv = "SEED_CASE_ROOT"
	seedClusterRestartEnv  = "SEED_RESTART"
)

// seedClusterUnit is one RAG unit's seed assignment, rendered into the job by
// the chart from the same ragUnits list that renders the unit itself.
type seedClusterUnit struct {
	Name       string `json:"name"`
	ChromaURL  string `json:"chromaURL"`
	Collection string `json:"collection"`
	Identity   string `json:"identity"`
	Deployment string `json:"deployment"`
}

// SeedCluster seeds every RAG unit named in SEED_UNITS from the case store at
// SEED_CASE_ROOT, writing each unit's retrieval collection and the shared
// document collection through the unit's own Chroma service, then restarts the
// unit's rag-server through the Kubernetes API when SEED_RESTART is true, so
// the server rebinds the seeded collection.
func (Corpus) SeedCluster() error {
	// The job's environment is its explicit configuration: the chart renders
	// these three variables, and nothing else configures the pod.
	units, err := seedClusterUnits(os.Getenv(seedClusterUnitsEnv)) //nolint:forbidigo // chart-rendered job configuration
	if err != nil {
		return err
	}
	caseRoot := strings.TrimSpace(os.Getenv(seedClusterCaseRootEnv)) //nolint:forbidigo // chart-rendered job configuration
	if caseRoot == "" {
		return fmt.Errorf("corpus:seedCluster: %s names no case root", seedClusterCaseRootEnv)
	}
	restart := os.Getenv(seedClusterRestartEnv) == "true" //nolint:forbidigo // chart-rendered job configuration
	for _, unit := range units {
		if err := seedClusterOneUnit(caseRoot, unit, restart); err != nil {
			return err
		}
	}
	fmt.Printf("corpus:seedCluster: %d RAG unit(s) seeded from %s; no embedding request issued\n",
		len(units), caseRoot)
	return nil
}

func seedClusterUnits(encoded string) ([]seedClusterUnit, error) {
	if strings.TrimSpace(encoded) == "" {
		return nil, fmt.Errorf("corpus:seedCluster: %s is empty; the chart renders it from ragUnits", seedClusterUnitsEnv)
	}
	var units []seedClusterUnit
	if err := json.Unmarshal([]byte(encoded), &units); err != nil {
		return nil, fmt.Errorf("corpus:seedCluster: decode %s: %w", seedClusterUnitsEnv, err)
	}
	if len(units) == 0 {
		return nil, fmt.Errorf("corpus:seedCluster: %s names no units", seedClusterUnitsEnv)
	}
	for _, unit := range units {
		if unit.Name == "" || unit.ChromaURL == "" || unit.Collection == "" || unit.Identity == "" {
			return nil, fmt.Errorf("corpus:seedCluster: unit %+v is missing a field", unit)
		}
	}
	return units, nil
}

func seedClusterOneUnit(caseRoot string, unit seedClusterUnit, restart bool) error {
	setChromaBaseURL(unit.ChromaURL)
	heartbeat := strings.TrimSuffix(unit.ChromaURL, "/") + "/api/v2/heartbeat"
	if err := waitHTTPStatus(heartbeat, http.StatusOK, 120*time.Second); err != nil {
		return fmt.Errorf("corpus:seedCluster: %s: Chroma not reachable at %s: %w", unit.Name, heartbeat, err)
	}
	report, err := seedCollectionFromCorpusStore(caseRoot, unit.Collection, unit.Identity)
	if err != nil {
		return fmt.Errorf("corpus:seedCluster: seed %s: %w", unit.Name, err)
	}
	fmt.Printf("corpus:seedCluster: %s: %d chunks written to collection %q at identity %q (dimension %d)\n",
		unit.Name, report.Chunks, report.Collection, report.Identity, report.Dimension)
	served, err := seedDocumentsCollection(caseRoot, corpusDocumentsCollection)
	if err != nil {
		return fmt.Errorf("corpus:seedCluster: seed the document collection for %s: %w", unit.Name, err)
	}
	fmt.Printf("corpus:seedCluster: %d envelopes and %d chunk versions written to collection %q\n",
		served.Documents, served.Chunks, served.Collection)
	if !restart {
		return nil
	}
	if unit.Deployment == "" {
		return fmt.Errorf("corpus:seedCluster: %s: %s is true but the unit names no deployment", unit.Name, seedClusterRestartEnv)
	}
	api, err := inClusterAPI()
	if err != nil {
		return fmt.Errorf("corpus:seedCluster: %s: %w", unit.Name, err)
	}
	if err := api.restartDeployment(unit.Deployment); err != nil {
		return fmt.Errorf("corpus:seedCluster: restart %s so it rebinds the seeded collection: %w", unit.Deployment, err)
	}
	if err := api.waitDeploymentReady(unit.Deployment, 180*time.Second); err != nil {
		return fmt.Errorf("corpus:seedCluster: wait for %s rollout: %w", unit.Deployment, err)
	}
	fmt.Printf("corpus:seedCluster: %s seeded and restarted\n", unit.Name)
	return nil
}

// clusterAPI reaches the Kubernetes API from inside a pod with the service
// account the chart's seed job binds: no kubectl in the image, no client-go in
// the module -- a restart is one strategic-merge patch.
type clusterAPI struct {
	base      string
	namespace string
	token     string
	client    *http.Client
}

const serviceAccountDir = "/var/run/secrets/kubernetes.io/serviceaccount"

func inClusterAPI() (*clusterAPI, error) {
	host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT") //nolint:forbidigo // the in-cluster API location is only ever published as environment
	if host == "" || port == "" {
		return nil, fmt.Errorf("not running in a cluster: KUBERNETES_SERVICE_HOST is unset")
	}
	token, err := os.ReadFile(serviceAccountDir + "/token")
	if err != nil {
		return nil, err
	}
	namespace, err := os.ReadFile(serviceAccountDir + "/namespace")
	if err != nil {
		return nil, err
	}
	ca, err := os.ReadFile(serviceAccountDir + "/ca.crt")
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return nil, fmt.Errorf("the service account ca.crt holds no certificate")
	}
	return &clusterAPI{
		base:      "https://" + host + ":" + port,
		namespace: strings.TrimSpace(string(namespace)),
		token:     strings.TrimSpace(string(token)),
		client: &http.Client{
			Timeout:   30 * time.Second,
			Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}},
		},
	}, nil
}

func (api *clusterAPI) deploymentURL(name string) string {
	return api.base + "/apis/apps/v1/namespaces/" + api.namespace + "/deployments/" + name
}

func (api *clusterAPI) do(method, url, contentType string, body string) ([]byte, int, error) {
	request, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("Authorization", "Bearer "+api.token)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := api.client.Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = response.Body.Close() }()
	data := make([]byte, 0, 4096)
	buffer := make([]byte, 4096)
	for {
		n, readErr := response.Body.Read(buffer)
		data = append(data, buffer[:n]...)
		if readErr != nil {
			break
		}
	}
	return data, response.StatusCode, nil
}

// restartDeployment is kubectl rollout restart as the API sees it: a new
// restartedAt annotation on the pod template.
func (api *clusterAPI) restartDeployment(name string) error {
	patch := fmt.Sprintf(
		`{"spec":{"template":{"metadata":{"annotations":{"kubectl.kubernetes.io/restartedAt":%q}}}}}`,
		time.Now().UTC().Format(time.RFC3339))
	data, status, err := api.do(http.MethodPatch, api.deploymentURL(name),
		"application/strategic-merge-patch+json", patch)
	if err != nil {
		return err
	}
	if status/100 != 2 {
		return fmt.Errorf("patch %s: status %d: %s", name, status, tailOfOutput(string(data)))
	}
	return nil
}

func (api *clusterAPI) waitDeploymentReady(name string, limit time.Duration) error {
	deadline := time.Now().Add(limit)
	for {
		data, status, err := api.do(http.MethodGet, api.deploymentURL(name), "", "")
		if err == nil && status == http.StatusOK {
			var deployment struct {
				Metadata struct {
					Generation int64 `json:"generation"`
				} `json:"metadata"`
				Spec struct {
					Replicas *int64 `json:"replicas"`
				} `json:"spec"`
				Status struct {
					ObservedGeneration int64 `json:"observedGeneration"`
					UpdatedReplicas    int64 `json:"updatedReplicas"`
					ReadyReplicas      int64 `json:"readyReplicas"`
				} `json:"status"`
			}
			if err := json.Unmarshal(data, &deployment); err == nil {
				want := int64(1)
				if deployment.Spec.Replicas != nil {
					want = *deployment.Spec.Replicas
				}
				if deployment.Status.ObservedGeneration >= deployment.Metadata.Generation &&
					deployment.Status.UpdatedReplicas >= want &&
					deployment.Status.ReadyReplicas >= want {
					return nil
				}
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("deployment %s not ready within %s", name, limit)
		}
		time.Sleep(3 * time.Second)
	}
}
