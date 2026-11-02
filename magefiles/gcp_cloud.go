// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

// The GCP helpers the gcp:* verbs share (#877): the environment's names, the
// gcloud preflight, a private kubeconfig per run, the Workload Identity
// annotation, kubeconfig-bound namespace handling, and the phase lines and
// their dated log (#881).
// Carried from declarative-agents' gcprig and kindrig so no GCP code depends
// on that repository.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// gcpConfigFile is the gitignored operator configuration beside the
// repository root (eng07).
const gcpConfigFile = "gcp.yaml"

// gcpConfig names every resource a GCP helper acts on.
type gcpConfig struct {
	Project string
	Region  string
	Cluster string
	// Bucket, ServiceAccount, Namespace, KSA, and SyncKSA are the application
	// binding ForApplication derives.
	Bucket         string
	ServiceAccount string
	Namespace      string
	KSA            string
	// SyncKSA is the unannotated KSA GKE secret sync reads Secret Manager as.
	// An annotated KSA impersonates its Google service account, so the one
	// that reaches the bucket cannot be the one that reads the secrets.
	SyncKSA string
}

// ForApplication derives the cloud identity and bucket bound to one
// application, the names the environment root creates: bucket
// <project>-<application>-telemetry, Google service account
// <application>-objectstore, namespace app-<application>, KSA default, and
// secret-sync KSA secret-sync.
func (c gcpConfig) ForApplication(application string) gcpConfig {
	application = strings.TrimSpace(application)
	binding := c
	binding.Bucket = fmt.Sprintf("%s-%s-telemetry", c.Project, application)
	binding.ServiceAccount = application + "-objectstore"
	binding.Namespace = "app-" + application
	binding.KSA = "default"
	binding.SyncKSA = "secret-sync"
	return binding
}

// GSAEmail is the binding's Google service account address.
func (c gcpConfig) GSAEmail() string {
	return fmt.Sprintf("%s@%s.iam.gserviceaccount.com", c.ServiceAccount, c.Project)
}

// KSAAnnotation is the value gcpAnnotateKSA places on the namespace's KSA so
// GKE maps it to the Google service account.
func (c gcpConfig) KSAAnnotation() string { return c.GSAEmail() }

// BucketURL is the collector's bucket, with no endpoint override, so ambient
// identity resolves it.
func (c gcpConfig) BucketURL() string { return "gs://" + c.Bucket }

// gcpCommandRunner executes one command and returns its combined output;
// tests substitute a recorder.
type gcpCommandRunner func(name string, args ...string) ([]byte, error)

func gcpDefaultRun(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

// gcpLookPath is swapped in tests so preflight is provable on a machine with
// gcloud and one without.
var gcpLookPath = exec.LookPath

// gcpPreflight verifies the verbs can act at all: gcloud present, an account
// active, the configured project set and reachable. Every failure carries its
// one-line remedy.
func gcpPreflight(run gcpCommandRunner, config gcpConfig) error {
	if _, err := gcpLookPath("gcloud"); err != nil {
		return errors.New("gcloud is not installed; install the Google Cloud CLI: https://cloud.google.com/sdk/docs/install")
	}
	if strings.TrimSpace(config.Project) == "" {
		return fmt.Errorf("no project configured; set project in %s beside the repository root", gcpConfigFile)
	}
	out, err := run("gcloud", "auth", "list", "--filter=status:ACTIVE", "--format=value(account)")
	if err != nil {
		return fmt.Errorf("gcloud auth list: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if strings.TrimSpace(string(out)) == "" {
		return errors.New("no active gcloud account; run: gcloud auth login")
	}
	if out, err := run("gcloud", "projects", "describe", config.Project, "--format=value(projectId)"); err != nil {
		return fmt.Errorf("project %q is not accessible to the active account: %s",
			config.Project, strings.TrimSpace(string(out)))
	}
	return nil
}

// writeGcpKubeconfig fetches the cluster's credential into path and nothing
// else: KUBECONFIG is set for the child only, so the operator's own
// kubeconfig is never touched and no ambient context can leak into a run.
func writeGcpKubeconfig(config gcpConfig, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create kubeconfig directory: %w", err)
	}
	cmd := exec.Command("gcloud", "container", "clusters", "get-credentials", config.Cluster,
		"--project", config.Project, "--region", config.Region)
	cmd.Env = append(os.Environ(), "KUBECONFIG="+path)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("get-credentials %q: %w: %s", config.Cluster, err, strings.TrimSpace(string(out)))
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("restrict kubeconfig %s: %w", path, err)
	}
	return nil
}

