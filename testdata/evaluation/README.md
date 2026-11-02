<!-- Copyright (c) 2026 Mesh Intelligence Inc. -->
<!-- SPDX-License-Identifier: MIT-0 -->

# Labeled Evaluation Corpus

Ten taskings, each paired with the set of Sable Strait documents an analyst
working that tasking would identify as bearing on it. This is the corpus the
rel13.0 harness measures recall over. It is invented, it labels an invented
corpus, and every figure produced from it is a demonstration figure rather than
an operational result.

## What is here

| Path | Role |
|---|---|
| `taskings.yaml` | The versioned corpus: taskings, relevant sets, and the labeling provenance |

The documents the labels name live in [`corpus/documents/`](../../corpus/documents/).
The labels are deliberately not stored beside them: srd015 R6.4 keeps the chunk
store to the retrieval corpus alone, because a label sitting next to the chunk
it scores is a label an evaluation can read as an answer key. The two artifacts
stay in sync through the retrieval corpus version `taskings.yaml` names, and a
Go check fails when that version stops matching what `corpus/corpus.yaml`
declares.

## How the labels were made

Each set was written by reading the documents and asking what a brief on that
tasking would have to draw on, tasking by tasking, before any query had been
posed to the mesh. Nothing here was derived from what the system retrieved,
because a relevant set assembled after seeing what came back measures the
system's agreement with itself.

The rule that keeps it that way is in `taskings.yaml` under
`labeling.amendment_rule`, and it is worth stating twice: a set is not tightened
when the system misses a labeled document and not extended when the system
surfaces something plausible the label omitted. The pull to adjust is strongest
on exactly the taskings the system handles worst, which is where an adjustment
would do the most damage to the number.

A label may be corrected for an error in the label itself — a source identifier
naming no document, or a document whose clearance sits above its tasking's
level. Any such correction increments the corpus version, so figures measured
before and after are not silently compared.

## Two decisions a reader should know about

**Relevant sets name only documents at or below their tasking's clearance.** A
set containing a document the run is not cleared to retrieve would cap recall at
a number retrieval never had a chance to reach, blending a gating outcome into a
retrieval measurement. Clearance isolation is proven by the srd010 access-control
test, which checks a population rather than sampling one, and srd014 R6.3 keeps
this evaluation from carrying that criterion.

**`lexically_oblique` describes how a document was written, not how it ranked.**
Marked documents are relevant in substance while sharing little surface
vocabulary with their tasking, so embedding similarity has less to work with
than the relevance judgment does. Whether such a document actually falls below
the composition cutoff is what the harness measures — recording it here would be
deriving a label from a run. The marks say where the corpus was built so the
with-and-without-Rerank comparison has something to separate.

## Shape of the corpus

Ten taskings: one at unclassified, one at restricted, three at confidential, and
five at secret. Nine of the ten label documents across more than one source type.
Four label more relevant documents than the five a turn passes to composition,
so a perfect retrieval cannot score full recall from one source's top-N. Eighteen
labeled documents across the corpus are marked oblique.

`go test ./magefiles -run '^TestEvaluationCorpus'` checks all of that, plus that
every labeled identifier resolves to a real document, that no label carries a
rank or a score, and that the labeling provenance is recorded.
