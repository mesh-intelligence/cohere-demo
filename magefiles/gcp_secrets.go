// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// gcpSecret is one credential in Secret Manager. Name is the id the environment
// root (infra/gcp/envs) declares; Terraform names the entry <application>-<Name>
// and never holds its value. KeysField is where gcp:secrets reads the value in
// the operator's .secrets/keys.json. Secret and Key are the Kubernetes Secret
// and key GKE secret sync writes it to for the chart (srd009 R1.3); an entry
// with Sync false reaches no pod: a Google service reads it, or gcp:deploy
// hands it to Terraform from keys.json.
type gcpSecret struct {
	Name      string
	KeysField string
	Sync      bool
	Secret    string
	Key       string
}

// gcpIAPOAuthSecret is the Secret the exposure root's GCPBackendPolicy reads
// the custom OAuth client secret from, at its client_secret key (#885). The
// policy refuses a Secret holding anything else ("must have exactly 1
// key-value pair", found live in #883), so the client id never syncs here: it
// is an identifier, and gcp:deploy passes it to the exposure root.
const gcpIAPOAuthSecret = "cohere-demo-iap-oauth"

// gcpSecrets are the credentials the cohere-demo release references. Entries
// naming the same Secret sync into it as separate keys.
var gcpSecrets = []gcpSecret{
	{Name: "cohere-api-key", KeysField: "cohere", Sync: true, Secret: demoCohereSecret, Key: demoCohereSecretKey},
	{Name: "serpapi-api-key", KeysField: "serpapi", Sync: true, Secret: demoSerpapiSecret, Key: demoSerpapiSecretKey},
	{Name: "iap-client-id", KeysField: "iap_client_id"},
	{Name: "iap-client-secret", KeysField: "iap_client_secret", Sync: true, Secret: gcpIAPOAuthSecret, Key: "client_secret"},
	// Artifact Registry reads the registry token to pull ghcr.io images
	// through its remote repository; no pod ever sees it.
	{Name: "ghcr-pull", KeysField: "ghcr_pat"},
}

// gcpSyncedSecrets are the distinct Kubernetes Secrets the synced entries of
// gcpSecrets fill, in declaration order, each with its keys.
func gcpSyncedSecrets() ([]string, map[string][]gcpSecret) {
	var names []string
	keys := map[string][]gcpSecret{}
	for _, secret := range gcpSecrets {
		if !secret.Sync {
			continue
		}
		if _, seen := keys[secret.Secret]; !seen {
			names = append(names, secret.Secret)
		}
		keys[secret.Secret] = append(keys[secret.Secret], secret)
	}
	return names, keys
}

// gcpSecretProviderClass names the SecretProviderClass every SecretSync reads.
const gcpSecretProviderClass = "cohere-demo-credentials"

// gcpSecretSyncWait bounds how long gcp:deploy waits for the sync controller
// to write each Kubernetes Secret.
const gcpSecretSyncWait = 2 * time.Minute

func gcpSecretID(secret gcpSecret) string {
	return cohereApplicationRelease + "-" + secret.Name
}

// requireGcpSecretVersions refuses a deploy when a declared secret has no
// enabled version. It lists version metadata only and never accesses a value.
func requireGcpSecretVersions(run gcpCommandRunner, application gcpConfig) error {
	for _, secret := range gcpSecrets {
		id := gcpSecretID(secret)
		out, err := run("gcloud", "secrets", "versions", "list", id, "--project", application.Project,
			"--filter=state:ENABLED", "--limit=1", "--format=value(name)")
		if err != nil {
			return fmt.Errorf("gcp:deploy: Secret Manager secret %s is not readable (%s); mage gcp:up creates it",
				id, strings.TrimSpace(string(out)))
		}
		if !listsSecretVersion(string(out)) {
			return fmt.Errorf("gcp:deploy: Secret Manager secret %s has no enabled version; set %q in"+
				" .secrets/keys.json and run mage gcp:secrets", id, secret.KeysField)
		}
	}
	return nil
}

