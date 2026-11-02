// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mesh-intelligence/declarative-agents/magefiles/kindrig"
)

const (
	controlPlaneLiveRelease = "cplive"

	// The directory the ingest child reads, inside the creator's work mount. The
	// mount is an emptyDir the container can write, so the target copies the
	// corpus in rather than the chart carrying it: a fixture baked into the chart
	// would ship with every deployment to serve one test.
	controlPlaneLiveIngestDir = "/work/ingest-corpus"

	// The collection rag0 serves, which is what the creator is configured to
	// ingest into. The intent names it, and the creator refuses a mismatch.
	controlPlaneLiveCollection = "corpus"

	controlPlaneLiveReadyWait = 4 * time.Minute

	// The intent is answered only after the ingest child has run and the rollout
	// has drained, so it outlives the shared inference timeout by design. It stays
	// bounded: a wait longer than the creator's own request budget would report a
	// hung mesh as a client timeout and lose which leg stalled.
	controlPlaneLiveIntentWait = 10 * time.Minute
)

// controlPlaneLiveChartConfigMap names the out-of-release ConfigMap carrying the
// packaged chart the applier unpacks at /chart. Provisioning it is not enough on
// its own: the chart mounts it only when applier.chartArchiveConfigMap names it,
// and an install that skipped that value stood the applier up with nothing at
// /chart. Every word then failed on `path "/chart" not found` -- which the
// applier reports as a rejected patch, so the creator's unmapped 400 read as a
// values-schema fault and sent GH-196 looking at the values document (GH-155).
const controlPlaneLiveChartConfigMap = controlPlaneLiveRelease + "-applier-chart"

// controlPlaneLiveTasking asks about something only the ingested fixture says,
// so an answer composed from the pre-existing corpus cannot satisfy it.
const controlPlaneLiveTasking = "What does the knowledge base say about specification-driven development?"

// controlPlaneLiveEvidence is a phrase the ingested documents carry and nothing
// else in the mesh does. A turn that retrieved them composes over it.
//
// The phrase has to be in the document text, not in its file name. The fixture
// file is spec-driven-development.md and its prose says "specification-driven"
// throughout, so the obvious-looking "spec-driven" matched nothing and failed a
// run whose ingest, rollout, and retrieval had all worked. Lowercase, because
// requireComposedSourceEvidence lowercases the document and not this.
const controlPlaneLiveEvidence = "specification-driven"

// ControlPlaneLive drives one ingest-carrying provisioning intent through
// ingest, rollout, and a grounded turn from the newly ingested documents
// (rel05.0-uc001; srd004 R2.1, srd005 R3.1, srd002 R3.3).
//
// integration:controlPlane proves the values-only apply against recording
// stand-ins, and cannot reach this: the ingest leg runs a child process that
// needs a Chroma and an embedding provider, and the rollout it triggers is a
// real Deployment draining real requests. The three legs are covered
// independently by integration:chroma, the deploy swaps, and
// integration:chatbot; what nothing covered is that one intent carries them,
// which is the release's claim.
func (Integration) ControlPlaneLive() error {
	profilesRoot, err := os.Getwd()
	if err != nil {
		return err
	}
	coreRoot := demoCoreRoot(profilesRoot)
	if reason := controlPlaneLiveSkipReason(profilesRoot, coreRoot); reason != "" {
		fmt.Printf("SKIP controlPlaneLive: %s\n", reason)
		return nil
	}
	return runControlPlaneLive(coreRoot, profilesRoot)
}

// controlPlaneLiveSkipReason reports why the live control plane cannot run. It
// names what to supply, because a machine missing a dependency is not evidence
// of a broken mesh.
func controlPlaneLiveSkipReason(profilesRoot, coreRoot string) string {
	if reason := applierLiveSkipReason(coreRoot); reason != "" {
		return reason
	}
	// The turn embeds its query at Cohere and the ingest embeds the documents at
	// the identity rag0 reports, which this scenario sets to the same value.
	// Without the credential neither side can embed, and a run that ingested at
	// one family and queried at another would report every source excluded --
	// a red run that says nothing about the code.
	if reason := cohereCredentialSkipReason(); reason != "" {
		return reason
	}
	required, err := chatbotRequiredModels(profilesRoot)
	if err != nil {
		return fmt.Sprintf("invalid shipped chatbot model config: %v", err)
	}
	return chatbotOllamaSkipReasonForModels(required)
}

