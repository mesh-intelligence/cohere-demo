// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// The GCP footprint's Terraform (#868) and the verbs that apply it (#870).
// gcp:up applies the bootstrap root, which creates the state bucket on local
// state and the chatbot's reserved address, then the selected environment
// root on that bucket; gcp:deploy applies the environment's exposure root
// after the release (#885); gcp:down destroys the exposure root and then the
// environment root; gcp:bootstrapDown removes the state bucket and the
// address when the demo is retired. Upstream's tfrig runs Terraform; nothing here takes
// settings from the environment (eng07, eng11).
const (
	gcpTerraformRoot           = "infra/gcp"
	gcpStatePrefixBase         = "cohere-demo/"
	gcpDownConfirmationPrefix  = "delete-cohere-demo-"
	gcpBootstrapConfirmation   = "delete-cohere-demo-tfstate"
	gcpDefaultRegion           = "us-central1"
	gcpDefaultEnvironment      = "dev"
	gcpBootstrapBucketAddress  = "google_storage_bucket.tfstate"
	gcpBootstrapCorpusAddress  = "google_storage_bucket.corpus"
	gcpBootstrapIngressAddress = "google_compute_global_address.ingress"
	gcpExposureStateSuffix     = "-exposure"
	gcpIAPClientIDKeysField    = "iap_client_id"
	gcpGhcrUsernameKeysField   = "ghcr_username"
	gcpTerraformWorkDirSegment = "build/gcp/terraform"
	gcpGhcrTokenSecret         = "cohere-demo-ghcr-pull"
)

// gcpSettings is the deployment part of the gitignored gcp.yaml. The file's
// cohere_demo section (gcpAccess) holds the access settings.
type gcpSettings struct {
	Project            string `yaml:"project"`
	Region             string `yaml:"region"`
	Environment        string `yaml:"environment"`
	ProdProject        string `yaml:"prod_project"`
	Cluster            string `yaml:"cluster"`
	NodeServiceAccount string `yaml:"node_service_account"`
	// BillingAccount and BudgetAmount shape gcp:budget (#881). An empty
	// billing account is read from the project's billing link; the amount is
	// in that account's currency and defaults to 100.
	BillingAccount string `yaml:"billing_account"`
	BudgetAmount   int    `yaml:"budget_amount"`
}

// loadGcpSettings reads gcp.yaml beside root, fills the literal defaults, and
// refuses an environment with no root and a production environment aimed at
// the development project.
func loadGcpSettings(root string) (gcpSettings, error) {
	var settings gcpSettings
	data, err := os.ReadFile(filepath.Join(root, gcpConfigFile))
	if err != nil && !os.IsNotExist(err) {
		return settings, fmt.Errorf("read %s: %w", gcpConfigFile, err)
	}
	if err == nil {
		if err := yaml.Unmarshal(data, &settings); err != nil {
			return settings, fmt.Errorf("parse %s: %w", gcpConfigFile, err)
		}
	}
	if settings.Region == "" {
		settings.Region = gcpDefaultRegion
	}
	if settings.Environment == "" {
		settings.Environment = gcpDefaultEnvironment
	}
	if settings.BudgetAmount == 0 {
		settings.BudgetAmount = gcpDefaultBudgetAmount
	}
	return settings, settings.validate(root)
}

func (s gcpSettings) validate(root string) error {
	if strings.TrimSpace(s.Project) == "" {
		return fmt.Errorf("%s names no project; set project beside the repository root", gcpConfigFile)
	}
	if info, err := os.Stat(s.environmentDir(root)); err != nil || !info.IsDir() {
		return fmt.Errorf("%s environment %q has no root at %s", gcpConfigFile, s.Environment,
			filepath.Join(gcpTerraformRoot, "envs", s.Environment))
	}
	if s.Environment == "prod" && (s.ProdProject == "" || s.ProdProject == s.Project) {
		return fmt.Errorf("environment prod needs prod_project in %s naming a project other than %q;"+
			" production never shares the development project", gcpConfigFile, s.Project)
	}
	return nil
}

// project is the project the selected environment runs in.
func (s gcpSettings) project() string {
	if s.Environment == "prod" {
		return s.ProdProject
	}
	return s.Project
}

