// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// The rel13.0 tracer (rel13.0-uc001, srd014). integration:evaluation drives
// the shipped turn through its ordinary request boundary for every corpus
// tasking and composes the three metrics into one report: retrieval recall
// with and without the rerank pass, citation coverage under the declared
// segmentation, and first-draft time in measured and assumed segments. The
// report carries per-tasking results beside every aggregate (R5.3), the
// corpus version, run configuration, and model identities (R5.4), failed
// taskings as failures rather than omissions (R5.5), and the mixed
// real-and-synthetic qualification in the output itself (R6.1, srd017
// R9.4).
//
// The target's verdict is measurement soundness, and the threshold verdicts
// are statements inside the report. srd014 R2.5, R3.5, and R4.4 require
// pass or fail REPORTED against each threshold, and R6.2 requires the
// evaluation described as evidence that the method measures rather than as
// an operational result -- so a threshold miss with a measured explanation
// (the recall bound the deployed retrieval breadth imposes, GH-336) is a
// finding the report states, while a tasking that failed to measure, a
// missing metric, or an unexpected deployment shape fails the target. The
// GH-336 decision on retrieval breadth can tighten this to threshold-gating
// without touching the measurements.

type evaluationReport struct {
	Qualification struct {
		SyntheticNotice string `json:"synthetic_notice"`
		Framing         string `json:"framing"`
		ScopeExclusions string `json:"scope_exclusions"`
		CustomerClaim   string `json:"customer_claim"`
	} `json:"qualification"`
	Corpus struct {
		ID                     string `json:"id"`
		Version                string `json:"version"`
		RetrievalCorpus        string `json:"retrieval_corpus"`
		RetrievalCorpusVersion string `json:"retrieval_corpus_version"`
	} `json:"corpus"`
	Models struct {
		Embedding string `json:"query_embedding"`
		Rerank    string `json:"rerank"`
		Command   string `json:"command"`
	} `json:"models"`
	Recall struct {
		Default       evaluationRecallRun `json:"deployed_configuration"`
		WithRerank    evaluationRecallRun `json:"with_rerank"`
		WithoutRerank evaluationRecallRun `json:"without_rerank"`
		// Ordering delta at the same N: rerank@N minus cosine@N (GH-576).
		// The old figure compared recall@N against the untrimmed superset,
		// which measured the cutoff, not the ordering.
		Contribution float64 `json:"contribution"`
		// Recall over the untrimmed fan-out: what breadth alone reaches
		// before any trim (GH-576).
		RetrievalBoundary float64 `json:"retrieval_boundary"`
		// Recall@N divided by each tasking's achievable ceiling at N (GH-576).
		Normalised   float64 `json:"normalised"`
		Threshold    float64 `json:"threshold"`
		ThresholdMet bool    `json:"threshold_met"`
		BoundNote    string  `json:"bound_note"`
	} `json:"recall"`
	Coverage struct {
		PerBrief     []evaluationCoverageResult `json:"per_brief"`
		Average      float64                    `json:"average"`
		Threshold    string                     `json:"threshold"`
		ThresholdMet bool                       `json:"threshold_met"`
		ShortBriefs  []string                   `json:"short_briefs"`
	} `json:"coverage"`
	DraftTime evaluationDraftTimeReport `json:"first_draft_time"`
	Soundness struct {
		FailedTaskings int  `json:"failed_taskings"`
		Sound          bool `json:"sound"`
	} `json:"measurement"`
}

// declaredModel reads one ${VAR:-default} model declaration from the
// committed rest.yaml, so the reported identity is the deployed declaration.
func declaredModel(root, variable string) string {
	data, err := os.ReadFile(filepath.Join(root, "agents", "chatbot", "rest.yaml"))
	if err != nil {
		return "unknown"
	}
	match := regexp.MustCompile(`\$\{` + variable + `:-([^}]+)\}`).FindSubmatch(data)
	if match == nil {
		return "unknown"
	}
	return string(match[1])
}