func runControlPlaneLive(coreRoot, profilesRoot string) (result error) {
	images, err := resolveChatbotIntegrationImages(profilesRoot)
	if err != nil {
		return err
	}
	fmt.Printf("controlPlaneLive: building runtime image %s from %s\n", images.Runtime, coreRoot)
	if err := buildSmokeRuntimeImage(coreRoot, images.Runtime); err != nil {
		return err
	}
	chartDir := applicationChartDir(profilesRoot)
	staged, cleanupChart, err := stageSmokeChart(chartDir, profilesRoot)
	if err != nil {
		return err
	}
	defer cleanupChart()
	assets, cleanupAssets, err := externalizeUIAssets(staged, controlPlaneLiveRelease)
	if err != nil {
		return err
	}
	defer cleanupAssets()
	profiles, cleanupProfiles, err := externalizeProfiles(staged, controlPlaneLiveRelease)
	if err != nil {
		return err
	}
	defer cleanupProfiles()
	chartArchive, cleanupArchive, err := packageApplierChart(staged)
	if err != nil {
		return err
	}
	defer cleanupArchive()

	dependencyImages, err := applierLiveDependencyImages(chartDir)
	if err != nil {
		return err
	}
	if err := pullIntegrationDependencyImages(
		"controlPlaneLive", dependencyImages, runHelmSmokeCommand); err != nil {
		return err
	}

	cluster, err := acquireIntegrationCluster(profilesRoot)
	if err != nil {
		return err
	}
	commands, cleanupCommands, err := kindrig.ClusterCommands(kindrig.CaptureRun, cluster.Name)
	if err != nil {
		cluster.Release(kindrig.DefaultRun)
		return err
	}
	defer cleanupCommands()

	namespace, cleanupNamespace, err := prepareScenarioNamespace(
		commands.Run, "control-plane-live", controlPlaneLiveRelease)
	if err != nil {
		cluster.Release(kindrig.DefaultRun)
		return err
	}
	cleanupNamespaceFn := cleanupNamespace
	defer func() { result = errors.Join(result, cleanupNamespaceFn()) }()

	evidenceDir := helmScenarioEvidenceDirectory(profilesRoot, cluster.Name, images.Revision)
	evidence := kindrig.FailureEvidence{
		Directory:  evidenceDir,
		Namespaces: []string{namespace},
		Run: boundedHelmEvidenceRunnerWith(
			helmDiagnosticRunner(commands.RunContext), helmEvidenceCommandTimeout),
	}
	defer func() {
		failed := result != nil
		if releaseAggregateKindCluster(cluster, commands.KindRun, evidence, result) {
			if failed && scenarioClusterGone(cluster.Name) {
				cleanupNamespaceFn = func() error { return nil }
			}
			return
		}
		result = errors.Join(result, releaseDirectScenarioCluster(
			cluster, commands.KindRun, failed, evidence, &cleanupNamespaceFn))
	}()

	// The applier runs the runtime image; its helm and kubectl come from the
	// pinned CLI donor (GH-2222).
	if err := loadKindImageWithCommands(commands, cluster.Name, images.Runtime); err != nil {
		return err
	}
	if err := kindrig.EnsureCLIDonorImage(commands.Run, cluster.Name); err != nil {
		return err
	}
	// The dependency images are pulled from a registry rather than built here,
	// so they load through the loader that handles their platform variants.
	for _, image := range dependencyImages {
		if err := loadSmokeDependencyImageWithCommands(commands, cluster.Name, image); err != nil {
			return err
		}
	}

	if err := helmInstallControlPlaneLive(
		commands.Run, staged, chartArchive, images.Runtime, images.Runtime, assets, profiles); err != nil {
		return err
	}
	if err := waitControlPlaneLiveReady(commands.Run); err != nil {
		return err
	}
	return assertControlPlaneLiveIngestToTurn(commands, profilesRoot)
}

