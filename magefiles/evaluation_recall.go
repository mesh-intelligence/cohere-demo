// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/magefile/mage/mg"
)

// Retrieval recall (srd014 R2): the fraction of a tasking's labeled relevant
// documents appearing in the set handed to composition, read from the
// response's composition metadata, averaged over the corpus, and reported
// twice over the same taskings -- with the rerank pass and without it -- so
// Rerank's contribution is a measured difference (R2.3).
//
// Both configurations run with the search branch disabled through the
// air-gap steer the chart itself renders. The labeled corpus names internal
// documents only, so the fixture results could never count toward recall;
// left enabled they occupy composition slots in one configuration and not
// the other, which would fold branch noise into a rerank comparison. The
// disablement is recorded in each figure's configuration (R2.4), and the
// steers ride the same full-data profile-key rewrite the release tracers
// use, restored on every path (R5.1: the harness drives the shipped turn and
// modifies no committed artifact; it selects between shapes the chart
// renders).

const (
	evaluationFanoutKey = "agents__chatbot__request-fanout-declarations.yaml"

	// The RAG rerank's order-check signals; swapping them routes every
	// ordering to the degraded leg, which composes the fan-out order
	// untrimmed -- the without-rerank configuration.
	evaluationRagCheckEnabled  = "      satisfied: RerankOrderEmpty\n      unsatisfied: RerankOrderPresent"
	evaluationRagCheckInverted = "      satisfied: RerankOrderPresent\n      unsatisfied: RerankOrderEmpty"

	// Paced apart for the same reason the tracers pace: an evaluation turn
	// spends several provider calls, and a throttled pass turns a measurement
	// into noise the per-tasking report then has to explain.
	evaluationPace = 60 * time.Second

	evaluationRecallThreshold = 0.90
)

type evaluationTurn struct {
	Answer   string `json:"answer"`
	Metadata struct {
		AnsweredBy          string `json:"answered_by"`
		Rerank              string `json:"rerank"`
		Fusion              string `json:"fusion"`
		QueryEmbeddingModel string `json:"query_embedding_model"`
		ClearanceEnforcedAt string `json:"clearance_enforced_at"`
		Osint               struct {
			Status string `json:"status"`
			Rerank string `json:"rerank"`
		} `json:"osint"`
		Composition struct {
			Documents []string `json:"documents"`
		} `json:"composition"`
		// Raw, decoded leniently: on the fallback and uncited paths the mesh
		// renders citations as an empty string rather than an empty list (the
		// SPA's ?? [] coalescing absorbs the same wart), and a strict array
		// decode failed 18 of 20 taskings on the first paced run. Recall does
		// not read citations; the field is kept only for inspection.
		Citations json.RawMessage `json:"citations"`
	} `json:"metadata"`
}

// runEvaluationTurn drives one shipped-boundary turn at the tasking's own
// clearance, retrying transport errors a rollout leaves behind.
func runEvaluationTurn(base, tasking, clearance string) (evaluationTurn, time.Duration, error) {
	body, err := json.Marshal(map[string]interface{}{"message": tasking, "history": []interface{}{}})
	if err != nil {
		return evaluationTurn{}, 0, err
	}
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		var turn evaluationTurn
		request, err := http.NewRequest(http.MethodPost, base+conformanceChatEndpoint, bytes.NewReader(body))
		if err != nil {
			return turn, 0, err
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Analyst-Clearance", clearance)
		client := &http.Client{Timeout: conformanceTurnTimeout}
		started := time.Now()
		response, err := client.Do(request)
		elapsed := time.Since(started)
		if err == nil && response.StatusCode == http.StatusOK {
			err = json.NewDecoder(response.Body).Decode(&turn)
			_ = response.Body.Close()
			if err == nil {
				return turn, elapsed, nil
			}
			lastErr = err
		} else if err == nil {
			_ = response.Body.Close()
			lastErr = fmt.Errorf("chat returned status %d", response.StatusCode)
		} else {
			lastErr = err
		}
		time.Sleep(45 * time.Second)
	}
	return evaluationTurn{}, 0, lastErr
}