func (s gcpSettings) environmentDir(root string) string {
	return filepath.Join(root, gcpTerraformRoot, "envs", s.Environment)
}

func (s gcpSettings) stateBucket() string { return s.project() + "-tfstate" }

// corpusBucket is the private corpus bucket gcp:syncData mirrors the private
// data checkout into (GH-910).
func (s gcpSettings) corpusBucket() string { return s.project() + "-corpus" }

// rigConfig is the gcprig view of the environment, for the gcloud and
// kubectl helpers it shares with upstream. The cluster comes from the
// environment root's outputs once it exists.
func (s gcpSettings) rigConfig(cluster string) gcpConfig {
	config := gcpConfig{}
	config.Project = s.project()
	config.Region = s.Region
	config.Cluster = cluster
	return config
}

// defaultCluster is the cluster name the environment root uses when gcp.yaml
// overrides none.
func (s gcpSettings) defaultCluster() string {
	if s.Cluster != "" {
		return s.Cluster
	}
	return "cohere-demo-" + s.Environment
}

func gcpBootstrapRoot(root string, s gcpSettings) tfRoot {
	return tfRoot{
		Dir:  filepath.Join(root, gcpTerraformRoot, "bootstrap"),
		Vars: map[string]any{"project": s.project(), "region": s.Region},
	}
}

func gcpEnvironmentRoot(root string, s gcpSettings, ghcrUsername string) tfRoot {
	vars := map[string]any{
		"project":       s.project(),
		"region":        s.Region,
		"ghcr_username": ghcrUsername,
	}
	if s.Cluster != "" {
		vars["cluster"] = s.Cluster
	}
	if s.NodeServiceAccount != "" {
		vars["node_service_account"] = s.NodeServiceAccount
	}
	return tfRoot{
		Dir:     s.environmentDir(root),
		Vars:    vars,
		Backend: map[string]string{"bucket": s.stateBucket(), "prefix": gcpStatePrefixBase + s.Environment},
	}
}

// gcpExposureRoot is the environment's exposure root (#885): the Gateway on
// the reserved address, the route that strips the Google front end and IAP
// headers, the certificate, IAP on the chatbot backend, and who IAP admits.
// It plans against the live cluster and the namespace the release creates,
// so gcp:deploy applies it after the release and gcp:down destroys it first.
func gcpExposureRoot(root string, s gcpSettings, access gcpAccess, cluster, address, iapClientID string) tfRoot {
	return tfRoot{
		Dir: filepath.Join(s.environmentDir(root), "exposure"),
		Vars: map[string]any{
			"project":       s.project(),
			"region":        s.Region,
			"cluster":       cluster,
			"address_name":  gcpIngressAddress,
			"address":       address,
			"host":          access.Host,
			"iap_members":   append([]string{}, access.IAPMembers...),
			"iap_client_id": iapClientID,
		},
		Backend: map[string]string{
			"bucket": s.stateBucket(),
			"prefix": gcpStatePrefixBase + s.Environment + gcpExposureStateSuffix,
		},
	}
}

func gcpTerraform(root string) (tfTerraform, error) {
	return tfDefaultTerraform(filepath.Join(root, gcpTerraformWorkDirSegment))
}

// gcpRun is everything a verb needs: the settings, the access section, and a
// Terraform runner bound to this checkout.
type gcpRun struct {
	root     string
	settings gcpSettings
	access   gcpAccess
	tf       tfTerraform
}

// newGcpRun loads gcp.yaml, preflights gcloud and terraform, and opens the
// phase log under the verb's name.
func newGcpRun(verb string) (gcpRun, error) {
	root, err := cohereApplicationRoot()
	if err != nil {
		return gcpRun{}, err
	}
	gcpOpenPhaseLog(root, verb)
	settings, err := loadGcpSettings(root)
	if err != nil {
		return gcpRun{}, err
	}
	access, err := loadGcpAccess(root)
	if err != nil {
		return gcpRun{}, err
	}
	if err := gcpPreflight(gcpDefaultRun, settings.rigConfig(settings.defaultCluster())); err != nil {
		return gcpRun{}, err
	}
	if _, err := tfPreflight(tfDefaultRun); err != nil {
		return gcpRun{}, err
	}
	tf, err := gcpTerraform(root)
	if err != nil {
		return gcpRun{}, err
	}
	return gcpRun{root: root, settings: settings, access: access, tf: tf}, nil
}

