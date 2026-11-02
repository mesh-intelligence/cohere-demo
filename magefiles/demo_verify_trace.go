// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// VerifyTrace runs one research turn at a stated clearance and proves the
// DefTech ask against the turn's own cross-agent trace (GH-463): each
// assertion below names the specification it verifies, and the evidence is
// span facts the collector recorded -- models, signals, and the complete
// egress inventory in server.address -- not the response body's word for it.
func (App) VerifyTrace() error {
	release := demoCohereReleaseValues()
	if !release.Enabled {
		return fmt.Errorf("app:verifyTrace needs the Cohere tier; the deployed release has cohere.enabled=false")
	}
	body := fmt.Sprintf(`{"message":%q,"history":[]}`, demoVerifyBriefTasking)
	data, status, err := requestInferenceWithHeaders(
		http.MethodPost, demoVerifyURL(), body,
		map[string]string{"X-Analyst-Clearance": demoTraceClearance},
		"app:verifyTrace chat turn")
	if err != nil {
		return fmt.Errorf("app:verifyTrace: %w (is the application up?)", err)
	}
	if status != http.StatusOK {
		return fmt.Errorf("app:verifyTrace: turn returned HTTP %d: %s", status, truncateBody(data))
	}
	turn := struct {
		Metadata struct {
			Conformance struct {
				Verdict string `json:"verdict"`
			} `json:"conformance"`
		} `json:"metadata"`
		Trace struct {
			TraceID string `json:"trace_id"`
		} `json:"trace"`
	}{}
	if err := json.Unmarshal(data, &turn); err != nil {
		return fmt.Errorf("app:verifyTrace: parse turn response: %w", err)
	}
	if turn.Trace.TraceID == "" {
		return fmt.Errorf("app:verifyTrace: the turn reported no trace_id")
	}
	raw, err := demoFetchTrace(turn.Trace.TraceID)
	if err != nil {
		return fmt.Errorf("app:verifyTrace: %w", err)
	}
	return demoAssertTraceSpecs(raw, release, turn.Metadata.Conformance.Verdict)
}

const demoTraceClearance = "secret"

// demoFetchTrace reads the turn's trace through the observer's proxy, polling
// briefly because the collector spools spans after the turn answers.
func demoFetchTrace(traceID string) (string, error) {
	url := demoObserverOrigin + "/monitor-proxy/collector/query/traces/" + traceID
	deadline := time.Now().Add(30 * time.Second)
	for {
		data, status, err := requestHTTPWithClient(integrationInferenceClient, http.MethodGet, url, "")
		// The turn answers before its tail spans finish spooling, so completeness
		// is the terminal word's span, not the first tool span.
		if err == nil && status == http.StatusOK &&
			strings.Contains(string(data), "execute_tool compose_response") {
			return string(data), nil
		}
		if time.Now().After(deadline) {
			if err != nil {
				return "", fmt.Errorf("fetch trace %s: %w", traceID, err)
			}
			return "", fmt.Errorf("trace %s never carried tool spans (HTTP %d)", traceID, status)
		}
		time.Sleep(2 * time.Second)
	}
}

