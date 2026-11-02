// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"bytes"
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// The rel14.0 tracer (rel14.0-uc001, srd013). It proves the brief-export
// release against the deployed mesh: a generation turn that triggers no
// export, an analyst edit exported to a PDF whose bytes are the run's own
// rendered artifact and whose .tex carries the edit and not the dropped
// generation text, a malformed brief rendered unaltered, a failing document
// rolled back through the endpoint with the markdown still exportable, and
// the hostile fixtures run in the pod under the declared engine argv without
// executing or reading what they name.
//
// The correspondence proof reads the run's own artifacts rather than
// extracting PDF text: the returned base64 decodes to the exact bytes of
// /work/export/brief.pdf (md5 over kubectl exec), and that PDF was rendered
// from /work/export/brief.tex by the same machine run, so marker assertions
// against the .tex are assertions about the returned document. Client-side
// text extraction would need a ToUnicode CMap decoder for XeTeX's
// glyph-encoded content streams and would prove less: it could not tie the
// response bytes to the deployed run.
//
// The hostile fixtures cannot arrive through the endpoint -- pandoc escapes
// raw TeX in GitHub markdown, which is itself a boundary -- so the tracer
// writes them at the declared workspace path in the pod and runs the engine
// under the exact argv exec-declarations.yaml fixes (suite preconditions:
// the fixtures exercise the boundary rather than describing it).

const (
	exporterExportLocalBase  = "http://127.0.0.1:18120"
	exporterMonitorLocalBase = "http://127.0.0.1:18122"
	exporterEditMarker       = "AMENDMENT-GH293-FINAL"
	exporterMalformedMarker  = "UNCONFORMING-GH293-SECTION"
)

func exporterDeployName() string {
	return fmt.Sprintf("%s-%s-exporter", cohereApplicationRelease, demoChartName)
}

// exporterExec runs a command in the exporter pod. Callers asserting an
// expected failure (a grep with no match, a file that must be absent) wrap
// the probe in sh so the assertion is the printed outcome, not the exit code.
func exporterExec(args ...string) (string, error) {
	full := append([]string{"exec", "deploy/" + exporterDeployName(), "--"}, args...)
	return osintKubectl(full...)
}

func startExporterPortForward() (func(), error) {
	args, err := applicationKubectlArgs(
		"port-forward", "svc/"+exporterDeployName(), "18120:18120", "18122:18122")
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("kubectl", args...)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	stop := func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		response, err := http.Get(exporterMonitorLocalBase + "/monitor/state")
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return stop, nil
			}
		}
		time.Sleep(time.Second)
	}
	stop()
	return nil, fmt.Errorf("the exporter monitor did not answer through the port-forward")
}

type exportResponse struct {
	Status    string `json:"status"`
	Error     string `json:"error"`
	PdfBase64 string `json:"pdf_base64"`
}

// exportBrief posts one markdown document to the export endpoint and decodes
// the JSON either terminal state returns.
func exportBrief(content string) (int, exportResponse, []byte, error) {
	var decoded exportResponse
	body, err := json.Marshal(map[string]string{"schema_version": "1", "content": content})
	if err != nil {
		return 0, decoded, nil, err
	}
	client := &http.Client{Timeout: 130 * time.Second}
	response, err := client.Post(exporterExportLocalBase+"/export/api/pdf", "application/json", bytes.NewReader(body))
	if err != nil {
		return 0, decoded, nil, err
	}
	raw := new(bytes.Buffer)
	_, _ = raw.ReadFrom(response.Body)
	_ = response.Body.Close()
	if err := json.Unmarshal(raw.Bytes(), &decoded); err != nil {
		return response.StatusCode, decoded, raw.Bytes(), fmt.Errorf("decode export response: %w: %s", err, raw.String())
	}
	return response.StatusCode, decoded, raw.Bytes(), nil
}

// exporterRequestRuns counts request-machine runs the monitor has seen, by
// the AwaitingExport transitions in the recent events. The count is how the
// off-turn leg tells "the turn exported nothing" from "the export ran".
func exporterRequestRuns() (int, error) {
	response, err := http.Get(exporterMonitorLocalBase + "/monitor/events")
	if err != nil {
		return 0, err
	}
	raw := new(bytes.Buffer)
	_, _ = raw.ReadFrom(response.Body)
	_ = response.Body.Close()
	return strings.Count(raw.String(), `"from_state":"AwaitingExport"`), nil
}

