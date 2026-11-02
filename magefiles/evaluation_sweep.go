// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The retrieval-parameter sweep (GH-576, srd014 E7): vary the deployed
// pipeline, never the corpus. Each configuration patches the rendered
// literals the chart wrote into the profiles ConfigMap -- the same patch-and-
// roll mechanism the recall pair itself uses -- runs the corrected pair, and
// appends its row to the committed report, so an interrupted sweep keeps its
// partial evidence. The original key contents are captured before the first
// patch and restored on every path.

const (
	evaluationRestKey = "agents__chatbot__rest.yaml"

	evalSweepBreadthsEnv = "EVAL_SWEEP_BREADTHS"
	evalSweepTopNsEnv    = "EVAL_SWEEP_TOPNS"
	evalSweepCascadesEnv = "EVAL_SWEEP_CASCADES"

	// The cascade operand the render writes (srd011 R6.4). With the search
	// branch disabled -- which the pair enforces for branch parity -- the
	// collapse leg has nothing to fuse, so the cascade axis is expected to be
	// flat there; the sweep still exercises it so that expectation is a
	// measured row rather than an assumption.
	evalCascadeThreePass    = "left: cascade-three-pass"
	evalCascadeSingleFusion = "left: cascade-single-fusion"
)

type evaluationSweepRow struct {
	Breadth   string  `json:"retrieval_breadth"`
	TopN      string  `json:"rerank_top_n"`
	Cascade   string  `json:"cascade"`
	Corpus    string  `json:"corpus"`
	Version   string  `json:"corpus_version"`
	Slice     string  `json:"slice,omitempty"`
	RerankAtN float64 `json:"rerank_at_n"`
	CosineAtN float64 `json:"cosine_at_n"`
	// rerank_at_n minus cosine_at_n: the ordering effect at equal N.
	OrderingDelta float64 `json:"ordering_delta"`
	// Recall over the untrimmed fan-out: what breadth alone reaches.
	Boundary   float64 `json:"retrieval_boundary"`
	Normalised float64 `json:"normalised"`
	Failed     int     `json:"failed_taskings"`
	MeasuredAt string  `json:"measured_at"`
}

func splitSweepList(env, fallback string) []string {
	var out []string
	for _, part := range strings.Split(envOrDefault(env, fallback), ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// applySweepShape patches the rendered breadth, top_n, and cascade literals
// from their current rendered values to the requested ones and rolls.
func applySweepShape(restOriginal, osintOriginal, breadth, topN, cascade string,
	renderedBreadth, renderedTopN string) error {
	rest := strings.ReplaceAll(restOriginal, "n_results: "+renderedBreadth, "n_results: "+breadth)
	rest = strings.ReplaceAll(rest, "top_n: "+renderedTopN, "top_n: "+topN)
	osint := osintOriginal
	if cascade == "single_fusion" {
		osint = strings.Replace(osint, evalCascadeThreePass, evalCascadeSingleFusion, 1)
	}
	if err := osintPatchKeys(map[string]string{
		evaluationRestKey:    rest,
		osintDeclarationsKey: osint,
	}); err != nil {
		return err
	}
	return osintRollChatbot()
}

// renderedLiteral pulls the current value of a rendered `prefix: value` line.
func renderedLiteral(content, prefix string) string {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, prefix+": ") {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, prefix+": "))
		}
	}
	return ""
}

func appendSweepReport(root string, rows []evaluationSweepRow, reading string) error {
	dir := filepath.Join(root, "evaluation")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "recall-sweep.json"), encoded, 0o644); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("# Recall sweep (GH-576)\n\n")
	b.WriteString(reading + "\n\n")
	b.WriteString("| breadth | top_n | cascade | corpus | rerank@N | cosine@N | ordering Δ | boundary | normalised | failed |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |\n")
	for _, row := range rows {
		b.WriteString(fmt.Sprintf("| %s | %s | %s | %s v%s%s | %.3f | %.3f | %+.3f | %.3f | %.3f | %d |\n",
			row.Breadth, row.TopN, row.Cascade, row.Corpus, row.Version, row.Slice,
			row.RerankAtN, row.CosineAtN, row.OrderingDelta, row.Boundary, row.Normalised, row.Failed))
	}
	return os.WriteFile(filepath.Join(dir, "recall-sweep.md"), []byte(b.String()), 0o644)
}

