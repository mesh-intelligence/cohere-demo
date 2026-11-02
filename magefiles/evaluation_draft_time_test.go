// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"strings"
	"testing"
	"time"
)

// First-draft time (srd014 R4): segment arithmetic and the labeling that
// keeps a recorded assumption from reading as a measurement.

func draftTimeFixtureCorpus() evaluationCorpus {
	var corpus evaluationCorpus
	corpus.Assumptions.FirstDraftBaselineMinutes = 240
	corpus.Assumptions.AnalystEditMinutes = 20
	for _, id := range []string{"t-fast", "t-slow", "t-broken"} {
		corpus.Taskings = append(corpus.Taskings, evaluationTasking{ID: id})
	}
	return corpus
}

// TestDraftTimeReportSeparatesMeasuredFromAssumed is srd014 R4.2 and R4.3:
// both segments and the total ride every row, the measured field carries the
// stopwatch, the assumed fields carry the recorded assumptions, and the
// baseline names itself an assumption.
func TestDraftTimeReportSeparatesMeasuredFromAssumed(t *testing.T) {
	t.Parallel()
	corpus := draftTimeFixtureCorpus()
	report := draftTimeReport(corpus,
		map[string]time.Duration{"t-fast": 45 * time.Second, "t-slow": 80 * time.Minute},
		map[string]string{"t-broken": "chat returned status 500"})
	if report.BaselineMinutes != 240 || !strings.Contains(report.BaselineBasis, "assumption") {
		t.Error("the baseline must ride the report as a named assumption")
	}
	if report.EditMinutesAssumed != 20 || !report.ExportExcluded {
		t.Error("the edit assumption and the export exclusion must ride the report")
	}
	rows := map[string]evaluationDraftTimeResult{}
	for _, row := range report.PerTasking {
		rows[row.Tasking] = row
	}
	fast := rows["t-fast"]
	if fast.SystemSegment != "45s" || fast.EditSegment != "20m0s" || fast.Total != "20m45s" || !fast.UnderThreshold {
		t.Errorf("fast row mismatch: %+v", fast)
	}
	// 80 min system + 20 min edit = 100 min: over the 90-minute threshold.
	slow := rows["t-slow"]
	if slow.UnderThreshold {
		t.Error("a 100-minute total must fail the 90-minute threshold (srd014 R4.4)")
	}
	if report.Pass {
		t.Error("the report must fail while a total exceeds the threshold")
	}
	broken := rows["t-broken"]
	if broken.Error == "" || report.Failed != 1 {
		t.Error("a failed tasking must be reported, never dropped (srd014 R5.5)")
	}
	if report.MaxTotal != "1h40m0s" {
		t.Errorf("max total %q, want 1h40m0s", report.MaxTotal)
	}
}

// TestDraftTimePassesWhenEveryTotalIsUnderTheThreshold pins the happy path
// the deployed mesh is expected to measure.
func TestDraftTimePassesWhenEveryTotalIsUnderTheThreshold(t *testing.T) {
	t.Parallel()
	corpus := draftTimeFixtureCorpus()
	corpus.Taskings = corpus.Taskings[:2]
	report := draftTimeReport(corpus,
		map[string]time.Duration{"t-fast": 40 * time.Second, "t-slow": 90 * time.Second},
		nil)
	if !report.Pass || report.Failed != 0 {
		t.Errorf("report should pass: %+v", report)
	}
}

// TestCorpusRecordsTheEditAssumption pins the corpus fields the report
// reads: both assumptions present with their bases.
func TestCorpusRecordsTheEditAssumption(t *testing.T) {
	t.Parallel()
	corpus := readEvaluationCorpus(t)
	if corpus.Assumptions.AnalystEditMinutes <= 0 || corpus.Assumptions.AnalystEditBasis == "" {
		t.Error("the corpus must record the analyst edit-segment assumption with its basis (srd014 R4.2)")
	}
	if corpus.Assumptions.FirstDraftBaselineMinutes <= 0 {
		t.Error("the corpus must record the half-day baseline (srd014 R4.3)")
	}
}