// Up applies the bootstrap root and then the environment root gcp.yaml
// selects, and prints the environment's outputs. A second run with nothing
// changed reports both roots unchanged.
func (Gcp) Up() error {
	run, err := newGcpRun("gcp:up")
	if err != nil {
		return err
	}
	username, err := operatorKey(gcpGhcrUsernameKeysField)
	if err != nil {
		return err
	}
	target := run.settings.defaultCluster()
	if err := run.bootstrap(target); err != nil {
		return err
	}
	environment := run.environmentRoot(username)
	if err := applyGcpRoot(run.tf, environment, target); err != nil {
		return explainGcpCollision(err, run.settings)
	}
	outputs, err := run.tf.Output(environment)
	if err != nil {
		return err
	}
	if ready, _ := environment.Vars["ghcr_token_ready"].(bool); !ready {
		fmt.Printf("gcp:up: %s has no version yet, so the ghcr.io repository is not created;"+
			" run mage gcp:secrets, then mage gcp:up again\n", gcpGhcrTokenSecret)
	}
	return printGcpOutputs(outputs)
}

// Plan shows what gcp:up would change in the environment root, without
// applying it.
func (Gcp) Plan() error {
	run, err := newGcpRun("gcp:plan")
	if err != nil {
		return err
	}
	username, err := operatorKey(gcpGhcrUsernameKeysField)
	if err != nil {
		return err
	}
	environment := run.environmentRoot(username)
	if err := run.tf.Init(environment); err != nil {
		return err
	}
	plan, changes, err := run.tf.Plan(environment, false)
	if err != nil {
		return err
	}
	out, err := run.tf.Run(nil, "-chdir="+environment.Dir, "show", "-no-color", plan)
	if err != nil {
		return fmt.Errorf("terraform show %s: %w", run.settings.Environment, err)
	}
	fmt.Print(string(out))
	if !changes {
		fmt.Printf("gcp:plan: %s has no changes\n", run.settings.Environment)
	}
	return nil
}

// Down destroys the environment's exposure root (load balancer, certificate,
// IAP membership), drains its persistent volumes, and destroys its root:
// cluster, registries, telemetry bucket, identity, and secret containers. The
// state bucket and the reserved address stay. The confirmation is delete-cohere-demo-<environment>.
func (Gcp) Down(confirmation string) error {
	root, err := cohereApplicationRoot()
	if err != nil {
		return err
	}
	settings, err := loadGcpSettings(root)
	if err != nil {
		return err
	}
	if err := requireGcpConfirmation(confirmation, gcpDownConfirmationPrefix+settings.Environment); err != nil {
		return err
	}
	run, err := newGcpRun("gcp:down")
	if err != nil {
		return err
	}
	username, err := operatorKey(gcpGhcrUsernameKeysField)
	if err != nil {
		return err
	}
	environment := run.environmentRoot(username)
	if err := run.tf.Init(environment); err != nil {
		return err
	}
	cluster := run.settings.defaultCluster()
	if outputs, err := run.tf.Output(environment); err == nil {
		if name := gcpOutputString(outputs, "cluster_name"); name != "" {
			cluster = name
		}
	}
	config := run.settings.rigConfig(cluster)
	if err := run.destroyExposure(cluster); err != nil {
		return err
	}
	if err := drainGcpVolumes(gcpDefaultRun, config); err != nil {
		return err
	}
	started := time.Now()
	plan, changes, err := run.tf.Plan(environment, true)
	if err != nil {
		return err
	}
	if changes {
		if err := run.tf.Apply(environment, plan); err != nil {
			return err
		}
	}
	gcpLogPhase(cluster, filepath.Base(environment.Dir), "destroyed", started, "")
	if err := dropEmptyGcpState(gcpDefaultRun, run.tf, environment); err != nil {
		return err
	}
	return reportStrandedGcpDisks(gcpDefaultRun, config)
}