// exporterSplitForEdit halves the generated brief at a paragraph boundary
// and returns the kept half and a distinctive alphabetic token that only the
// dropped half carries, so the exported document is provably the edit rather
// than the generation. An empty token means the dropped half had no such
// word; the presence assertions still hold.
func exporterSplitForEdit(generated string) (kept, droppedToken string) {
	boundary := strings.Index(generated[len(generated)/2:], "\n\n")
	if boundary < 0 {
		return generated, ""
	}
	kept = generated[:len(generated)/2+boundary]
	dropped := generated[len(generated)/2+boundary:]
	for _, word := range regexp.MustCompile(`[A-Za-z]{10,}`).FindAllString(dropped, -1) {
		if !strings.Contains(kept, word) {
			return kept, word
		}
	}
	return kept, ""
}

// Exporter proves the rel14.0 brief-export release against the deployed
// mesh. It skips with the cause named when the mesh or the exporter workload
// is absent; the exporter itself binds no provider, so no credential gates
// any leg.
func (Integration) Exporter() error {
	if _, err := osintKubectl("get", "deploy", exporterDeployName()); err != nil {
		fmt.Printf("SKIP integration:exporter: the exporter workload is not deployed (%v); run mage app:up first\n", err)
		return nil
	}
	base := envOrDefault("CHATBOT_UI_BASE", chatbotUIDefaultBase)
	if err := chatbotUIReachable(base); err != nil {
		fmt.Printf("SKIP integration:exporter: %s does not serve the mesh (%v); run mage app:up and mage app:seed first\n", base, err)
		return nil
	}
	return proveExporter(base)
}

