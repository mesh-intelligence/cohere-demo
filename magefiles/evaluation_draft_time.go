// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"fmt"
	"os"
	"time"
)

// First-draft time (srd014 R4): end to end from tasking submission to a
// draft a reviewer accepts, split into the system segment this harness
// measures -- submission to the brief surfaced at the request boundary --
// and the analyst edit segment the corpus records as a stated assumption.
// The half-day baseline is likewise a recorded assumption (R4.3), the
// threshold is ninety minutes total (R4.4), and export time is excluded
// because export runs after approval and off the drafting path (R4.5).

const evaluationDraftTimeThreshold = 90 * time.Minute

// evaluationDraftTimeResult is one tasking's segments. Measured and assumed
// values are labeled apart, so a reader never mistakes the recorded edit
// assumption for a stopwatch.
type evaluationDraftTimeResult struct {
	Tasking        string `json:"tasking"`
	SystemSegment  string `json:"system_segment_measured"`
	EditSegment    string `json:"analyst_edit_segment_assumed"`
	Total          string `json:"total"`
	UnderThreshold bool   `json:"under_threshold"`
	Error          string `json:"error,omitempty"`
}

// evaluationDraftTimeReport folds per-tasking segments against the recorded
// assumptions (R4.2, R4.3): both segments beside every total, the baseline
// named as an assumption, pass or fail against the threshold.
type evaluationDraftTimeReport struct {
	BaselineMinutes    int                         `json:"baseline_minutes_assumed"`
	BaselineBasis      string                      `json:"baseline_basis"`
	EditMinutesAssumed int                         `json:"analyst_edit_minutes_assumed"`
	ThresholdMinutes   int                         `json:"threshold_minutes"`
	ExportExcluded     bool                        `json:"export_time_excluded"`
	PerTasking         []evaluationDraftTimeResult `json:"per_tasking"`
	MaxTotal           string                      `json:"max_total"`
	Failed             int                         `json:"failed_taskings"`
	Pass               bool                        `json:"pass"`
}

// draftTimeReport computes the report from measured system segments. The
// edit segment is the corpus's recorded assumption applied uniformly; a
// tasking whose turn failed carries its error and counts against the run
// rather than vanishing from it (R5.5).
func draftTimeReport(corpus evaluationCorpus, systems map[string]time.Duration, errors map[string]string) evaluationDraftTimeReport {
	report := evaluationDraftTimeReport{
		BaselineMinutes:    corpus.Assumptions.FirstDraftBaselineMinutes,
		BaselineBasis:      "recorded assumption about manual practice; not measured by this harness",
		EditMinutesAssumed: corpus.Assumptions.AnalystEditMinutes,
		ThresholdMinutes:   int(evaluationDraftTimeThreshold.Minutes()),
		ExportExcluded:     true,
		Pass:               true,
	}
	edit := time.Duration(corpus.Assumptions.AnalystEditMinutes) * time.Minute
	var maxTotal time.Duration
	for _, tasking := range corpus.Taskings {
		result := evaluationDraftTimeResult{Tasking: tasking.ID}
		if message, failed := errors[tasking.ID]; failed {
			result.Error = message
			report.Failed++
			report.Pass = false
			report.PerTasking = append(report.PerTasking, result)
			continue
		}
		system := systems[tasking.ID]
		total := system + edit
		result.SystemSegment = system.Round(time.Second).String()
		result.EditSegment = edit.String()
		result.Total = total.Round(time.Second).String()
		result.UnderThreshold = total < evaluationDraftTimeThreshold
		if !result.UnderThreshold {
			report.Pass = false
		}
		if total > maxTotal {
			maxTotal = total
		}
		report.PerTasking = append(report.PerTasking, result)
	}
	report.MaxTotal = maxTotal.Round(time.Second).String()
	if len(report.PerTasking) == 0 {
		report.Pass = false
	}
	return report
}

// DraftTime measures srd014 R4 against the deployed mesh in its default
// shape: one turn per tasking measuring the system segment, totaled with the
// corpus's recorded edit-segment assumption against the ninety-minute
// threshold and the recorded half-day baseline. Skips with the cause named
// when the mesh is absent.
func (Evaluation) DraftTime() error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	base := envOrDefault("CHATBOT_UI_BASE", chatbotUIDefaultBase)
	if err := chatbotUIReachable(base); err != nil {
		fmt.Printf("SKIP evaluation:draftTime: %s does not serve the mesh (%v);"+
			" run mage app:up and mage app:seed first\n", base, err)
		return nil
	}
	corpus, err := loadEvaluationCorpus(root)
	if err != nil {
		return err
	}
	systems, errors := runEvaluationDraftTimings(base, corpus)
	report := draftTimeReport(corpus, systems, errors)
	verdict := "FAIL"
	if report.Pass {
		verdict = "PASS"
	}
	fmt.Printf("evaluation:draftTime %s - max total %s against the %d-minute threshold"+
		" (system segment measured; analyst edit %d min and the %d-minute baseline are recorded"+
		" assumptions; export excluded); %d taskings failed; measured over the mixed"+
		" real-and-synthetic %s v%s\n",
		verdict, report.MaxTotal, report.ThresholdMinutes, report.EditMinutesAssumed,
		report.BaselineMinutes, report.Failed, corpus.ID, corpus.Version)
	if !report.Pass {
		return fmt.Errorf("first-draft time exceeds the threshold or taskings failed (srd014 R4.4)")
	}
	return nil
}

// runEvaluationDraftTimings drives one turn per tasking and keeps the
// wall-clock system segment for each.
func runEvaluationDraftTimings(base string, corpus evaluationCorpus) (map[string]time.Duration, map[string]string) {
	systems := map[string]time.Duration{}
	errors := map[string]string{}
	for index, tasking := range corpus.Taskings {
		if index > 0 {
			time.Sleep(evaluationPace)
		}
		_, elapsed, err := runEvaluationTurn(base, tasking.Text, tasking.Clearance)
		if err != nil {
			errors[tasking.ID] = err.Error()
			fmt.Printf("evaluation: %s draft-time FAILED: %v\n", tasking.ID, err)
			continue
		}
		systems[tasking.ID] = elapsed
		fmt.Printf("evaluation: %s system segment %s\n", tasking.ID, elapsed.Round(time.Second))
	}
	return systems, errors
}