// helmInstallControlPlaneLive installs the mesh with the control plane enabled.
// The values layer is a file rather than a wall of --set arguments, so the
// scenario's shape is readable in one place and the values file every other
// cluster test depends on stays untouched.
func helmInstallControlPlaneLive(
	run helmLLMCommandRunner,
	chartPath, chartArchive, runtimeImage, applierImage string,
	assets []externalUIAsset,
	profiles profilesExternalizationResult,
) error {
	args := append(
		[]string{"upgrade", "--install", controlPlaneLiveRelease, chartPath},
		controlPlaneLiveInstallArgs(runtimeImage, applierImage, chartPath)...)
	args = append(args, externalUIAssetValueArgs(assets)...)
	args = append(args, externalProfileValueArgs(profiles)...)
	args = append(args, "--timeout", helmInstallTimeout.String())
	if err := provisionApplierChartConfigMap(
		run, controlPlaneLiveChartConfigMap, chartArchive); err != nil {
		return err
	}
	if err := provisionControlPlaneLiveCredential(run); err != nil {
		return err
	}
	// The UI assets are carried out of release, like the chart archive, and the
	// values above only name them. Without this the collector and observer mount
	// a ConfigMap nobody created and sit in PodInitializing forever -- which the
	// install never notices, because it waits only on the agents this scenario
	// drives. The rollout does notice: helm upgrade --atomic --wait waits on the
	// whole release, so it waited on two pods that could never start and burned
	// the applier's entire apply budget doing it (GH-197).
	for _, asset := range assets {
		if err := provisionExternalUIAssetWithRunner(run, asset); err != nil {
			return err
		}
	}
	if err := provisionExternalProfiles(run, profiles); err != nil {
		return err
	}
	if out, err := run("helm", args...); err != nil {
		return fmt.Errorf("install the control-plane release: %w: %s",
			err, strings.TrimSpace(string(out)))
	}
	return nil
}

// controlPlaneLiveInstallArgs is the scenario's values layer, separated from the
// install so a test can hold it to what the applier needs mounted without a
// cluster.
func controlPlaneLiveInstallArgs(runtimeImage, applierImage, chartPath string) []string {
	return []string{
		"--values", filepath.Join(chartPath, "ci", chatbotDemoValuesFile),
		"--values", filepath.Join(chartPath, "ci", "kind-control-plane-values.yaml"),
		"--set", "image.repository=" + strings.Split(runtimeImage, ":")[0],
		"--set-string", "image.tag=" + imageTagOf(runtimeImage),
		"--set", "image.pullPolicy=Never",
		"--set", "applier.image.repository=" + strings.Split(applierImage, ":")[0],
		"--set-string", "applier.image.tag=" + imageTagOf(applierImage),
		// The applier realizes the rollout leg by running helm against this
		// chart, so the archive has to be mounted as well as provisioned.
		"--set", "applier.chartArchiveConfigMap=" + controlPlaneLiveChartConfigMap,
		// The turn embeds at Cohere, and rag0 must report the identity the ingest
		// embeds the documents at, or srd002 R3.3 excludes the source this target
		// just created.
		"--set", "cohere.enabled=true",
		"--set", "cohere.existingSecret=" + demoCohereSecret,
		"--set", "ragUnits[0].embeddingModel=" + cohereEmbedModelForRelease(chartPath),
		"--set", "llm.externalURL=http://host.docker.internal:11434",
	}
}

// cohereEmbedModelForRelease reads the chart's own embedding model, so the RAG
// unit the ingest writes for and the query side the chatbot embeds at come from
// one declaration rather than a literal repeated in a test.
func cohereEmbedModelForRelease(chartPath string) string {
	if model := demoCohereEmbedModel(chartPath); model != "" {
		return model
	}
	return cohereEmbedDefaultModel
}