// gcpKSAAnnotationKey is the annotation GKE reads to map a Kubernetes service
// account onto a Google service account under Workload Identity.
const gcpKSAAnnotationKey = "iam.gke.io/gcp-service-account"

// gcpAnnotateKSA points the application namespace's KSA at its Google service
// account, the Kubernetes half of the Workload Identity binding the
// environment root grants; overwrite keeps it idempotent.
func gcpAnnotateKSA(run gcpCommandRunner, application gcpConfig, kubeconfig string) error {
	if out, err := run("kubectl", "--kubeconfig", kubeconfig, "--namespace", application.Namespace,
		"annotate", "serviceaccount", application.KSA,
		gcpKSAAnnotationKey+"="+application.KSAAnnotation(), "--overwrite"); err != nil {
		return fmt.Errorf("annotate %s/%s for workload identity: %w: %s",
			application.Namespace, application.KSA, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// gcpKubeconfigRun binds every command to one kubeconfig through the child's
// KUBECONFIG, leaving the parent environment untouched.
func gcpKubeconfigRun(kubeconfig string) gcpCommandRunner {
	return func(name string, args ...string) ([]byte, error) {
		cmd := exec.Command(name, args...)
		cmd.Env = append(gcpWithoutKubeconfig(os.Environ()), "KUBECONFIG="+kubeconfig)
		return cmd.CombinedOutput()
	}
}

func gcpWithoutKubeconfig(environment []string) []string {
	kept := make([]string, 0, len(environment))
	for _, entry := range environment {
		if !strings.HasPrefix(entry, "KUBECONFIG=") {
			kept = append(kept, entry)
		}
	}
	return kept
}

// gcpNamespaceManager is the ownership label value the application namespace
// carries. It matches apprig's, because gcp:deploy hands the namespace to
// apprig's deploy runner, which refuses one any other manager owns.
const gcpNamespaceManager = "apprig"

// gcpNamespaceDeleteTimeout bounds the wait for a namespace's deletion.
const gcpNamespaceDeleteTimeout = "180s"

var gcpDNSLabel = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)

type gcpNamespaceDocument struct {
	Metadata struct {
		Labels map[string]string `json:"labels"`
	} `json:"metadata"`
}

// ensureGcpNamespace creates and labels the application namespace, or reuses
// one already carrying the ownership label, and refuses to adopt a namespace
// another manager owns. It reports whether this call created it, so a failed
// deploy removes only its own work.
func ensureGcpNamespace(run gcpCommandRunner, namespace string) (bool, error) {
	if !gcpDNSLabel.MatchString(namespace) {
		return false, fmt.Errorf("application namespace %q is not a DNS-1123 label", namespace)
	}
	document, exists, err := readGcpNamespace(run, namespace)
	if err != nil {
		return false, err
	}
	if exists {
		if got := document.Metadata.Labels["app.kubernetes.io/managed-by"]; got != gcpNamespaceManager {
			return false, fmt.Errorf("application namespace %s is managed by %q, not %s; refusing adoption",
				namespace, got, gcpNamespaceManager)
		}
		return false, nil
	}
	if out, err := run("kubectl", "create", "namespace", namespace); err != nil {
		return false, fmt.Errorf("create application namespace %s: %w: %s", namespace, err, strings.TrimSpace(string(out)))
	}
	if out, err := run("kubectl", "label", "namespace", namespace,
		"app.kubernetes.io/managed-by="+gcpNamespaceManager, "--overwrite"); err != nil {
		_, deleteErr := run("kubectl", "delete", "namespace", namespace,
			"--ignore-not-found=true", "--wait=true", "--timeout="+gcpNamespaceDeleteTimeout)
		return false, errors.Join(
			fmt.Errorf("label application namespace %s: %w: %s", namespace, err, strings.TrimSpace(string(out))),
			deleteErr)
	}
	return true, nil
}

// deleteGcpNamespace removes an application namespace this tooling owns and
// waits until it is gone. Unlike kind, a GKE cluster has no shared data plane
// to re-verify afterwards (Autopilot runs no kube-proxy pods), so deletion
// ends at the wait (declarative-agents#470).
func deleteGcpNamespace(run gcpCommandRunner, namespace string) error {
	document, exists, err := readGcpNamespace(run, namespace)
	if err != nil || !exists {
		return err
	}
	if got := document.Metadata.Labels["app.kubernetes.io/managed-by"]; got != gcpNamespaceManager {
		return fmt.Errorf("application namespace %s is managed by %q, not %s; refusing deletion",
			namespace, got, gcpNamespaceManager)
	}
	if out, err := run("kubectl", "delete", "namespace", namespace, "--wait=false"); err != nil {
		return fmt.Errorf("delete application namespace %s: %w: %s", namespace, err, strings.TrimSpace(string(out)))
	}
	if out, err := run("kubectl", "wait", "--for=delete", "namespace/"+namespace,
		"--timeout="+gcpNamespaceDeleteTimeout); err != nil {
		return fmt.Errorf("wait for application namespace %s deletion: %w: %s",
			namespace, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func readGcpNamespace(run gcpCommandRunner, namespace string) (gcpNamespaceDocument, bool, error) {
	out, err := run("kubectl", "get", "namespace", namespace, "-o", "json", "--ignore-not-found=true")
	if err != nil {
		return gcpNamespaceDocument{}, false, fmt.Errorf("read application namespace %s: %w: %s",
			namespace, err, strings.TrimSpace(string(out)))
	}
	if strings.TrimSpace(string(out)) == "" {
		return gcpNamespaceDocument{}, false, nil
	}
	var document gcpNamespaceDocument
	if err := json.Unmarshal(out, &document); err != nil {
		return gcpNamespaceDocument{}, false, fmt.Errorf("decode application namespace %s: %w", namespace, err)
	}
	return document, true, nil
}

// gcpPhaseLog is where gcpLogPhase keeps its lines besides stdout: the
// directory of dated log files and the verb that is running. It stays empty
// until a verb opens it, so a helper exercised alone writes no file.
var gcpPhaseLog struct{ dir, verb string }

// gcpOpenPhaseLog names the verb whose phase lines follow and the
// application root whose build/gcp directory keeps them (#881).
func gcpOpenPhaseLog(root, verb string) {
	gcpPhaseLog.dir = filepath.Join(root, "build", "gcp")
	gcpPhaseLog.verb = verb
}

// gcpLogPhase prints one phase line: what ran against which target, how long
// it took, and how it ended. It also appends the line to the day's phase log,
// so a session's history survives the terminal; a log that cannot be written
// warns and never fails the verb.
func gcpLogPhase(target, name, outcome string, started time.Time, detail string) {
	if detail != "" {
		detail = " " + detail
	}
	line := fmt.Sprintf("phase target=%s name=%s elapsed=%s outcome=%s%s",
		target, name, time.Since(started).Round(time.Millisecond), outcome, detail)
	fmt.Println(line)
	if err := appendGcpPhaseLog(gcpPhaseLog.dir, gcpPhaseLog.verb, time.Now(), line); err != nil {
		fmt.Fprintf(os.Stderr, "gcp: phase log not written: %v\n", err)
	}
}

// gcpLogOutcome logs one phase as done, or failed when err is set, and
// returns err so a verb can end on it.
func gcpLogOutcome(target, name, done string, started time.Time, err error) error {
	outcome := done
	if err != nil {
		outcome = "failed"
	}
	gcpLogPhase(target, name, outcome, started, "")
	return err
}

// appendGcpPhaseLog appends "<RFC 3339 time> <verb> <line>" to
// <dir>/<YYYY-MM-DD>.log, one file per local day. No directory means no
// verb opened the log.
func appendGcpPhaseLog(dir, verb string, now time.Time, line string) error {
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(dir, now.Format(time.DateOnly)+".log"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(file, "%s %s %s\n", now.Format(time.RFC3339), verb, line)
	return errors.Join(err, file.Close())
}