// composedDocuments maps the composition ids onto the document identities the
// labels are written at.
func composedDocuments(turn evaluationTurn) map[string]bool {
	documents := map[string]bool{}
	for _, id := range turn.Metadata.Composition.Documents {
		documents[parentDocumentID(id)] = true
	}
	return documents
}

// taskingRecall is |labeled ∩ composed| / |labeled| for one turn (R2.1).
func taskingRecall(tasking evaluationTasking, composed map[string]bool) (float64, []string) {
	var hit int
	var missing []string
	for _, relevant := range tasking.Relevant {
		if composed[relevant.ID] {
			hit++
		} else {
			missing = append(missing, relevant.ID)
		}
	}
	return float64(hit) / float64(len(tasking.Relevant)), missing
}

// recallCeiling is the best any at-N figure can reach for one tasking:
// min(n, relevant)/relevant (GH-576). A non-positive n means untrimmed, whose
// ceiling is 1.
func recallCeiling(n, relevant int) float64 {
	if relevant <= 0 {
		return 1
	}
	if n <= 0 || n >= relevant {
		return 1
	}
	return float64(n) / float64(relevant)
}

// taskingRecallAtK scores recall over the first k composed chunk ids only, in
// the order composition received them. On the untrimmed configuration that
// order is the cosine order, so one run yields both the retrieval boundary
// and the same-K comparator (GH-576).
func taskingRecallAtK(tasking evaluationTasking, ids []string, k int) float64 {
	if k > 0 && k < len(ids) {
		ids = ids[:k]
	}
	composed := map[string]bool{}
	for _, id := range ids {
		composed[parentDocumentID(id)] = true
	}
	recall, _ := taskingRecall(tasking, composed)
	return recall
}

// evaluationRecallResult is one tasking under one configuration, kept beside
// the aggregate so every number is inspectable down to the tasking that
// produced it (R5.3), and a failed tasking is reported rather than dropped
// from the denominator (R5.5).
type evaluationRecallResult struct {
	Tasking   string  `json:"tasking"`
	Clearance string  `json:"clearance"`
	Recall    float64 `json:"recall"`
	// Recall over the first N composed chunks in the order composition
	// received them (GH-576). On the untrimmed run that order is the cosine
	// order, so this is the same-K comparator R2.3 needs; zero on the trimmed
	// run, whose Recall is already at N.
	RecallAtK float64 `json:"recall_at_k,omitempty"`
	// Recall divided by the tasking's achievable ceiling at N,
	// min(N, relevant)/relevant, so a 10-document set scored at N=8 reads
	// against 0.8 rather than an unreachable 1.0 (GH-576).
	Normalised float64  `json:"recall_normalised"`
	Relevant   int      `json:"relevant"`
	Composed   int      `json:"composed"`
	Missing    []string `json:"missing,omitempty"`
	System     string   `json:"system_segment"`
	Error      string   `json:"error,omitempty"`
}

// evaluationRecallRun is one configuration's figures with everything R2.4 and
// R5.4 require a figure to name.
type evaluationRecallRun struct {
	Configuration struct {
		Rerank         string   `json:"rerank"`
		Branches       []string `json:"branches"`
		N              string   `json:"n"`
		Clearances     []string `json:"clearance_levels"`
		EmbeddingModel string   `json:"query_embedding_model"`
		InputTypeNote  string   `json:"input_type_note"`
	} `json:"configuration"`
	PerTasking []evaluationRecallResult `json:"per_tasking"`
	Average    float64                  `json:"average_recall"`
	// The same-K aggregate over RecallAtK (GH-576); zero on the trimmed run.
	AverageAtK float64 `json:"average_recall_at_k,omitempty"`
	// The ceiling-normalised aggregate over Normalised (GH-576).
	AverageNormalised float64 `json:"average_recall_normalised"`
	Failed            int     `json:"failed_taskings"`
}