// provisionControlPlaneLiveCredential creates the Secret the chart references.
// Delete-then-create rather than apply: an apply would need the credential on a
// manifest, and this way the value reaches kubectl through one argument.
func provisionControlPlaneLiveCredential(run helmLLMCommandRunner) error {
	_, _ = run("kubectl", "delete", "secret", demoCohereSecret, "--ignore-not-found")
	if out, err := run("kubectl", "create", "secret", "generic", demoCohereSecret,
		"--from-literal="+demoCohereSecretKey+"="+cohereCredential()); err != nil {
		return fmt.Errorf("create the Cohere credential Secret: %w: %s",
			err, strings.TrimSpace(string(out)))
	}
	return nil
}

// waitControlPlaneLiveReady gates on every Deployment in the release, not only
// the ones this scenario drives. The rollout leg runs helm upgrade --atomic
// --wait, which waits on all of them, so a workload that is broken but unwatched
// does not fail the install -- it fails the apply, minutes later, as a timeout
// that names nothing (GH-197).
func waitControlPlaneLiveReady(run helmLLMCommandRunner) error {
	for _, deployment := range []string{
		"-chatbot-mesh-applier", "-chatbot-mesh-creator",
		"-chatbot-mesh-provisioning-workflow-orchestrator",
		"-chatbot-mesh-rag0", "-chatbot-mesh-chatbot",
		"-chatbot-mesh-collector", "-chatbot-mesh-observer",
	} {
		name := "deployment/" + controlPlaneLiveRelease + deployment
		if out, err := run("kubectl", "rollout", "status", name,
			"--timeout", controlPlaneLiveReadyWait.String()); err != nil {
			return fmt.Errorf("%s never became ready: %w: %s",
				name, err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// assertControlPlaneLiveIngestToTurn is the tracer: one intent, three legs, and
// an answer that has to come from what the ingest just wrote.
func assertControlPlaneLiveIngestToTurn(commands kindrig.Commands, profilesRoot string) error {
	if err := copyIngestCorpusIntoCreator(commands, profilesRoot); err != nil {
		return err
	}

	intentPorts, err := reserveLoopbackPorts(2)
	if err != nil {
		return err
	}
	intentURL := loopbackURL(intentPorts[0], "/api/v1/provision")

	// The surface is forwarded alongside its control port and gated on its
	// health, because kubectl returns before the tunnel carries traffic and a
	// request issued into an unready forward reads as a refused connection
	// rather than as the timing it is.
	stopIntent, err := kubectlPortForwardPairs(commands,
		"svc/"+controlPlaneLiveRelease+"-chatbot-mesh-provisioning-workflow-orchestrator",
		portForwardPair{local: intentPorts[0], remote: 18100},
		portForwardPair{local: intentPorts[1], remote: 18101})
	if err != nil {
		return err
	}
	defer stopIntent()
	if err := waitHTTPStatus(loopbackURL(intentPorts[1], "/api/lifecycle/health"),
		http.StatusOK, controlPlaneLiveReadyWait); err != nil {
		return fmt.Errorf("the orchestrator control health never answered: %w", err)
	}

	// The creator's monitor is forwarded for the failure path alone. A failed
	// ingest answers command_error and nothing else; the word that failed is only
	// in the creator's events, and a cluster torn down without them leaves a
	// re-run as the only way to learn anything.
	creatorEvents := controlPlaneLiveCreatorEvents(commands)

	// The revision is read before the intent because it is what proves the
	// rollout leg ran: the applier's helm_upgrade moved the release, or it did
	// not, whatever the intent answered.
	before, err := helmReleaseRevision(commands.Run, controlPlaneLiveRelease)
	if err != nil {
		return err
	}

	report, err := postControlPlaneLiveIntent(intentURL, profilesRoot, commands.Run)
	if err != nil {
		return fmt.Errorf("%w\n%s\n%s", err, creatorEvents(),
			controlPlaneLiveChildProbe(commands))
	}
	if err := assertControlPlaneLiveIngested(report); err != nil {
		return fmt.Errorf("%w\n%s", err, creatorEvents())
	}
	fmt.Printf("controlPlaneLive: the intent ingested %s document(s) into %q\n",
		report.Count, report.Collection)

	after, err := helmReleaseRevision(commands.Run, controlPlaneLiveRelease)
	if err != nil {
		return err
	}
	if err := assertControlPlaneLiveRolledOut(before, after); err != nil {
		return fmt.Errorf("%w\n%s", err, creatorEvents())
	}
	fmt.Printf("controlPlaneLive: the same intent rolled the release from revision %d to %d\n",
		before, after)

	return assertControlPlaneLiveGroundedTurnAfterRollout(commands)
}

// assertControlPlaneLiveRolledOut holds the rollout leg to the release actually
// moving. The intent's own 200 cannot carry this: the creator reports the health
// the applier read, and a rollout that never reached helm would still answer with
// a phase. Split out so the judgement is testable without a cluster.
func assertControlPlaneLiveRolledOut(before, after int) error {
	if after <= before {
		return fmt.Errorf(
			"the release is still at revision %d after the intent reported success;"+
				" the applier's helm_upgrade did not reach the release, so the rollout"+
				" leg did not run", after)
	}
	return nil
}

// assertControlPlaneLiveGroundedTurnAfterRollout serves the tasking once the
// rollout has settled. The forward is opened here rather than alongside the
// orchestrator's: the rollout re-rolls the chatbot Deployment, and a tunnel
// opened before it points at a pod the upgrade then deletes, which surfaces as a
// refused connection rather than as the rollout it is.
func assertControlPlaneLiveGroundedTurnAfterRollout(commands kindrig.Commands) error {
	deployment := "deployment/" + controlPlaneLiveRelease + "-chatbot-mesh-chatbot"
	if out, err := commands.Run("kubectl", "rollout", "status", deployment,
		"--timeout", controlPlaneLiveReadyWait.String()); err != nil {
		return fmt.Errorf("the chatbot never became ready after the rollout: %w: %s",
			err, strings.TrimSpace(string(out)))
	}

	chatPorts, err := reserveLoopbackPorts(2)
	if err != nil {
		return err
	}
	stopChat, err := kubectlPortForwardPairs(commands,
		"svc/"+controlPlaneLiveRelease+"-chatbot-mesh-chatbot",
		portForwardPair{local: chatPorts[0], remote: 18080},
		portForwardPair{local: chatPorts[1], remote: 18081})
	if err != nil {
		return err
	}
	defer stopChat()
	if err := waitHTTPStatus(loopbackURL(chatPorts[1], "/api/lifecycle/health"),
		http.StatusOK, controlPlaneLiveReadyWait); err != nil {
		return fmt.Errorf("the chatbot control health never answered: %w", err)
	}
	return assertControlPlaneLiveGroundedTurn(loopbackURL(chatPorts[0], "/api/v1/chat"))
}

// controlPlaneLiveCreatorEvents returns a reader for the creator's monitor
// events. The forward is opened lazily, so a passing run pays nothing for
// diagnostics it never reads.
func controlPlaneLiveCreatorEvents(commands kindrig.Commands) func() string {
	return func() string {
		ports, err := reserveLoopbackPorts(1)
		if err != nil {
			return fmt.Sprintf("creator events unavailable: %v", err)
		}
		stop, err := kubectlPortForwardPairs(commands,
			"svc/"+controlPlaneLiveRelease+"-chatbot-mesh-creator",
			portForwardPair{local: ports[0], remote: 18112})
		if err != nil {
			return fmt.Sprintf("creator events unavailable: %v", err)
		}
		defer stop()
		// The forward returns before it carries traffic, as the surfaces above do.
		if err := waitHTTPStatus(loopbackURL(ports[0], "/monitor/state"),
			http.StatusOK, 30*time.Second); err != nil {
			return fmt.Sprintf("creator monitor never answered: %v", err)
		}
		var report strings.Builder
		for _, route := range []string{"/monitor/events", "/monitor/state"} {
			body, _, err := requestInference(http.MethodGet,
				loopbackURL(ports[0], route), "", "creator monitor "+route)
			if err != nil {
				report.WriteString(fmt.Sprintf("creator %s unavailable: %v\n", route, err))
				continue
			}
			report.WriteString(fmt.Sprintf("creator %s: %s\n",
				route, strings.TrimSpace(string(body))))
		}
		return report.String()
	}
}

// controlPlaneLiveChildProbe reports whether the corpus-ingest child can run in
// the creator at all. An exec word answers ToolFailed the same way whether the
// binary is missing, the profile will not load, or the run failed on its own
// terms, and the child's own stderr does not reach the creator's log -- so a
// failure there is unreadable without asking the container directly.
func controlPlaneLiveChildProbe(commands kindrig.Commands) string {
	pod, err := controlPlaneLiveCreatorPod(commands.Run)
	if err != nil {
		return fmt.Sprintf("child probe unavailable: %v", err)
	}
	var report strings.Builder
	if out, err := commands.Run("kubectl", "exec",
		"deployment/"+controlPlaneLiveRelease+"-chatbot-mesh-applier", "--",
		"sh", "-c", "wget -qO- http://127.0.0.1:18092/monitor/state 2>&1 | tail -c 1200",
	); err == nil {
		report.WriteString("applier state: " +
			strings.ReplaceAll(strings.TrimSpace(string(out)), "\n", " | ") + "\n")
	}
	for _, probe := range [][]string{
		{"sh", "-c", "command -v agent || echo 'agent not on PATH'"},
		{"sh", "-c", "ls /profiles/agents/corpus-ingest/ 2>&1"},
		{"sh", "-c", "ls " + controlPlaneLiveIngestDir + " 2>&1"},
		{"sh", "-c", "ls /profiles/agents/corpus-ingest/ 2>&1 | tr '\\n' ' '"},
	} {
		out, _ := commands.Run("kubectl", append(
			[]string{"exec", pod, "--"}, probe...)...)
		report.WriteString(fmt.Sprintf("child probe %q: %s\n",
			probe[len(probe)-1], strings.TrimSpace(string(out))))
	}
	return report.String()
}

// copyIngestCorpusIntoCreator places the documents where the child reads them.
// The creator's work mount is an emptyDir, so the corpus arrives at run time
// rather than being baked into a chart every deployment would carry.
func copyIngestCorpusIntoCreator(commands kindrig.Commands, profilesRoot string) error {
	pod, err := controlPlaneLiveCreatorPod(commands.Run)
	if err != nil {
		return err
	}
	source := filepath.Join(profilesRoot, chromaCorpusFixture, "corpus")
	if out, err := commands.Run("kubectl", "cp", source,
		pod+":"+controlPlaneLiveIngestDir); err != nil {
		return fmt.Errorf("copy the ingest corpus into %s: %w: %s",
			pod, err, strings.TrimSpace(string(out)))
	}
	fmt.Printf("controlPlaneLive: staged the ingest corpus at %s in %s\n",
		controlPlaneLiveIngestDir, pod)
	return nil
}

func controlPlaneLiveCreatorPod(run helmLLMCommandRunner) (string, error) {
	out, err := run("kubectl", "get", "pods",
		"-l", "app.kubernetes.io/component=creator",
		"-o", "jsonpath={.items[0].metadata.name}")
	if err != nil {
		return "", fmt.Errorf("find the creator pod: %w: %s", err, strings.TrimSpace(string(out)))
	}
	pod := strings.TrimSpace(string(out))
	if pod == "" {
		return "", fmt.Errorf("no creator pod is running; the control plane did not stand up")
	}
	return pod, nil
}

// controlPlaneLiveReport is what the orchestrator answers an ingest-carrying
// intent with.
type controlPlaneLiveReport struct {
	Status     string `json:"status"`
	Error      string `json:"error"`
	Count      string `json:"count"`
	Collection string `json:"collection"`
	Trace      struct {
		Status string `json:"status"`
	} `json:"trace"`
}

// postControlPlaneLiveIntent sends one intent carrying both the ingest request
// and the desired values, which is the join this target exists to prove. Driving
// the legs separately would prove the legs, and they are proven already.
func postControlPlaneLiveIntent(
	intentURL, profilesRoot string,
	run helmLLMCommandRunner,
) (controlPlaneLiveReport, error) {
	values, err := controlPlaneLiveValuesDocument(run)
	if err != nil {
		return controlPlaneLiveReport{}, err
	}
	intent, err := json.Marshal(map[string]string{
		"values":          values,
		"rag_name":        "rag0",
		"collection":      controlPlaneLiveCollection,
		"directory":       controlPlaneLiveIngestDir,
		"embedding_model": cohereEmbedModelForRelease(applicationChartDir(profilesRoot)),
	})
	if err != nil {
		return controlPlaneLiveReport{}, err
	}
	// One intent carries three legs, and the middle one waits on a real rollout:
	// the applier's helm_upgrade runs --atomic --wait against the whole mesh. The
	// shared inference timeout is sized for a model call, so the client gave up
	// while the rollout was still draining and the failure read as an unresponsive
	// orchestrator rather than as the wait it is.
	client := &http.Client{Timeout: controlPlaneLiveIntentWait}
	body, status, err := requestHTTPWithClient(
		client, http.MethodPost, intentURL, string(intent))
	if err != nil {
		return controlPlaneLiveReport{}, fmt.Errorf("provision intent: %w", err)
	}
	var report controlPlaneLiveReport
	if err := json.Unmarshal(body, &report); err != nil {
		return report, fmt.Errorf("decode the intent response (status %d): %w: %s",
			status, err, body)
	}
	if status != http.StatusOK {
		return report, fmt.Errorf(
			"the ingest-carrying intent answered %d, want 200: status=%q error=%q;"+
				" the ingest leg did not complete",
			status, report.Status, report.Error)
	}
	return report, nil
}

// controlPlaneLiveValuesDocument is the desired values the intent carries.
// It repeats what the release already runs, because this target's subject is the
// ingest and the rollout rather than a values change: a rollout that drains
// requests is what the release claims, and it happens whatever the values say.
//
// The document is a patch, not the complete values plane. Both helm words carry
// --reuse-values (agents/applier/exec-declarations.yaml), so what this omits is
// the release's own, and only the lists it names are replaced -- which is why
// testdata/integration/applier-values/conforming.yaml carries ragUnits alone and
// applies cleanly against the live applier. An operator's panel therefore never
// has to round-trip image tags or a ConfigMap name it does not own.
//
// The document is read from the deployed release rather than from the values
// file on disk. The install repoints rag0's embedding model, so a document
// rebuilt from the file would quietly move that unit back to the Ollama family
// and the turn below would find its source excluded -- a values-plane change
// dressed as a no-op. It also has to satisfy the chart schema, which a
// hand-built unit carrying only a name and a collection does not.
func controlPlaneLiveValuesDocument(run helmLLMCommandRunner) (string, error) {
	deployed, err := helmReleaseValues(run, controlPlaneLiveRelease)
	if err != nil {
		return "", err
	}
	// Only the RAG units: that is the values plane the provisioning panel owns
	// and the applier patches. Echoing back the whole computed values -- image
	// tags, chart-archive references, resource blocks -- would re-apply
	// deployment-time configuration as if an operator had asked for it.
	units, ok := deployed["ragUnits"]
	if !ok {
		return "", fmt.Errorf(
			"the deployed release declares no ragUnits; the intent would apply a" +
				" values document the chart schema rejects")
	}
	document, err := json.Marshal(map[string]any{"ragUnits": units})
	if err != nil {
		return "", err
	}
	return string(document), nil
}

// assertControlPlaneLiveIngested holds the ingest leg to the count the creator
// judged and the collection it actually wrote, rather than to the intent having
// been accepted.
func assertControlPlaneLiveIngested(report controlPlaneLiveReport) error {
	if report.Trace.Status != "" && report.Trace.Status != "succeeded" {
		return fmt.Errorf("the intent's trace reports %q, want succeeded", report.Trace.Status)
	}
	count, err := strconv.Atoi(strings.TrimSpace(report.Count))
	if err != nil {
		return fmt.Errorf("the intent reported count %q, which is not a number;"+
			" the ingest leg did not report a judged count", report.Count)
	}
	if count <= 0 {
		return fmt.Errorf("the ingest wrote %d documents; the child ran but added nothing,"+
			" so the turn below would compose from the pre-existing corpus", count)
	}
	if report.Collection != "" && report.Collection != controlPlaneLiveCollection {
		return fmt.Errorf(
			"the ingest wrote collection %q and the intent asked for %q;"+
				" the documents are not in the collection rag0 serves",
			report.Collection, controlPlaneLiveCollection)
	}
	return nil
}

// assertControlPlaneLiveGroundedTurn is the leg the release's claim rests on.
// A 200 carrying fluent prose is what a mesh composing from nothing returns, so
// the answer is held to the documents its sources composed from and to the
// evidence only the ingested fixture carries.
func assertControlPlaneLiveGroundedTurn(chatURL string) error {
	// The agent-driven ingest tags every document it writes with the highest
	// declared level (srd010 R1.3), and a turn with no clearance runs at the
	// lowest (R4.1), so the turn reads as a fully cleared analyst or the newly
	// ingested documents are filtered out before the search.
	body, status, err := requestInferenceWithHeaders(
		http.MethodPost, chatURL,
		fmt.Sprintf(`{"message":%q,"history":[]}`, controlPlaneLiveTasking),
		map[string]string{"X-Analyst-Clearance": ingestDefaultClearance},
		"control-plane live grounded turn")
	if err != nil {
		return fmt.Errorf("serve the tasking: %w", err)
	}
	var resp chatResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return fmt.Errorf("decode the chat response (status %d): %w: %s", status, err, body)
	}
	if err := assertControlPlaneLiveTurnRetrieved(resp, status); err != nil {
		return err
	}
	fmt.Printf("controlPlaneLive: the tasking was answered from %d source(s) over the"+
		" newly ingested documents at identity %q\n",
		len(resp.Metadata.Sources.Composed), resp.Metadata.QueryEmbeddingModel)
	return nil
}

// assertControlPlaneLiveTurnRetrieved is split out so the judgement is testable
// without a cluster.
func assertControlPlaneLiveTurnRetrieved(resp chatResponse, status int) error {
	if status != http.StatusOK {
		return fmt.Errorf("the tasking answered %d, want 200: error=%q message=%q",
			status, resp.Error, resp.Message)
	}
	if strings.TrimSpace(resp.Answer) == "" {
		return fmt.Errorf("the tasking answered 200 with an empty brief")
	}
	if excluded := corpusTurnSourceNames(
		resp.Metadata.Sources.EmbeddingModelExcluded); len(excluded) > 0 {
		return fmt.Errorf(
			"the turn excluded %s on embedding identity; the ingest embedded the"+
				" documents at one family and the query was embedded at %q, so the"+
				" newly added source is unreachable (srd002 R3.3)",
			strings.Join(excluded, ", "), resp.Metadata.QueryEmbeddingModel)
	}
	// The evidence has to be in what a source composed from, not merely in the
	// prose: a model that has read about the subject writes a plausible answer
	// over an empty retrieval.
	if err := requireComposedSourceEvidence(
		resp.Metadata.Sources.Composed, "rag0", controlPlaneLiveEvidence); err != nil {
		return fmt.Errorf("the turn did not compose over the newly ingested documents: %w", err)
	}
	return nil
}

// imageTagOf reports the tag half of a repository:tag reference.
func imageTagOf(image string) string {
	if index := strings.LastIndex(image, ":"); index > 0 {
		return image[index+1:]
	}
	return "latest"
}