// Evaluation proves the rel13.0 release against the deployed mesh: one
// default-shape pass measuring recall, coverage, and draft time from the
// same turns, then the steered with/without-rerank pair, folded into the
// qualified report beside the printed summary. Skips naming COHERE_API_KEY
// when the mesh answers on the pre-provider tier, which composes no
// citations to measure.
func (Integration) Evaluation() error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	base := envOrDefault("CHATBOT_UI_BASE", chatbotUIDefaultBase)
	if err := chatbotUIReachable(base); err != nil {
		fmt.Printf("SKIP integration:evaluation: %s does not serve the mesh (%v);"+
			" run mage app:up and mage app:seed first\n", base, err)
		return nil
	}
	corpus, err := loadEvaluationCorpus(root)
	if err != nil {
		return err
	}

	var report evaluationReport
	report.Qualification.SyntheticNotice = corpus.SyntheticNotice
	report.Qualification.Framing = "Method evidence: these figures show the method measures what it claims to measure" +
		" over a demonstration corpus. They are not an operational performance result (srd014 R6.2)."
	report.Qualification.ScopeExclusions = "This evaluation carries no clearance-isolation and no template-conformance claim;" +
		" the srd010 access-control test and the srd012 conformance check prove those (srd014 R6.3)."
	report.Qualification.CustomerClaim = "Thresholds met or missed here are met or missed over this corpus;" +
		" re-running the method against a customer corpus is the step that produces a customer claim (srd014 R6.4)."
	report.Corpus.ID = corpus.ID
	report.Corpus.Version = corpus.Version
	report.Corpus.RetrievalCorpus = corpus.RetrievalCorpus
	report.Corpus.RetrievalCorpusVersion = corpus.RetrievalCorpusVersion
	report.Models.Embedding = declaredModel(root, "COHERE_EMBED_MODEL")
	report.Models.Rerank = declaredModel(root, "COHERE_RERANK_MODEL")
	report.Models.Command = declaredModel(root, "COHERE_CHAT_MODEL")

	// Pass one, default shape: coverage, draft time, and deployed-shape
	// recall from the same turns -- one measurement per tasking serves three
	// metrics, so the corpus is driven once per shape rather than once per
	// metric.
	fmt.Println("integration:evaluation: default-shape pass (coverage, draft time, deployed recall)")
	n := declaredCompositionBudget(root)
	defaultRecall := evaluationRecallRun{}
	defaultRecall.Configuration.Rerank = "cascade (rag, search, fusion)"
	defaultRecall.Configuration.Branches = []string{"rag", "osint"}
	defaultRecall.Configuration.N = n
	systems := map[string]time.Duration{}
	failures := map[string]string{}
	var recallSum, coverageSum float64
	var counted int
	probe := true
	for index, tasking := range corpus.Taskings {
		if index > 0 {
			time.Sleep(evaluationPace)
		}
		turn, elapsed, err := runEvaluationTurn(base, tasking.Text, tasking.Clearance)
		if err != nil {
			failures[tasking.ID] = err.Error()
			report.Coverage.PerBrief = append(report.Coverage.PerBrief,
				evaluationCoverageResult{Tasking: tasking.ID, Error: err.Error()})
			defaultRecall.PerTasking = append(defaultRecall.PerTasking,
				evaluationRecallResult{Tasking: tasking.ID, Error: err.Error()})
			defaultRecall.Failed++
			fmt.Printf("integration:evaluation: %s FAILED: %v\n", tasking.ID, err)
			continue
		}
		if probe {
			probe = false
			if turn.Metadata.Rerank == "" {
				fmt.Println("SKIP integration:evaluation: the mesh answers on the pre-provider tier," +
					" which composes no citations to measure; export COHERE_API_KEY and redeploy (GH-318)")
				return nil
			}
		}
		systems[tasking.ID] = elapsed
		if defaultRecall.Configuration.EmbeddingModel == "" {
			defaultRecall.Configuration.EmbeddingModel = turn.Metadata.QueryEmbeddingModel
		}
		recallResult := evaluationRecallResult{
			Tasking: tasking.ID, Clearance: tasking.Clearance, Relevant: len(tasking.Relevant),
			Composed: len(turn.Metadata.Composition.Documents),
			System:   elapsed.Round(time.Second).String(),
		}
		recallResult.Recall, recallResult.Missing = taskingRecall(tasking, composedDocuments(turn))
		defaultRecall.PerTasking = append(defaultRecall.PerTasking, recallResult)
		recallSum += recallResult.Recall
		coverage := coverageForTurn(tasking.ID, turn)
		report.Coverage.PerBrief = append(report.Coverage.PerBrief, coverage)
		coverageSum += coverage.Coverage
		if coverage.Coverage < 1.0 {
			report.Coverage.ShortBriefs = append(report.Coverage.ShortBriefs,
				fmt.Sprintf("%s (%.2f, %d/%d, answered_by=%s, %d unresolved)",
					tasking.ID, coverage.Coverage, coverage.Covered, coverage.Assertions,
					coverage.AnsweredBy, coverage.Unresolved))
		}
		counted++
		fmt.Printf("integration:evaluation: %s recall %.2f coverage %.2f system %s (answered_by=%s)\n",
			tasking.ID, recallResult.Recall, coverage.Coverage,
			elapsed.Round(time.Second), coverage.AnsweredBy)
	}
	if counted > 0 {
		defaultRecall.Average = recallSum / float64(counted)
		report.Coverage.Average = coverageSum / float64(counted)
	}
	report.Recall.Default = defaultRecall
	report.Coverage.Threshold = "complete coverage"
	report.Coverage.ThresholdMet = len(report.Coverage.ShortBriefs) == 0 && counted > 0
	report.DraftTime = draftTimeReport(corpus, systems, failures)

	// Pass two and three: the R2.3 with/without-rerank pair over the steered
	// shapes, restored before the report is written.
	withRerank, withoutRerank, err := runEvaluationRecallPair(base, root, "", corpus)
	if err != nil {
		return err
	}
	report.Recall.WithRerank = withRerank
	report.Recall.WithoutRerank = withoutRerank
	report.Recall.Contribution = withRerank.Average - withoutRerank.AverageAtK
	report.Recall.RetrievalBoundary = withoutRerank.Average
	report.Recall.Normalised = withRerank.AverageNormalised
	report.Recall.Threshold = evaluationRecallThreshold
	report.Recall.ThresholdMet = withRerank.Average >= evaluationRecallThreshold
	report.Recall.BoundNote = fmt.Sprintf(
		"The deployed retrieval breadth (n_results per source) bounds recall at composition:"+
			" when it does not exceed the composition budget N=%s, the rerank trims nothing and"+
			" recall is a property of retrieval breadth against the labeled set sizes (GH-336).", n)

	report.Soundness.FailedTaskings = len(failures) + withRerank.Failed + withoutRerank.Failed
	report.Soundness.Sound = report.Soundness.FailedTaskings == 0 && counted == len(corpus.Taskings)

	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	reportPath := filepath.Join(root, "evaluation", "report.json")
	if err := os.WriteFile(reportPath, encoded, 0o644); err != nil {
		return err
	}

	verdict := "FAIL"
	if report.Soundness.Sound {
		verdict = "PASS"
	}
	fmt.Printf("integration:evaluation %s - every figure measured over the mixed real-and-synthetic %s v%s"+
		" (report: %s).\n  recall: rerank@N %.3f, cosine@N %.3f, ordering delta %+.3f, retrieval boundary %.3f,"+
		" normalised %.3f, threshold %.2f %s\n"+
		"  coverage: average %.3f, %d briefs short of complete\n"+
		"  first-draft time: max total %s against %d minutes, %s\n"+
		"  %s\n",
		verdict, corpus.ID, corpus.Version, reportPath,
		report.Recall.WithRerank.Average, report.Recall.WithoutRerank.AverageAtK, report.Recall.Contribution,
		report.Recall.RetrievalBoundary, report.Recall.Normalised,
		report.Recall.Threshold, statedVerdict(report.Recall.ThresholdMet),
		report.Coverage.Average, len(report.Coverage.ShortBriefs),
		report.DraftTime.MaxTotal, report.DraftTime.ThresholdMinutes, statedVerdict(report.DraftTime.Pass),
		report.Qualification.Framing)
	if !report.Soundness.Sound {
		return fmt.Errorf("the measurement is unsound: %d taskings failed to measure (srd014 R5.5)",
			report.Soundness.FailedTaskings)
	}
	return nil
}

func statedVerdict(met bool) string {
	if met {
		return "met over this corpus"
	}
	return "not met over this corpus"
}