// declaredCompositionBudget reads N from the one rerank operation's declared
// top_n, so the reported N is the deployed declaration rather than a copy.
func declaredCompositionBudget(root string) string {
	data, err := os.ReadFile(root + "/agents/chatbot/rest.yaml")
	if err != nil {
		return "unknown"
	}
	match := regexp.MustCompile(`top_n: \$\{COHERE_RERANK_TOP_N:-(\d+)\}`).FindSubmatch(data)
	if match == nil {
		return "unknown"
	}
	return string(match[1])
}

// runEvaluationRecallConfig drives every tasking once under the deployment
// shape currently applied and folds the figures.
func runEvaluationRecallConfig(base, rerankMode, n string, corpus evaluationCorpus, deriveAtK bool) evaluationRecallRun {
	run := evaluationRecallRun{}
	run.Configuration.Rerank = rerankMode
	run.Configuration.Branches = []string{"rag"}
	run.Configuration.N = n
	run.Configuration.InputTypeNote = "recall regressions: check the embedding input_type wiring first (srd014 R2.6)"
	k, _ := strconv.Atoi(n)
	levels := map[string]bool{}
	var sum, sumAtK, sumNorm float64
	var counted int
	for index, tasking := range corpus.Taskings {
		if index > 0 {
			time.Sleep(evaluationPace)
		}
		levels[tasking.Clearance] = true
		result := evaluationRecallResult{
			Tasking: tasking.ID, Clearance: tasking.Clearance, Relevant: len(tasking.Relevant),
		}
		turn, elapsed, err := runEvaluationTurn(base, tasking.Text, tasking.Clearance)
		if err != nil {
			result.Error = err.Error()
			run.Failed++
			run.PerTasking = append(run.PerTasking, result)
			fmt.Printf("evaluation: %s [%s] FAILED: %v\n", tasking.ID, rerankMode, err)
			continue
		}
		if run.Configuration.EmbeddingModel == "" {
			run.Configuration.EmbeddingModel = turn.Metadata.QueryEmbeddingModel
		}
		composed := composedDocuments(turn)
		result.Composed = len(turn.Metadata.Composition.Documents)
		result.Recall, result.Missing = taskingRecall(tasking, composed)
		ceiling := recallCeiling(k, len(tasking.Relevant))
		if deriveAtK {
			// Untrimmed run: Recall is the retrieval boundary; the first-N
			// truncation of the composition order is the cosine@N comparator.
			result.RecallAtK = taskingRecallAtK(tasking, turn.Metadata.Composition.Documents, k)
			result.Normalised = result.RecallAtK / ceiling
			sumAtK += result.RecallAtK
		} else {
			result.Normalised = result.Recall / ceiling
		}
		result.System = elapsed.Round(time.Second).String()
		sum += result.Recall
		sumNorm += result.Normalised
		counted++
		run.PerTasking = append(run.PerTasking, result)
		fmt.Printf("evaluation: %s [%s] recall %.2f (%d composed, rerank=%s, answered_by=%s)\n",
			tasking.ID, rerankMode, result.Recall, result.Composed, turn.Metadata.Rerank, turn.Metadata.AnsweredBy)
	}
	for level := range levels {
		run.Configuration.Clearances = append(run.Configuration.Clearances, level)
	}
	if counted > 0 {
		run.Average = sum / float64(counted)
		run.AverageNormalised = sumNorm / float64(counted)
		if deriveAtK {
			run.AverageAtK = sumAtK / float64(counted)
		}
	}
	return run
}

type Evaluation mg.Namespace