// destroyExposure removes the environment's public edge before the cluster
// goes, so the load balancer and its network endpoint groups do not outlive
// the backends they point at. A root that was never applied holds nothing
// and is skipped without reading the operator's keys.
func (r gcpRun) destroyExposure(cluster string) error {
	exposure := gcpExposureRoot(r.root, r.settings, r.access, cluster, "", "")
	if err := r.tf.Init(exposure); err != nil {
		return err
	}
	held, err := gcpHeldOrEmpty(r.tf.StateList(exposure))
	if err != nil {
		return err
	}
	if len(held) == 0 {
		return nil
	}
	clientID, err := operatorKey(gcpIAPClientIDKeysField)
	if err != nil {
		return err
	}
	address, _ := gcpDescribeAddress(gcpDefaultRun, r.settings.project())
	exposure = gcpExposureRoot(r.root, r.settings, r.access, cluster, address, clientID)
	started := time.Now()
	plan, changes, err := r.tf.Plan(exposure, true)
	if err != nil {
		return err
	}
	if changes {
		if err := r.tf.Apply(exposure, plan); err != nil {
			return err
		}
	}
	gcpLogPhase(cluster, "exposure", "destroyed", started, "")
	return dropEmptyGcpState(gcpDefaultRun, r.tf, exposure)
}

// dropEmptyGcpState removes an environment's state object once a destroy has
// left it holding nothing, so the state bucket can be retired without an
// empty file standing in the way. A state that still names a resource stays.
func dropEmptyGcpState(run gcpCommandRunner, tf tfTerraform, root tfRoot) error {
	held, err := gcpHeldOrEmpty(tf.StateList(root))
	if err != nil {
		return err
	}
	if len(held) != 0 {
		return nil
	}
	return removeGcpStateObject(run, root.Backend)
}