// Sweep runs the corrected recall pair over a matrix of deployed retrieval
// parameters (GH-576): EVAL_SWEEP_BREADTHS x EVAL_SWEEP_TOPNS x
// EVAL_SWEEP_CASCADES, over the EVAL_CORPUS selection with EVAL_SLICE
// respected. Parameters are swept, never the corpus (srd014 E7). Rows land in
// evaluation/recall-sweep.{json,md} after every configuration, and the
// deployed shape is restored on every path.
func (Evaluation) Sweep() error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	base := envOrDefault("CHATBOT_UI_BASE", chatbotUIDefaultBase)
	if err := chatbotUIReachable(base); err != nil {
		fmt.Printf("SKIP evaluation:sweep: %s does not serve the mesh (%v)\n", base, err)
		return nil
	}
	corpora, err := selectedEvaluationCorpora(root, envOrDefault(evaluationCorpusEnv, "generated"))
	if err != nil {
		return err
	}
	slice := 0
	if raw := envOrDefault(evaluationSliceEnv, ""); raw != "" {
		fmt.Sscanf(raw, "%d", &slice)
	}
	breadths := splitSweepList(evalSweepBreadthsEnv, "10,20,40")
	topNs := splitSweepList(evalSweepTopNsEnv, "8,12,16")
	cascades := splitSweepList(evalSweepCascadesEnv, "three_pass,single_fusion")

	restOriginal, err := osintConfigMapKey(evaluationRestKey)
	if err != nil {
		return err
	}
	osintOriginal, err := osintConfigMapKey(osintDeclarationsKey)
	if err != nil {
		return err
	}
	renderedBreadth := renderedLiteral(restOriginal, "n_results")
	renderedTopN := renderedLiteral(restOriginal, "top_n")
	if renderedBreadth == "" || renderedTopN == "" {
		return fmt.Errorf("the rendered rest key carries no n_results/top_n literals; redeploy with mage app:up first")
	}
	restore := func() error {
		if err := osintPatchKeys(map[string]string{
			evaluationRestKey:    restOriginal,
			osintDeclarationsKey: osintOriginal,
		}); err != nil {
			return err
		}
		return osintRollChatbot()
	}

	var rows []evaluationSweepRow
	reading := "Sweep in progress; the reading paragraph is written when the matrix completes."
	for _, cascade := range cascades {
		for _, breadth := range breadths {
			for _, topN := range topNs {
				fmt.Printf("evaluation:sweep configuration breadth=%s top_n=%s cascade=%s\n", breadth, topN, cascade)
				if err := applySweepShape(restOriginal, osintOriginal, breadth, topN, cascade, renderedBreadth, renderedTopN); err != nil {
					if restoreErr := restore(); restoreErr != nil {
						return fmt.Errorf("%w (and restore failed: %v)", err, restoreErr)
					}
					return err
				}
				for _, corpus := range corpora {
					total := len(corpus.Taskings)
					sliced := applyEvaluationSlice(corpus, slice)
					sliceNote := ""
					if len(sliced.Taskings) < total {
						sliceNote = fmt.Sprintf(" (first %d of %d)", len(sliced.Taskings), total)
					}
					with, without, err := runEvaluationRecallPair(base, root, topN, sliced)
					if err != nil {
						if restoreErr := restore(); restoreErr != nil {
							return fmt.Errorf("%w (and restore failed: %v)", err, restoreErr)
						}
						return err
					}
					rows = append(rows, evaluationSweepRow{
						Breadth: breadth, TopN: topN, Cascade: cascade,
						Corpus: sliced.ID, Version: sliced.Version, Slice: sliceNote,
						RerankAtN: with.Average, CosineAtN: without.AverageAtK,
						OrderingDelta: with.Average - without.AverageAtK,
						Boundary:      without.Average, Normalised: with.AverageNormalised,
						Failed:     with.Failed + without.Failed,
						MeasuredAt: time.Now().UTC().Format(time.RFC3339),
					})
					if err := appendSweepReport(root, rows, reading); err != nil {
						fmt.Printf("evaluation:sweep report write failed: %v\n", err)
					}
				}
			}
		}
	}
	if err := restore(); err != nil {
		return err
	}
	reading = "Reading: compare the boundary column against rerank@N per configuration --" +
		" where the boundary is high and rerank@N is low the gap is ordering or trim, and the" +
		" ordering Δ column says which; where the boundary itself is low, the miss is retrieval" +
		" and no ordering fix can recover it. The cascade axis is expected flat here because the" +
		" pair disables the search branch for parity; a single_fusion row that differs from its" +
		" three_pass twin is measurement noise, not a fusion effect."
	return appendSweepReport(root, rows, reading)
}