// Recall measures srd014 R2 against the deployed mesh: recall at the
// composition top-N with the rerank pass and without it, over the same
// taskings, search branch disabled in both shapes so the comparison holds
// the branch set equal. Skips with the cause named when the mesh is absent.
// runEvaluationRecallPair applies the two deployment shapes, measures each,
// and restores the default on every path. Shared by the standalone target and
// the integration:evaluation report.
func runEvaluationRecallPair(base, root, nOverride string, corpus evaluationCorpus) (evaluationRecallRun, evaluationRecallRun, error) {
	var with, without evaluationRecallRun
	osintDecl, err := osintConfigMapKey(osintDeclarationsKey)
	if err != nil {
		return with, without, err
	}
	fanoutDecl, err := osintConfigMapKey(evaluationFanoutKey)
	if err != nil {
		return with, without, err
	}
	if !strings.Contains(osintDecl, osintEnabledOperand) || !strings.Contains(fanoutDecl, evaluationRagCheckEnabled) {
		return with, without, fmt.Errorf("the deployed profiles are not in the default shape; redeploy with mage app:up first")
	}
	n := declaredCompositionBudget(root)
	if nOverride != "" {
		// A swept configuration deploys a top_n the repo does not declare
		// (GH-576); the caller stamps the value it patched in.
		n = nOverride
	}
	restore := func() error {
		if err := osintPatchKeys(map[string]string{
			osintDeclarationsKey: osintDecl,
			evaluationFanoutKey:  fanoutDecl,
		}); err != nil {
			return err
		}
		return osintRollChatbot()
	}

	// With rerank: the air-gapped shape, RAG cascade intact, composition
	// trimmed to the declared N.
	fmt.Println("evaluation: recall with-rerank configuration (search branch disabled)")
	disabled := strings.Replace(osintDecl, osintEnabledOperand, osintDisabledOperand, 1)
	if err := osintPatchKeys(map[string]string{osintDeclarationsKey: disabled}); err != nil {
		return with, without, err
	}
	if err := osintRollChatbot(); err != nil {
		return with, without, err
	}
	with = runEvaluationRecallConfig(base, "applied", n, corpus, false)

	// Without rerank: the same shape with the RAG order check inverted, so
	// every turn composes the fan-out order untrimmed. The comparison is the
	// trim-versus-order trade Rerank makes, over an identical branch set.
	fmt.Println("evaluation: recall without-rerank configuration")
	inverted := strings.Replace(fanoutDecl, evaluationRagCheckEnabled, evaluationRagCheckInverted, 1)
	if err := osintPatchKeys(map[string]string{evaluationFanoutKey: inverted}); err != nil {
		if restoreErr := restore(); restoreErr != nil {
			return with, without, fmt.Errorf("%w (and restore failed: %v)", err, restoreErr)
		}
		return with, without, err
	}
	if err := osintRollChatbot(); err != nil {
		if restoreErr := restore(); restoreErr != nil {
			return with, without, fmt.Errorf("%w (and restore failed: %v)", err, restoreErr)
		}
		return with, without, err
	}
	// The untrimmed run carries the same declared N as the trimmed one
	// (GH-576): its Average is the retrieval boundary, its AverageAtK the
	// cosine order truncated to that N -- like compares with like.
	without = runEvaluationRecallConfig(base, "disabled (untrimmed fan-out order)", n, corpus, true)

	return with, without, restore()
}

// Recall measures srd014 R2 against the deployed mesh: recall at the
// composition top-N with the rerank pass and without it, over the same
// taskings, search branch disabled in both shapes so the comparison holds
// the branch set equal. Skips with the cause named when the mesh is absent,
// and errors when the with-rerank figure misses the ninety-percent
// threshold; the integration:evaluation report instead states that verdict
// beside the measured retrieval bound (GH-336).
// selectedEvaluationCorpora resolves the EVAL_CORPUS selector (srd014 R7.4):
// "analyst" (the default, the rel13.0 behavior), "generated", or "both". Each
// corpus keeps its own figures and its own denominator; nothing is combined.
func selectedEvaluationCorpora(root, selector string) ([]evaluationCorpus, error) {
	var corpora []evaluationCorpus
	if selector == "analyst" || selector == "both" {
		corpus, err := loadEvaluationCorpus(root)
		if err != nil {
			return nil, err
		}
		corpora = append(corpora, corpus)
	}
	if selector == "generated" || selector == "both" {
		generated, err := loadGeneratedEvaluationCorpus(root)
		if err != nil {
			return nil, err
		}
		corpora = append(corpora, generated.asEvaluationCorpus())
	}
	if len(corpora) == 0 {
		return nil, fmt.Errorf("%s=%q: want analyst, generated, or both", evaluationCorpusEnv, selector)
	}
	return corpora, nil
}