func removeGcpStateObject(run gcpCommandRunner, backend map[string]string) error {
	object := "gs://" + backend["bucket"] + "/" + backend["prefix"] + "/default.tfstate"
	if out, err := run("gcloud", "storage", "rm", object); err != nil {
		text := strings.ToLower(string(out))
		if strings.Contains(text, "matched no objects") || strings.Contains(text, "not found") {
			return nil
		}
		return fmt.Errorf("remove empty state %s: %w: %s", object, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// BootstrapDown removes the state bucket and the chatbot's reserved address
// when the demo is retired. It refuses while the bucket still holds any
// state, cohere-demo's or anyone else's, so it cannot orphan a root that is
// still deployed.
func (Gcp) BootstrapDown(confirmation string) error {
	if err := requireGcpConfirmation(confirmation, gcpBootstrapConfirmation); err != nil {
		return err
	}
	run, err := newGcpRun("gcp:bootstrapDown")
	if err != nil {
		return err
	}
	bucket := run.settings.stateBucket()
	objects, err := gcpStateObjects(gcpDefaultRun, bucket)
	if err != nil {
		return err
	}
	if len(objects) > 0 {
		return fmt.Errorf("gcp:bootstrapDown: gs://%s still holds state (%s); destroy those roots first",
			bucket, strings.Join(objects, ", "))
	}
	target := run.settings.defaultCluster()
	bootstrap := gcpBootstrapRoot(run.root, run.settings)
	if err := run.prepareBootstrap(bootstrap); err != nil {
		return err
	}
	started := time.Now()
	plan, changes, err := run.tf.Plan(bootstrap, true)
	if err != nil {
		return err
	}
	if changes {
		if err := run.tf.Apply(bootstrap, plan); err != nil {
			return err
		}
	}
	gcpLogPhase(target, "bootstrap", "destroyed", started, "")
	return nil
}

// environmentRoot is the environment root with this run's settings and the
// ghcr.io repository gated on the token being stored: Artifact Registry
// reads the token when it creates that repository.
func (r gcpRun) environmentRoot(ghcrUsername string) tfRoot {
	environment := gcpEnvironmentRoot(r.root, r.settings, ghcrUsername)
	environment.Vars["ghcr_token_ready"] = gcpGhcrTokenReady(gcpDefaultRun, r.settings.project())
	return environment
}

// gcpGhcrTokenReady reports whether the registry token secret holds an
// enabled version. A secret that does not exist yet holds none.
func gcpGhcrTokenReady(run gcpCommandRunner, project string) bool {
	out, err := run("gcloud", "secrets", "versions", "list", gcpGhcrTokenSecret, "--project", project,
		"--filter=state:ENABLED", "--limit=1", "--format=value(name)")
	return err == nil && listsSecretVersion(string(out))
}

// deployConfig is the environment's gcprig view with the cluster gcp:up
// created, read from the environment root's outputs. It refuses before
// gcp:up has run, rather than deploying to a cluster name it guessed.
func (r gcpRun) deployConfig() (gcpConfig, gcpRegistries, error) {
	var registries gcpRegistries
	environment := gcpEnvironmentRoot(r.root, r.settings, "")
	if err := r.tf.Init(environment); err != nil {
		return gcpConfig{}, registries, err
	}
	outputs, err := r.tf.Output(environment)
	if err != nil {
		return gcpConfig{}, registries, err
	}
	cluster := gcpOutputString(outputs, "cluster_name")
	if cluster == "" {
		return gcpConfig{}, registries, fmt.Errorf("environment %s has no cluster yet; run mage gcp:up first",
			r.settings.Environment)
	}
	config := r.settings.rigConfig(cluster)
	if location := gcpOutputString(outputs, "cluster_location"); location != "" {
		config.Region = location
	}
	registries = gcpRegistries{
		Ghcr:      gcpOutputString(outputs, "ghcr_path"),
		Dockerhub: gcpOutputString(outputs, "dockerhub_path"),
	}
	if err := registries.require(); err != nil {
		return gcpConfig{}, registries, err
	}
	return config, registries, nil
}

// gcpRegistries are the two Artifact Registry remote repositories the nodes
// pull every image through (#868).
type gcpRegistries struct {
	Ghcr      string
	Dockerhub string
}

// require refuses a deploy before both repositories exist. The ghcr.io one
// waits for the registry token: Artifact Registry reads it at create time.
func (g gcpRegistries) require() error {
	switch {
	case g.Dockerhub == "":
		return fmt.Errorf("the environment has no Docker Hub repository yet; run mage gcp:up first")
	case g.Ghcr == "":
		return fmt.Errorf("the environment has no ghcr.io repository yet: %s has no version;"+
			" set ghcr_pat in .secrets/keys.json, run mage gcp:secrets, then mage gcp:up", gcpGhcrTokenSecret)
	}
	return nil
}

// bootstrap applies the state-bucket root, importing the bucket by name when
// it exists but this machine's local state does not know it.
func (r gcpRun) bootstrap(target string) error {
	bootstrap := gcpBootstrapRoot(r.root, r.settings)
	if err := r.prepareBootstrap(bootstrap); err != nil {
		return err
	}
	return applyGcpRoot(r.tf, bootstrap, target)
}

func (r gcpRun) prepareBootstrap(bootstrap tfRoot) error {
	if err := r.tf.Init(bootstrap); err != nil {
		return err
	}
	held, err := gcpHeldOrEmpty(r.tf.StateList(bootstrap))
	if err != nil {
		return err
	}
	exists := gcpBucketExists(gcpDefaultRun, r.settings.stateBucket())
	if needsGcpBootstrapImport(held, exists) {
		started := time.Now()
		if err := r.tf.Import(bootstrap, gcpBootstrapBucketAddress, r.settings.stateBucket()); err != nil {
			return err
		}
		gcpLogPhase(r.settings.defaultCluster(), "bootstrap", "imported", started, "gs://"+r.settings.stateBucket())
	}
	// The corpus bucket holds data worth real upload time, so a fresh
	// machine's local state adopts an existing one the way it adopts the
	// state bucket rather than failing the apply on a name collision.
	if gcpBucketExists(gcpDefaultRun, r.settings.corpusBucket()) && !held[gcpBootstrapCorpusAddress] {
		started := time.Now()
		if err := r.tf.Import(bootstrap, gcpBootstrapCorpusAddress, r.settings.corpusBucket()); err != nil {
			return err
		}
		gcpLogPhase(r.settings.defaultCluster(), "bootstrap", "imported", started, "gs://"+r.settings.corpusBucket())
	}
	// The address predates its Terraform ownership (gcp:deploy reserved it
	// with gcloud before #885), and a fresh machine's local state knows
	// neither it nor the bucket.
	_, err = gcpDescribeAddress(gcpDefaultRun, r.settings.project())
	if !needsGcpAddressImport(held, err == nil) {
		return nil
	}
	started := time.Now()
	id := "projects/" + r.settings.project() + "/global/addresses/" + gcpIngressAddress
	if err := r.tf.Import(bootstrap, gcpBootstrapIngressAddress, id); err != nil {
		return err
	}
	gcpLogPhase(r.settings.defaultCluster(), "bootstrap", "imported", started, gcpIngressAddress)
	return nil
}

// needsGcpAddressImport reports whether the reserved address exists in the
// project while the local bootstrap state does not hold it.
func needsGcpAddressImport(held map[string]bool, addressExists bool) bool {
	return addressExists && !held[gcpBootstrapIngressAddress]
}

// gcpIngressAddress is the reserved global address the chatbot's Gateway
// serves on; the bootstrap root owns it.
const gcpIngressAddress = "cohere-demo-ingress"

// gcpDescribeAddress reads the reserved address. Terraform creates it; this
// only reads it, for the import probe and for the host gcp:deploy hands the
// release and the exposure root.
func gcpDescribeAddress(run gcpCommandRunner, project string) (string, error) {
	out, err := run("gcloud", "compute", "addresses", "describe", gcpIngressAddress,
		"--global", "--project", project, "--format=value(address)")
	address := strings.TrimSpace(string(out))
	if err != nil {
		return "", fmt.Errorf("read reserved address %s: %w: %s", gcpIngressAddress, err, address)
	}
	if address == "" {
		return "", fmt.Errorf("reserved address %s has no address", gcpIngressAddress)
	}
	return address, nil
}

// gcpHeldOrEmpty reads a state listing, treating a root that has never been
// applied on this machine as holding nothing: terraform state list fails
// with "No state file was found" there rather than printing an empty list.
func gcpHeldOrEmpty(held map[string]bool, err error) (map[string]bool, error) {
	if err != nil && strings.Contains(err.Error(), "No state file was found") {
		return map[string]bool{}, nil
	}
	return held, err
}

// needsGcpBootstrapImport reports whether the state bucket exists in the
// project while the local bootstrap state does not hold it: a fresh machine,
// or a bucket another tool created under the same name.
func needsGcpBootstrapImport(held map[string]bool, bucketExists bool) bool {
	return bucketExists && !held[gcpBootstrapBucketAddress]
}

// applyGcpRoot initializes, plans, and applies one root, applying only when
// the plan changes something, with one phase line either way.
func applyGcpRoot(tf tfTerraform, root tfRoot, target string) error {
	started := time.Now()
	if err := tf.Init(root); err != nil {
		return err
	}
	plan, changes, err := tf.Plan(root, false)
	if err != nil {
		return err
	}
	if !changes {
		gcpLogPhase(target, filepath.Base(root.Dir), "unchanged", started, "")
		return nil
	}
	if err := tf.Apply(root, plan); err != nil {
		return err
	}
	gcpLogPhase(target, filepath.Base(root.Dir), "applied", started, "")
	return nil
}

// explainGcpCollision turns an apply that collided with an existing object
// into the release steps (#870): cohere-demo's bucket, service account, and
// secrets created by upstream's applications root must leave that state
// before this repository's root can own the same names.
func explainGcpCollision(err error, s gcpSettings) error {
	text := err.Error()
	if !strings.Contains(text, "already exists") && !strings.Contains(text, "Error 409") {
		return err
	}
	return fmt.Errorf("%w\n\ngcp:up collided with objects that already exist under cohere-demo's names in project %s."+
		" If declarative-agents' applications root still owns them, release them once, from the declarative-agents checkout:\n"+
		"  1. remove cohere-demo from gcp.yaml applications:\n"+
		"  2. terraform state rm its cohere-demo addresses in the applications prefix of gs://%s\n"+
		"  3. delete the released bucket, service account, and cohere-demo-* secrets with gcloud\n"+
		"then run mage gcp:up again (cohere-demo#870)", err, s.project(), s.stateBucket())
}

func requireGcpConfirmation(got, want string) error {
	if got != want {
		return fmt.Errorf("refusing: the confirmation must be exactly %q", want)
	}
	return nil
}

// gcpBucketExists reports whether a bucket answers describe.
func gcpBucketExists(run gcpCommandRunner, bucket string) bool {
	_, err := run("gcloud", "storage", "buckets", "describe", "gs://"+bucket, "--format=value(name)")
	return err == nil
}

// gcpStateObjects names the live state objects in the bucket, by prefix.
// A bucket that does not exist holds none.
func gcpStateObjects(run gcpCommandRunner, bucket string) ([]string, error) {
	out, err := run("gcloud", "storage", "ls", "gs://"+bucket+"/**")
	if err != nil {
		text := strings.ToLower(string(out))
		if strings.Contains(text, "matched no objects") || strings.Contains(text, "not found") ||
			strings.Contains(text, "does not exist") {
			return nil, nil
		}
		return nil, fmt.Errorf("list gs://%s: %w: %s", bucket, err, strings.TrimSpace(string(out)))
	}
	var objects []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line = strings.TrimSpace(line); strings.HasPrefix(line, "gs://") && !strings.HasSuffix(line, "/") {
			objects = append(objects, strings.TrimPrefix(line, "gs://"+bucket+"/"))
		}
	}
	sort.Strings(objects)
	return objects, nil
}

// operatorKey reads one field of the operator's gitignored keys.json, from
// this checkout or, in a linked worktree, the main checkout.
func operatorKey(field string) (string, error) {
	keys, err := readOperatorKeys()
	if err != nil {
		return "", err
	}
	if value := keys[field]; value != "" {
		return value, nil
	}
	return "", fmt.Errorf(".secrets/keys.json has no %q; add it beside the other operator keys", field)
}

// readOperatorKeys reads the string fields of the operator's gitignored
// keys.json: from the working directory, the application root, or, in a linked
// worktree, the main checkout. A missing file is an empty store.
func readOperatorKeys() (map[string]string, error) {
	paths := []string{filepath.Join(".secrets", "keys.json")}
	if root, err := cohereApplicationRoot(); err == nil {
		paths = append(paths, filepath.Join(root, ".secrets", "keys.json"))
	}
	if main := gitMainCheckoutRoot(); main != "" {
		paths = append(paths, filepath.Join(main, ".secrets", "keys.json"))
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		return parseOperatorKeys(path, data)
	}
	return map[string]string{}, nil
}

func parseOperatorKeys(path string, data []byte) (map[string]string, error) {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	keys := make(map[string]string, len(raw))
	for name, value := range raw {
		if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
			keys[name] = strings.TrimSpace(text)
		}
	}
	return keys, nil
}

func gcpOutputString(outputs map[string]json.RawMessage, name string) string {
	var value string
	if raw, ok := outputs[name]; ok && json.Unmarshal(raw, &value) == nil {
		return value
	}
	return ""
}

func printGcpOutputs(outputs map[string]json.RawMessage) error {
	names := make([]string, 0, len(outputs))
	for name := range outputs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Printf("gcp:up: %s = %s\n", name, string(outputs[name]))
	}
	return nil
}

