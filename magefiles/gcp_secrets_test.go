// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// A secret without an enabled version refuses the deploy by name, and the
// check only lists version metadata: no call accesses a value.
func TestRequireGcpSecretVersionsNamesTheEmptySecret(t *testing.T) {
	application := gcpTestApplication()
	var calls []string
	record := func(output string) func(string, ...string) ([]byte, error) {
		return func(name string, args ...string) ([]byte, error) {
			calls = append(calls, name+" "+strings.Join(args, " "))
			if strings.Contains(strings.Join(args, " "), "serpapi-api-key") {
				return []byte(output), nil
			}
			return []byte("projects/p/secrets/cohere-demo-cohere-api-key/versions/1"), nil
		}
	}
	err := requireGcpSecretVersions(record(""), application)
	if err == nil || !strings.Contains(err.Error(), "cohere-demo-serpapi-api-key has no enabled version") {
		t.Errorf("an empty secret = %v", err)
	}
	// gcloud warns on stderr when the listing is empty; the warning is output
	// but not a version (found against the live project).
	err = requireGcpSecretVersions(record("WARNING: The following filter keys were not present in any resource : state\n"), application)
	if err == nil || !strings.Contains(err.Error(), "cohere-demo-serpapi-api-key has no enabled version") {
		t.Errorf("an empty secret behind a gcloud warning = %v", err)
	}
	if err := requireGcpSecretVersions(record("projects/p/secrets/cohere-demo-serpapi-api-key/versions/2"),
		application); err != nil {
		t.Errorf("secrets with versions were refused: %v", err)
	}
	for _, call := range calls {
		if strings.Contains(call, " access") {
			t.Errorf("the version check read a value: %s", call)
		}
	}
	denied := func(string, ...string) ([]byte, error) { return []byte("NOT_FOUND"), errors.New("exit status 1") }
	if err := requireGcpSecretVersions(denied, application); err == nil ||
		!strings.Contains(err.Error(), "mage gcp:up creates it") {
		t.Errorf("a missing secret = %v", err)
	}
}

// The sync manifest names each Secret Manager entry and the Kubernetes Secret
// and key the chart references, authenticates as the unannotated secret-sync
// KSA it creates, and carries no value.
func TestGcpSecretSyncManifestTargetsTheChartSecrets(t *testing.T) {
	application := gcpTestApplication()
	manifest, err := gcpSecretSyncManifest(application)
	if err != nil {
		t.Fatal(err)
	}
	decoder := yaml.NewDecoder(strings.NewReader(manifest))
	kinds := map[string][]map[string]any{}
	for {
		var document map[string]any
		if err := decoder.Decode(&document); err != nil {
			break
		}
		kinds[document["kind"].(string)] = append(kinds[document["kind"].(string)], document)
	}
	if len(kinds["SecretProviderClass"]) != 1 || len(kinds["SecretSync"]) != 3 || len(kinds["ServiceAccount"]) != 1 {
		t.Fatalf("manifest kinds = %v", kinds)
	}
	// An annotated KSA impersonates the bucket's Google service account, which
	// reads no secret (found live: the sync failed with a provider error).
	account := kinds["ServiceAccount"][0]["metadata"].(map[string]any)
	if account["name"] != application.SyncKSA || account["name"] == application.KSA || account["annotations"] != nil {
		t.Errorf("the sync identity is %v; want an unannotated KSA apart from %s", account, application.KSA)
	}
	parameters := kinds["SecretProviderClass"][0]["spec"].(map[string]any)["parameters"].(map[string]any)["secrets"].(string)
	for _, want := range []string{
		"projects/p/secrets/cohere-demo-cohere-api-key/versions/latest",
		"projects/p/secrets/cohere-demo-serpapi-api-key/versions/latest",
	} {
		if !strings.Contains(parameters, want) {
			t.Errorf("the provider class lacks %s", want)
		}
	}
	if strings.Contains(parameters, "ghcr-pull") {
		t.Error("the registry token reaches the cluster through the provider class; Artifact Registry alone reads it")
	}
	synced := map[string]string{}
	for _, document := range kinds["SecretSync"] {
		metadata := document["metadata"].(map[string]any)
		spec := document["spec"].(map[string]any)
		if metadata["namespace"] != "app-cohere-demo" || spec["serviceAccountName"] != "secret-sync" {
			t.Errorf("SecretSync %v authenticates as %v", metadata, spec["serviceAccountName"])
		}
		for _, entry := range spec["secretObject"].(map[string]any)["data"].([]any) {
			data := entry.(map[string]any)
			synced[metadata["name"].(string)+"/"+data["targetKey"].(string)] = data["sourcePath"].(string)
		}
	}
	for key, source := range map[string]string{
		demoCohereSecret + "/" + demoCohereSecretKey:   "cohere-api-key",
		demoSerpapiSecret + "/" + demoSerpapiSecretKey: "serpapi-api-key",
		gcpIAPOAuthSecret + "/client_secret":           "iap-client-secret",
	} {
		if synced[key] != source {
			t.Errorf("Secret key %s syncs from %q, want %q", key, synced[key], source)
		}
	}
	// The GCPBackendPolicy refuses an OAuth Secret with more than one key (#883).
	if source, synced := synced[gcpIAPOAuthSecret+"/client_id"]; synced {
		t.Errorf("the OAuth Secret also syncs client_id from %q; the policy accepts client_secret alone", source)
	}
	for _, forbidden := range []string{"stringData", "data:\n  api-key"} {
		if strings.Contains(manifest, forbidden) {
			t.Errorf("the sync manifest carries %q", forbidden)
		}
	}
}