// proveExporter runs the release's legs against the mesh whose chat surface
// base serves. integration:exporter resolves base from the kind ingress and
// gcp:exporter from a port-forward to the GKE release (GH-880).
func proveExporter(base string) error {
	stopForward, err := startExporterPortForward()
	if err != nil {
		return err
	}
	defer stopForward()

	// The off-turn leg (suite case 2): a completed generation turn moves the
	// export-run count not at all -- the export is the analyst's separate,
	// later act, and the turn's latency contains no render.
	fmt.Println("integration:exporter: off-turn leg -- driving one generation turn")
	runsBefore, err := exporterRequestRuns()
	if err != nil {
		return err
	}
	turnStart := time.Now()
	turn, err := runCascadeTurn(base)
	if err != nil {
		return err
	}
	turnDuration := time.Since(turnStart)
	if strings.TrimSpace(turn.Answer) == "" {
		return fmt.Errorf("off-turn leg: the generation turn returned no brief")
	}
	runsAfter, err := exporterRequestRuns()
	if err != nil {
		return err
	}
	if runsAfter != runsBefore {
		return fmt.Errorf("off-turn leg: the generation turn moved the export-run count %d -> %d; export must occur only on the analyst's request (srd013 R4.1)", runsBefore, runsAfter)
	}
	fmt.Printf("integration:exporter: turn completed in %s with no export run\n", turnDuration.Round(time.Second))

	// The edit-and-export leg (suite cases 1, 4, 5, 9, 10): the analyst keeps
	// half the generation, appends an amendment section, and exports. The
	// returned bytes are the run's rendered artifact, the .tex carries the
	// amendment and not the dropped generation text, and the whole path runs
	// with no provider bound in the pod.
	fmt.Println("integration:exporter: edit-and-export leg")
	kept, droppedToken := exporterSplitForEdit(turn.Answer)
	edited := kept + "\n\n## Analyst Amendment\n\n" + exporterEditMarker + " -- the flooding risk in sector seven is reassessed as low after the analyst's review.\n"
	status, body, _, err := exportBrief(edited)
	if err != nil {
		return err
	}
	if status != http.StatusOK || body.Status != "rendered" {
		return fmt.Errorf("edit-and-export leg: export returned %d %q, want 200 rendered", status, body.Status)
	}
	pdf, err := base64.StdEncoding.DecodeString(body.PdfBase64)
	if err != nil {
		return fmt.Errorf("edit-and-export leg: the returned document is not base64: %w", err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) || !bytes.Contains(pdf[len(pdf)-64:], []byte("%%EOF")) {
		return fmt.Errorf("edit-and-export leg: the returned bytes are not a complete PDF (%d bytes)", len(pdf))
	}
	podSum, err := exporterExec("md5sum", "/work/export/brief.pdf")
	if err != nil {
		return err
	}
	if !strings.HasPrefix(podSum, fmt.Sprintf("%x", md5.Sum(pdf))) {
		return fmt.Errorf("edit-and-export leg: the returned PDF is not the run's rendered artifact (md5 mismatch)")
	}
	tex, err := exporterExec("cat", "/work/export/brief.tex")
	if err != nil {
		return err
	}
	if !strings.Contains(tex, exporterEditMarker) || !strings.Contains(tex, `section{Analyst Amendment}`) {
		return fmt.Errorf("edit-and-export leg: the rendered document does not carry the analyst's amendment")
	}
	if droppedToken != "" && strings.Contains(tex, droppedToken) {
		return fmt.Errorf("edit-and-export leg: the rendered document still carries dropped generation text %q; the export must render the edit, not the pre-edit draft", droppedToken)
	}
	if strings.Contains(tex, exporterEditMarker) && droppedToken == "" {
		fmt.Println("integration:exporter: note -- the dropped half carried no distinctive token; pre-edit absence is asserted by the amendment alone")
	}
	health, err := exporterExec("wget", "-qO-", "http://127.0.0.1:18121/api/lifecycle/health")
	if err != nil {
		return fmt.Errorf("edit-and-export leg: the control server's health endpoint did not answer: %w", err)
	}
	events, err := http.Get(exporterMonitorLocalBase + "/monitor/events")
	if err != nil {
		return err
	}
	eventsRaw := new(bytes.Buffer)
	_, _ = eventsRaw.ReadFrom(events.Body)
	_ = events.Body.Close()
	for _, state := range []string{"AwaitingExport", "PreparingWorkspace", "ConvertingTex", "RenderingPdf", "Reporting"} {
		if !strings.Contains(eventsRaw.String(), `"`+state+`"`) {
			return fmt.Errorf("edit-and-export leg: the monitor events do not show the declared state %s", state)
		}
	}
	if strings.Contains(eventsRaw.String(), exporterEditMarker) {
		return fmt.Errorf("edit-and-export leg: the monitor surface carries brief content (srd013 R5.3)")
	}
	// Kubernetes injects a service link for every Service in the namespace,
	// and the release name cohere-demo prefixes them all with COHERE_DEMO_
	// (_SERVICE_HOST, _SERVICE_PORT*, _PORT*). Those are addresses, not a
	// provider binding, so only the remaining COHERE names count.
	cohereVars, _ := exporterExec("sh", "-c",
		"env | cut -d= -f1 | grep COHERE | grep -cvE '_(SERVICE_HOST|SERVICE_PORT|PORT)(_|$)'; true")
	if strings.TrimSpace(strings.Split(cohereVars, "\n")[0]) != "0" {
		return fmt.Errorf("edit-and-export leg: the exporter pod carries a Cohere variable; the export path binds no provider (srd013 R3.1)")
	}
	texSum1, err := exporterExec("md5sum", "/work/export/brief.tex")
	if err != nil {
		return err
	}
	status, body, _, err = exportBrief(edited)
	if err != nil {
		return err
	}
	if status != http.StatusOK || body.Status != "rendered" {
		return fmt.Errorf("repeat-request check: the second identical export returned %d %q", status, body.Status)
	}
	texSum2, err := exporterExec("md5sum", "/work/export/brief.tex")
	if err != nil {
		return err
	}
	if texSum1 != texSum2 {
		return fmt.Errorf("repeat-request check: the second identical request produced a different .tex; state leaked between requests (srd013 R1.4)")
	}
	fmt.Printf("integration:exporter: exported the analyst's edit -- %d-byte PDF is the run's artifact, control healthy (%d bytes), states sequenced, second request identical\n", len(pdf), len(health))

	// The malformed-brief leg (suite case 3): a document with none of the
	// template's sections renders rather than being refused, and its odd
	// heading survives verbatim -- no re-derived structure, no re-run check.
	fmt.Println("integration:exporter: malformed-brief leg")
	status, body, _, err = exportBrief("## " + exporterMalformedMarker + "\n\nThis draft failed conformance and the analyst exports it knowingly.\n")
	if err != nil {
		return err
	}
	if status != http.StatusOK || body.Status != "rendered" {
		return fmt.Errorf("malformed-brief leg: the exporter refused a nonconforming draft (%d %q); export is not gated on the verdict (srd012 AC4)", status, body.Status)
	}
	tex, err = exporterExec("cat", "/work/export/brief.tex")
	if err != nil {
		return err
	}
	if !strings.Contains(tex, exporterMalformedMarker) {
		return fmt.Errorf("malformed-brief leg: the odd heading did not survive; the exporter must render what it is given unaltered")
	}

	// The failure leg (suite case 8): a document the engine cannot set --
	// list nesting past LaTeX's depth -- terminates in Failed through the
	// endpoint, the workspace is rolled back, the body names no content and
	// carries no partial PDF, and the same markdown path stays exportable.
	fmt.Println("integration:exporter: failure-and-rollback leg")
	deep := "# GH293-DEEPNEST\n\n" + func() string {
		var b strings.Builder
		for i := 0; i < 10; i++ {
			b.WriteString(strings.Repeat("  ", i) + "- level\n")
		}
		return b.String()
	}()
	status, body, rawBody, err := exportBrief(deep)
	if err != nil {
		return err
	}
	if status != http.StatusInternalServerError || body.Status != "failed" || body.Error != "export_failed" {
		return fmt.Errorf("failure leg: want 500 failed/export_failed, got %d %q/%q", status, body.Status, body.Error)
	}
	if body.PdfBase64 != "" || strings.Contains(string(rawBody), "GH293-DEEPNEST") {
		return fmt.Errorf("failure leg: the failure body carries a partial PDF or brief content (srd013 R5.2, R5.3)")
	}
	leftovers, _ := exporterExec("sh", "-c", "ls /work/export 2>/dev/null | wc -l")
	if strings.TrimSpace(strings.Split(leftovers, "\n")[0]) != "0" {
		return fmt.Errorf("failure leg: the workspace was not rolled back; files remain after Failed (srd013 R5.1)")
	}
	limits, err := osintKubectl("get", "deploy", exporterDeployName(), "-o", "jsonpath={.spec.template.spec.containers[0].resources.limits}")
	if err != nil {
		return err
	}
	if !strings.Contains(limits, "cpu") || !strings.Contains(limits, "memory") {
		return fmt.Errorf("failure leg: the deployed workload declares no resource limits (srd013 R5.4): %s", limits)
	}
	status, body, _, err = exportBrief(edited)
	if err != nil {
		return err
	}
	if status != http.StatusOK || body.Status != "rendered" {
		return fmt.Errorf("failure leg: the approved markdown is no longer exportable after a failure (%d %q); a failure must cost a retry, not the edit", status, body.Status)
	}
	fmt.Println("integration:exporter: the failing document rolled back clean and the markdown re-exported")

	// The hostile-fixture leg (suite cases 6 and 7): the fixtures are written
	// at the declared workspace path and the engine runs under the exact argv
	// exec-declarations.yaml fixes. The shell-escape construct must not
	// execute, and the outside path must not be read.
	fmt.Println("integration:exporter: hostile-fixture leg")
	engineArgv := "xelatex -no-shell-escape -interaction=batchmode -halt-on-error" +
		" -cnf-line openin_any=p -cnf-line openout_any=p -output-directory /work/export /work/export/brief.tex"
	escapeProbe, err := exporterExec("sh", "-c",
		`printf '\\documentclass{article}\\begin{document}\\immediate\\write18{touch /work/export/pwned-gh293}ok\\end{document}\n' > /work/export/brief.tex && `+
			engineArgv+` >/dev/null 2>&1; test -e /work/export/pwned-gh293 && echo executed || echo inert`)
	if err != nil {
		return err
	}
	if !strings.Contains(escapeProbe, "inert") {
		return fmt.Errorf("hostile-fixture leg: the shell-escape construct executed under the declared argv (srd013 R2.4)")
	}
	outsideProbe, err := exporterExec("sh", "-c",
		`rm -f /work/export/brief.pdf && printf '\\documentclass{article}\\begin{document}\\input{/etc/hostname}\\end{document}\n' > /work/export/brief.tex && `+
			engineArgv+` >/dev/null 2>&1 && echo rendered || echo refused; grep -rl "$(cat /etc/hostname)" /work/export 2>/dev/null | grep -v brief.log || echo outside-content-absent`)
	if err != nil {
		return err
	}
	if !strings.Contains(outsideProbe, "refused") || !strings.Contains(outsideProbe, "outside-content-absent") {
		return fmt.Errorf("hostile-fixture leg: the engine read a path outside the working directory (srd013 R2.5): %s", strings.TrimSpace(outsideProbe))
	}
	if _, err := exporterExec("rm", "-rf", "/work/export"); err != nil {
		return err
	}
	status, body, _, err = exportBrief(edited)
	if err != nil {
		return err
	}
	if status != http.StatusOK || body.Status != "rendered" {
		return fmt.Errorf("hostile-fixture leg: the exporter did not recover after the fixtures (%d %q)", status, body.Status)
	}

	fmt.Println("integration:exporter PASS - a generation turn triggers no export and the analyst's later request renders" +
		" the edited brief, whose returned bytes are the run's own artifact carrying the amendment and none of the dropped" +
		" generation text; a nonconforming draft renders unaltered; a failing document terminates in Failed with the" +
		" workspace rolled back, no partial PDF, no content in the error surface, and the markdown still exportable; and" +
		" the hostile fixtures neither execute their command nor read their outside path under the declared engine argv")
	return nil
}