// listsSecretVersion reports whether a version listing names a version. The
// runner returns gcloud's combined output, and an empty listing still prints
// a filter warning, so any output is not evidence of a version; only a line
// that is a version id is.
func listsSecretVersion(output string) bool {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if _, id, found := strings.Cut(line, "/versions/"); found {
			line = id
		}
		if line != "" && strings.Trim(line, "0123456789") == "" {
			return true
		}
	}
	return false
}

// gcpSecretSyncManifest is the sync identity's KSA, the SecretProviderClass,
// and one SecretSync per credential: GKE's sync controller, authenticating as
// that KSA through Workload Identity, writes each Kubernetes Secret from the
// latest version. The manifest names secrets, never values.
func gcpSecretSyncManifest(application gcpConfig) (string, error) {
	type entry struct {
		ResourceName string `yaml:"resourceName"`
		Path         string `yaml:"path"`
	}
	entries := make([]entry, 0, len(gcpSecrets))
	for _, secret := range gcpSecrets {
		if !secret.Sync {
			continue
		}
		entries = append(entries, entry{
			ResourceName: fmt.Sprintf("projects/%s/secrets/%s/versions/latest",
				application.Project, gcpSecretID(secret)),
			Path: secret.Name,
		})
	}
	parameters, err := yaml.Marshal(entries)
	if err != nil {
		return "", err
	}
	documents := []map[string]any{{
		"apiVersion": "v1",
		"kind":       "ServiceAccount",
		"metadata":   map[string]any{"name": application.SyncKSA, "namespace": application.Namespace},
	}, {
		"apiVersion": "secrets-store.csi.x-k8s.io/v1",
		"kind":       "SecretProviderClass",
		"metadata":   map[string]any{"name": gcpSecretProviderClass, "namespace": application.Namespace},
		"spec": map[string]any{
			"provider":   "gke",
			"parameters": map[string]any{"secrets": string(parameters)},
		},
	}}
	names, keys := gcpSyncedSecrets()
	for _, name := range names {
		data := make([]map[string]any, 0, len(keys[name]))
		for _, secret := range keys[name] {
			data = append(data, map[string]any{"sourcePath": secret.Name, "targetKey": secret.Key})
		}
		documents = append(documents, map[string]any{
			"apiVersion": "secret-sync.gke.io/v1",
			"kind":       "SecretSync",
			"metadata":   map[string]any{"name": name, "namespace": application.Namespace},
			"spec": map[string]any{
				"serviceAccountName":      application.SyncKSA,
				"secretProviderClassName": gcpSecretProviderClass,
				"secretObject":            map[string]any{"type": "Opaque", "data": data},
			},
		})
	}
	var manifest strings.Builder
	for _, document := range documents {
		data, err := yaml.Marshal(document)
		if err != nil {
			return "", err
		}
		manifest.WriteString("---\n")
		manifest.Write(data)
	}
	return manifest.String(), nil
}