// Kubernetes, not Terraform, creates the persistent disks behind the
// cluster's claims, so destroying the cluster would leave them billing.
// gcp:down drains them first: it removes workloads and claims in every
// application namespace and waits for the CSI driver to delete the disks.
// Ported from upstream gcprig's drain, whose functions are unexported.

var gcpSystemNamespaces = map[string]bool{"kube-system": true, "kube-public": true, "kube-node-lease": true}

var (
	gcpVolumeDrainDeadline = 5 * time.Minute
	gcpVolumePollInterval  = 5 * time.Second
	gcpDrainSleep          = time.Sleep
	gcpWriteKubeconfig     = writeGcpKubeconfig
)

func gcpSystemNamespace(name string) bool {
	return gcpSystemNamespaces[name] || strings.HasPrefix(name, "gke-") || strings.HasPrefix(name, "gmp-")
}

// drainGcpVolumes drains a RUNNING cluster; an absent or not-yet-running
// cluster has nothing to drain and the destroy proceeds.
func drainGcpVolumes(run gcpCommandRunner, config gcpConfig) error {
	out, err := run("gcloud", "container", "clusters", "describe", config.Cluster,
		"--project", config.Project, "--region", config.Region, "--format=value(status)")
	status := strings.TrimSpace(string(out))
	switch {
	case err != nil && strings.Contains(strings.ToLower(status), "not found"):
		return nil
	case err != nil:
		return fmt.Errorf("describe cluster %q: %w: %s", config.Cluster, err, status)
	case status != "RUNNING":
		gcpLogPhase(config.Cluster, "volume-drain", "skipped", time.Now(), "status="+status)
		return nil
	}
	dir, err := os.MkdirTemp("", "cohere-demo-gcp-down-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	kubeconfig := filepath.Join(dir, "config")
	if err := gcpWriteKubeconfig(config, kubeconfig); err != nil {
		return err
	}
	started := time.Now()
	namespaces, err := gcpKubectlNames(run, kubeconfig, "get", "namespaces")
	if err != nil {
		return err
	}
	for _, namespace := range namespaces {
		if gcpSystemNamespace(namespace) {
			continue
		}
		for _, kinds := range []string{"statefulsets,deployments,daemonsets,jobs,cronjobs,pods", "persistentvolumeclaims"} {
			if out, err := run("kubectl", "--kubeconfig", kubeconfig, "--namespace", namespace,
				"delete", kinds, "--all", "--wait=true", "--timeout=5m"); err != nil {
				return fmt.Errorf("drain namespace %s (%s): %w: %s", namespace, kinds, err, strings.TrimSpace(string(out)))
			}
		}
	}
	if err := waitGcpVolumesReleased(run, kubeconfig); err != nil {
		return err
	}
	gcpLogPhase(config.Cluster, "volume-drain", "drained", started, "")
	return nil
}

func gcpKubectlNames(run gcpCommandRunner, kubeconfig string, args ...string) ([]string, error) {
	out, err := run("kubectl", append([]string{"--kubeconfig", kubeconfig}, append(args, "-o", "name")...)...)
	if err != nil {
		return nil, fmt.Errorf("kubectl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	var names []string
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			names = append(names, line[strings.LastIndex(line, "/")+1:])
		}
	}
	return names, nil
}

func waitGcpVolumesReleased(run gcpCommandRunner, kubeconfig string) error {
	deadline := time.Now().Add(gcpVolumeDrainDeadline)
	for {
		out, err := run("kubectl", "--kubeconfig", kubeconfig, "get", "persistentvolumes",
			"-o", `jsonpath={range .items[?(@.spec.persistentVolumeReclaimPolicy=="Delete")]}{.metadata.name}{"\n"}{end}`)
		if err != nil {
			return fmt.Errorf("list persistent volumes: %w: %s", err, strings.TrimSpace(string(out)))
		}
		remaining := strings.Fields(string(out))
		if len(remaining) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("persistent volumes %s were not released within %s; their disks would outlive the cluster",
				strings.Join(remaining, ", "), gcpVolumeDrainDeadline)
		}
		gcpDrainSleep(gcpVolumePollInterval)
	}
}

// reportStrandedGcpDisks names unattached pvc- disks left in the project. It
// reports and never deletes: a disk it cannot attribute is not its to remove.
func reportStrandedGcpDisks(run gcpCommandRunner, config gcpConfig) error {
	out, err := run("gcloud", "compute", "disks", "list", "--project", config.Project,
		"--filter", "name~^pvc- AND -users:*", "--format=value(name,zone.basename())")
	if err != nil {
		return fmt.Errorf("list disks: %w: %s", err, strings.TrimSpace(string(out)))
	}
	var disks []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if fields := strings.Fields(line); len(fields) == 2 {
			disks = append(disks, fields[1]+"/"+fields[0])
		}
	}
	if len(disks) == 0 {
		return nil
	}
	return errors.New("teardown left unattached pvc disks still billing in project " + config.Project + ": " +
		strings.Join(disks, ", ") + "; delete them with gcloud compute disks delete <name> --zone <zone>")
}