// The deploy waits for every synced Secret and names the one the controller
// never wrote.
func TestWaitGcpSecretsNamesTheUnsyncedSecret(t *testing.T) {
	present := func(string, ...string) ([]byte, error) { return nil, nil }
	if err := waitGcpSecrets(present, time.Second, time.Millisecond); err != nil {
		t.Errorf("synced Secrets were refused: %v", err)
	}
	missingSerpapi := func(name string, args ...string) ([]byte, error) {
		if strings.Contains(strings.Join(args, " "), demoSerpapiSecret) {
			return []byte("NotFound"), errors.New("exit status 1")
		}
		return nil, nil
	}
	err := waitGcpSecrets(missingSerpapi, 10*time.Millisecond, time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "no Secret "+demoSerpapiSecret) {
		t.Errorf("an unsynced Secret = %v", err)
	}
}

// gcp:secrets puts each value on gcloud's stdin and never in its arguments;
// a secret with a version is kept unless rotating; a key keys.json lacks is
// named and skipped; the list check reads metadata only.
func TestPushGcpSecretsUsesStdinOnly(t *testing.T) {
	keys := map[string]string{"cohere": "co-value", "serpapi": "serp-value", "ghcr_pat": "ghp-value"}
	var argv []string
	stdin := map[string]string{}
	push := func(input, name string, args ...string) ([]byte, error) {
		line := name + " " + strings.Join(args, " ")
		argv = append(argv, line)
		stdin[args[3]] = input
		return nil, nil
	}
	versions := map[string]string{"cohere-demo-serpapi-api-key": "3\n"}
	list := func(name string, args ...string) ([]byte, error) {
		argv = append(argv, name+" "+strings.Join(args, " "))
		return []byte(versions[args[3]]), nil
	}
	report, err := pushGcpSecrets(list, push, "p", keys, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(report.Pushed, ",") != "cohere-demo-cohere-api-key,cohere-demo-ghcr-pull" ||
		strings.Join(report.Kept, ",") != "cohere-demo-serpapi-api-key" ||
		strings.Join(report.Missing, ",") != "cohere-demo-iap-client-id (iap_client_id),cohere-demo-iap-client-secret (iap_client_secret)" {
		t.Errorf("report = %+v", report)
	}
	if stdin["cohere-demo-cohere-api-key"] != "co-value" || stdin["cohere-demo-ghcr-pull"] != "ghp-value" {
		t.Errorf("stdin = %v", stdin)
	}
	for _, line := range argv {
		for _, value := range keys {
			if strings.Contains(line, value) {
				t.Errorf("a credential reached argv: %s", line)
			}
		}
		if strings.Contains(line, " access ") {
			t.Errorf("a value was read back: %s", line)
		}
	}

	rotated, err := pushGcpSecrets(list, push, "p", keys, true)
	if err != nil || len(rotated.Pushed) != 3 || len(rotated.Kept) != 0 {
		t.Errorf("rotate = %+v, %v; want every present key pushed", rotated, err)
	}

	absent := func(string, ...string) ([]byte, error) {
		return []byte("NOT_FOUND: Secret not found"), errors.New("exit status 1")
	}
	if _, err := pushGcpSecrets(absent, push, "p", keys, false); err == nil || !strings.Contains(err.Error(), "gcp:up first") {
		t.Errorf("a secret gcp:up has not created = %v", err)
	}
}

// keys.json keeps only non-empty string fields; a malformed file names itself.
func TestParseOperatorKeysKeepsStringFields(t *testing.T) {
	keys, err := parseOperatorKeys("keys.json", []byte(`{"cohere":" c ","serpapi":"","_comment":["x"],"ghcr_username":"octocat"}`))
	if err != nil {
		t.Fatal(err)
	}
	if keys["cohere"] != "c" || keys["ghcr_username"] != "octocat" || len(keys) != 2 {
		t.Errorf("keys = %v", keys)
	}
	if _, err := parseOperatorKeys("keys.json", []byte("{")); err == nil || !strings.Contains(err.Error(), "keys.json") {
		t.Errorf("a malformed store = %v", err)
	}
}