// syncGcpSecrets applies the sync resources and waits until the controller has
// written every Kubernetes Secret the chart references, so the deploy never
// starts pods against a Secret that does not exist yet.
func syncGcpSecrets(run helmLLMCommandRunner, application gcpConfig) error {
	manifest, err := gcpSecretSyncManifest(application)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp("", "cohere-demo-secret-sync-*.yaml")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	if _, err := file.WriteString(manifest); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if out, err := run("kubectl", "apply", "-f", file.Name()); err != nil {
		return fmt.Errorf("apply the Secret Manager sync: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return waitGcpSecrets(run, gcpSecretSyncWait, 3*time.Second)
}

func waitGcpSecrets(run helmLLMCommandRunner, timeout, interval time.Duration) error {
	deadline := time.Now().Add(timeout)
	names, _ := gcpSyncedSecrets()
	for _, name := range names {
		for {
			if _, err := run("kubectl", "get", "secret", name, "--output=name"); err == nil {
				break
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("gcp:deploy: the sync controller wrote no Secret %s within %s; check"+
					" kubectl describe secretsync %s (secret sync enabled on the cluster, and"+
					" secretAccessor for the namespace KSA)", name, timeout, name)
			}
			time.Sleep(interval)
		}
	}
	return nil
}

// gcpStdinRunner runs one command with stdin as its standard input. A
// credential reaches gcloud only this way: never as an argument, which the
// process table shows, and never as an environment variable (eng07).
type gcpStdinRunner func(stdin, name string, args ...string) ([]byte, error)

func gcpDefaultStdinRun(stdin, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Stdin = strings.NewReader(stdin)
	return cmd.CombinedOutput()
}

// gcpSecretsReport is what one gcp:secrets run did, by Secret Manager id.
type gcpSecretsReport struct {
	Pushed  []string
	Kept    []string
	Missing []string
}

// Secrets stores every credential from .secrets/keys.json in its Secret
// Manager entry. A secret that already has an enabled version is kept; a key
// keys.json does not carry is named and skipped. Run it after gcp:up, which
// creates the entries empty.
func (Gcp) Secrets() error {
	return runGcpSecrets(false)
}

// SecretsRotate stores every credential from .secrets/keys.json as a new
// version, whether or not the entry already has one.
func (Gcp) SecretsRotate() error {
	return runGcpSecrets(true)
}

func runGcpSecrets(rotate bool) error {
	verb := "gcp:secrets"
	if rotate {
		verb = "gcp:secretsRotate"
	}
	run, err := newGcpRun(verb)
	if err != nil {
		return err
	}
	keys, err := readOperatorKeys()
	if err != nil {
		return err
	}
	started := time.Now()
	report, err := pushGcpSecrets(gcpDefaultRun, gcpDefaultStdinRun, run.settings.project(), keys, rotate)
	if err != nil {
		return gcpLogOutcome(run.settings.project(), "secrets", "", started, err)
	}
	gcpLogPhase(run.settings.project(), "secrets", "stored", started, fmt.Sprintf("pushed=%d kept=%d missing=%d",
		len(report.Pushed), len(report.Kept), len(report.Missing)))
	printGcpSecretsReport(report, run.settings.project())
	return nil
}

// pushGcpSecrets adds each available value as a new version, over stdin.
// Listing a secret's versions reads metadata only, so the check never reads
// a value back.
func pushGcpSecrets(
	run gcpCommandRunner, push gcpStdinRunner, project string, keys map[string]string, rotate bool,
) (gcpSecretsReport, error) {
	var report gcpSecretsReport
	for _, secret := range gcpSecrets {
		id := gcpSecretID(secret)
		value := strings.TrimSpace(keys[secret.KeysField])
		if value == "" {
			report.Missing = append(report.Missing, id+" ("+secret.KeysField+")")
			continue
		}
		out, err := run("gcloud", "secrets", "versions", "list", id, "--project", project,
			"--filter=state:ENABLED", "--limit=1", "--format=value(name)")
		if err != nil {
			return report, fmt.Errorf("gcp:secrets: Secret Manager secret %s is not readable (%s); run mage gcp:up first",
				id, strings.TrimSpace(string(out)))
		}
		if !rotate && listsSecretVersion(string(out)) {
			report.Kept = append(report.Kept, id)
			continue
		}
		if out, err := push(value, "gcloud", "secrets", "versions", "add", id, "--project", project,
			"--data-file=-"); err != nil {
			return report, fmt.Errorf("gcp:secrets: add a version to %s: %w: %s", id, err, strings.TrimSpace(string(out)))
		}
		report.Pushed = append(report.Pushed, id)
	}
	return report, nil
}

func printGcpSecretsReport(report gcpSecretsReport, project string) {
	line := func(label string, ids []string) {
		if len(ids) > 0 {
			fmt.Printf("gcp:secrets: %s: %s\n", label, strings.Join(ids, ", "))
		}
	}
	line("stored a new version", report.Pushed)
	line("kept the existing version", report.Kept)
	line("no value in .secrets/keys.json", report.Missing)
	for _, id := range report.Pushed {
		if id == gcpGhcrTokenSecret {
			fmt.Printf("gcp:secrets: run mage gcp:up again to create the ghcr.io repository in %s\n", project)
		}
	}
}