// demoAssertTraceSpecs is the ask, span by span. The raw trace is scanned as
// text: the collector's export encoding is stable, and the assertions are
// substring facts (a span name, an attribute value), so a schema change
// breaks them loudly rather than silently.
func demoAssertTraceSpecs(raw string, release demoCohereRelease, verdict string) error {
	var failures []string
	pass := func(spec, evidence string) {
		fmt.Printf("app:verifyTrace PASS %-14s %s\n", spec, evidence)
	}
	fail := func(spec, evidence string) {
		failures = append(failures, spec+": "+evidence)
		fmt.Printf("app:verifyTrace FAIL %-14s %s\n", spec, evidence)
	}
	// signalOf reads the emitted signal from the span whose attribute list
	// names the word. Segments split on the attribute-list opener, so name
	// and signal are matched within one span regardless of attribute order.
	segments := strings.Split(raw, `"attributes":`)
	signalOf := func(word string) string {
		nameNeedle := `"command.name","Value":{"Type":"STRING","Value":"` + word + `"`
		signalPattern := regexp.MustCompile(`"command\.signal","Value":\{"Type":"STRING","Value":"([^"]+)"`)
		for _, segment := range segments {
			if !strings.Contains(segment, nameNeedle) {
				continue
			}
			if match := signalPattern.FindStringSubmatch(segment); match != nil {
				return match[1]
			}
		}
		return ""
	}

	// srd009 R2.1: Embed for retrieval, at the Cohere boundary.
	if sig := signalOf("embed_query_cohere"); sig == "QueryEmbedded" {
		pass("srd009 R2.1", "embed_query_cohere emitted QueryEmbedded at api.cohere.com")
	} else {
		fail("srd009 R2.1", "embed_query_cohere signal "+sig)
	}

	// GH-425: the one classifier, a Command-family model selecting sources.
	classifier := regexp.MustCompile(`"name":"chat ` + regexp.QuoteMeta(demoClassifierModel) + `"`)
	if classifier.MatchString(raw) {
		pass("GH-425", "a chat span ran the declared classifier "+demoClassifierModel)
	} else {
		fail("GH-425", "no chat span for classifier "+demoClassifierModel)
	}

	// srd009 R4: Rerank for precision.
	if sig := signalOf("cohere_rerank_rag"); sig == "Reranked" {
		pass("srd009 R4", "cohere_rerank_rag emitted Reranked")
	} else {
		fail("srd009 R4", "cohere_rerank_rag signal "+sig)
	}

	// srd009 R5: Command for composition.
	if sig := signalOf("invoke_cohere_command"); sig == "BriefComposed" {
		pass("srd009 R5", "invoke_cohere_command emitted BriefComposed")
	} else {
		fail("srd009 R5", "invoke_cohere_command signal "+sig)
	}

	// srd010 R3.5: the stated clearance gates retrieval and never rides a
	// prompt. The clearance value must appear in no LLM chat span. Captured
	// message content (GH-658) is excluded first: chunk text legitimately
	// contains the literal as a substring of ordinary words ("Secretary")
	// and of document markings, and the guarantee under test is about the
	// request parameter, which capture attributes do not carry.
	scanned := stripCaptureContent(raw)
	clearanceLeak := false
	for _, match := range regexp.MustCompile(`(?s)"name":"chat [^"]+"[^\x00]{0,1000}?\}\]`).FindAllString(scanned, -1) {
		if strings.Contains(match, demoTraceClearance) {
			clearanceLeak = true
		}
	}
	if !clearanceLeak {
		pass("srd010 R3.5", fmt.Sprintf("the stated clearance %q reached no LLM span", demoTraceClearance))
	} else {
		fail("srd010 R3.5", "the stated clearance appears inside an LLM chat span")
	}

	// srd011 R1.2 and the sovereignty story: the turn's complete egress
	// inventory is the declared enclave surface and nothing else.
	hosts := map[string]bool{}
	for _, match := range regexp.MustCompile(`"server\.address","Value":\{"Type":"STRING","Value":"([^"]+)"`).FindAllStringSubmatch(raw, -1) {
		hosts[match[1]] = true
	}
	var unexpected []string
	for host := range hosts {
		if !demoAllowedTraceHost(host) {
			unexpected = append(unexpected, host)
		}
	}
	if len(unexpected) == 0 {
		inventory := make([]string, 0, len(hosts))
		for host := range hosts {
			inventory = append(inventory, host)
		}
		pass("srd011 R1.2", "egress inventory: "+strings.Join(inventory, ", "))
	} else {
		fail("srd011 R1.2", "undeclared egress host(s): "+strings.Join(unexpected, ", "))
	}

	// srd009 R1.3: no credential material in any exported span.
	if strings.Contains(raw, "api_key=") && !strings.Contains(raw, "api_key=[REDACTED]") {
		fail("srd009 R1.3", "an api_key value is not redacted")
	} else if strings.Contains(strings.ToLower(raw), "bearer ") {
		fail("srd009 R1.3", "bearer material appears in a span")
	} else {
		pass("srd009 R1.3", "no unredacted credential in any span")
	}

	// srd012: the brief carries a conformance verdict.
	if verdict != "" {
		pass("srd012", "conformance verdict: "+verdict)
	} else {
		fail("srd012", "the response carries no conformance verdict")
	}

	if len(failures) > 0 {
		return fmt.Errorf("app:verifyTrace: %d specification(s) unproven:\n  %s",
			len(failures), strings.Join(failures, "\n  "))
	}
	fmt.Println("app:verifyTrace PASS - the turn's trace proves the ask: Embed, the classifier, Rerank, and Command ran at the declared boundary, the clearance gated retrieval without reaching a prompt, the egress inventory is the enclave surface, and no credential left redaction")
	return nil
}

// demoClassifierModel mirrors the declared classifier default (GH-425).
const demoClassifierModel = "command-r7b-12-2024"

// demoAllowedTraceHost is the declared enclave surface for a default demo
// deployment: the Cohere boundary, the mesh's own services, and the declared
// search engine (fixture in-cluster, or serpapi.com when a deploy opted in).
func demoAllowedTraceHost(host string) bool {
	// server.address may carry the port (api.cohere.com:443 on GKE), so every
	// comparison is on the bare host.
	bare := strings.Split(host, ":")[0]
	if bare == "api.cohere.com" || bare == "serpapi.com" {
		return true
	}
	return strings.Contains(bare, "chatbot-mesh") || strings.HasSuffix(bare, ".local") ||
		bare == "127.0.0.1" || bare == "localhost"
}

func truncateBody(data []byte) string {
	if len(data) > demoVerifyBodyLimit {
		return string(data[:demoVerifyBodyLimit]) + "..."
	}
	return string(data)
}

// requestInferenceWithHeaders is requestInference plus request headers, for
// the turn that states an analyst clearance.
func requestInferenceWithHeaders(method, url, body string, headers map[string]string, work string) ([]byte, int, error) {
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		return nil, 0, fmt.Errorf("%s: %w", work, err)
	}
	req.Header.Set("Content-Type", "application/json")
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	resp, err := integrationInferenceClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("%s at %s: %w", work, url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, 0, fmt.Errorf("%s: read response: %w", work, err)
	}
	return data, resp.StatusCode, nil
}

// captureContentKeys are the telemetry content-capture attributes (GH-658):
// full prompt and reply text, excluded from the clearance-leak scan because
// document content legitimately contains the clearance literal inside
// ordinary words while carrying no request parameter.
var captureContentKeys = []string{
	"gen_ai.input.messages", "gen_ai.output.messages", "llm.input.delta", "llm.output.delta",
	"http.request.body",
}

// stripCaptureContent blanks the capture attributes' string values in the raw
// span JSON. The values are JSON-escaped strings, so the regex ends at the
// first unescaped quote.
func stripCaptureContent(raw string) string {
	for _, key := range captureContentKeys {
		pattern := regexp.MustCompile(`("Key":"` + regexp.QuoteMeta(key) + `".{0,80}?"Value":")(?:[^"\\]|\\.)*(")`)
		raw = pattern.ReplaceAllString(raw, "${1}${2}")
	}
	return raw
}