// applyEvaluationSlice keeps the first n taskings. The slice is declared and
// deterministic -- first-n, never re-sampled -- and the caller reports it, so
// a sliced figure cannot pose as the full corpus's.
func applyEvaluationSlice(corpus evaluationCorpus, n int) evaluationCorpus {
	if n <= 0 || n >= len(corpus.Taskings) {
		return corpus
	}
	corpus.Taskings = corpus.Taskings[:n]
	return corpus
}

const (
	evaluationCorpusEnv = "EVAL_CORPUS"
	evaluationSliceEnv  = "EVAL_SLICE"
)

func (Evaluation) Recall() error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	base := envOrDefault("CHATBOT_UI_BASE", chatbotUIDefaultBase)
	if err := chatbotUIReachable(base); err != nil {
		fmt.Printf("SKIP evaluation:recall: %s does not serve the mesh (%v);"+
			" run mage app:up and mage app:seed first\n", base, err)
		return nil
	}
	corpora, err := selectedEvaluationCorpora(root, envOrDefault(evaluationCorpusEnv, "analyst"))
	if err != nil {
		return err
	}
	slice := 0
	if raw := envOrDefault(evaluationSliceEnv, ""); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			slice = parsed
		}
	}
	var failures []string
	for _, corpus := range corpora {
		total := len(corpus.Taskings)
		corpus = applyEvaluationSlice(corpus, slice)
		sliceNote := ""
		if len(corpus.Taskings) < total {
			sliceNote = fmt.Sprintf(" [declared slice: first %d of %d]", len(corpus.Taskings), total)
		}
		withRerank, withoutRerank, err := runEvaluationRecallPair(base, root, "", corpus)
		if err != nil {
			return err
		}
		pass := withRerank.Average >= evaluationRecallThreshold
		verdict := "FAIL"
		if pass {
			verdict = "PASS"
		}
		// Four figures, each labeled for what it is (GH-576): recall@N under
		// the rerank order and under the cosine order (the pair that isolates
		// ordering), the untrimmed retrieval boundary (what breadth alone
		// reaches), and the ceiling-normalised figure (recall against what N
		// makes achievable). The boundary minus recall@N is the cost of the
		// trim, never a rerank "contribution".
		fmt.Printf("evaluation:recall %s [%s v%s]%s - rerank@%s %.3f (threshold %.2f, normalised %.3f),"+
			" cosine@%s %.3f (ordering delta %+.3f), retrieval boundary %.3f (trim cost %.3f),"+
			" branches=%v; %d/%d taskings failed; measured over the mixed real-and-synthetic %s v%s\n",
			verdict, corpus.ID, corpus.Version, sliceNote,
			withRerank.Configuration.N, withRerank.Average, evaluationRecallThreshold, withRerank.AverageNormalised,
			withoutRerank.Configuration.N, withoutRerank.AverageAtK, withRerank.Average-withoutRerank.AverageAtK,
			withoutRerank.Average, withoutRerank.Average-withRerank.Average,
			withRerank.Configuration.Branches,
			withRerank.Failed+withoutRerank.Failed, 2*len(corpus.Taskings),
			corpus.ID, corpus.Version)
		if !pass {
			failures = append(failures, fmt.Sprintf("%s v%s: %.3f", corpus.ID, corpus.Version, withRerank.Average))
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("recall with rerank under the %.2f threshold (srd014 R2.5): %s",
			evaluationRecallThreshold, strings.Join(failures, "; "))
	}
	return nil
}
