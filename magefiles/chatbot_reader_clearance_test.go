// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"strings"
	"testing"
)

// The reader-clearance filter's shape (GH-378): who a brief is for is a
// clearance question, the comparison runs on the seeded rank because level
// names do not sort, and external open-source citations are exempt by
// category rather than by a rank chosen to slip under every ceiling
// (srd011 R7.4).

func TestReaderClearanceComparesTheSeededRank(t *testing.T) {
	t.Parallel()
	tools := chatbotDeclaredTools(t)
	partition, ok := tools["partition_reader_clearance"]
	if !ok {
		t.Fatal("partition_reader_clearance is not declared")
	}
	for _, want := range []string{
		"field: source.document.clearance_rank",
		"op: lte",
		"operand_type: number",
		"right: $from(reader_clearance).body.rank",
	} {
		if !strings.Contains(partition, want) {
			t.Errorf("partition_reader_clearance is missing %q; the filter compares the"+
				" declared order's rank, not the level names", want)
		}
	}
	if !strings.Contains(partition, "$from(partition_external_citations).body.unmatched") {
		t.Error("partition_reader_clearance must read internal citations alone; the" +
			" external split before it is the srd011 R7.4 exemption")
	}
}

func TestExternalCitationsAreExemptByCategory(t *testing.T) {
	t.Parallel()
	tools := chatbotDeclaredTools(t)
	split, ok := tools["partition_external_citations"]
	if !ok {
		t.Fatal("partition_external_citations is not declared")
	}
	if !strings.Contains(split, "field: source.document.source") || !strings.Contains(split, "right: osint") {
		t.Error("the external split must key on the provenance marking, not on any rank")
	}
	merge, ok := tools["compose_merged_citations"]
	if !ok {
		t.Fatal("compose_merged_citations is not declared")
	}
	// Both lanes reach the citation render: the externals from the split's
	// matched side untouched, the internals from the clearance partition.
	for _, want := range []string{
		"$from(partition_reader_clearance).body.matched",
		"$from(partition_external_citations).body.matched",
	} {
		if !strings.Contains(merge, want) {
			t.Errorf("the merge is missing %q; an exempt external citation must reach"+
				" the render however strict the reader's ceiling", want)
		}
	}
}
