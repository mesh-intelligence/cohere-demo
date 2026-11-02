# Evaluation Corpus

We label taskings over the Ukraine 2022 retrieval corpus so the srd014
evaluation can put numbers behind the demonstration's criteria. This
directory holds the labeled-tasking corpus (`corpus.yaml`) and the record of
how its labels were made. A figure reported by the evaluation names this
corpus's version together with the retrieval corpus version it labels.

## Labeling method

We authored every relevant set by reading documents, not by running
retrieval. For each tasking we searched the raw document store
(`corpus/store/documents`, the envelope text of all 432 documents) with plain
text terms, read the matched documents, and kept the ones whose content bears
on the tasking substantively -- the judgment an experienced analyst applies,
where a daily assessment that argues about the subject counts and one that
mentions the term in passing does not. The search ran over the corpus files
directly; the retrieval system, its embeddings, and its rankings played no
part, and srd014 R1.2 forbids them any future part: a label may be corrected
for a reading error, and may never be adjusted toward what the system
retrieved.

One set predates this file. eval-t01 adopts the relevant set the retrieval
corpus itself declares for its primary tasking
(`corpus/corpus.yaml`, `retrieval_conditions.relevant_to_primary_tasking`),
authored at rel10.5 when the corpus was assembled. We adopted it unchanged.

Labels sit at the document level. The store serves chunks, and a composed
chunk counts toward its parent document through the chunk identity the store
already carries; an analyst identifies relevant documents, and chunk
membership is a property of the store rather than of relevance.

## Conditions

srd014 R1.3 requires the corpus to span the retrieval conditions the metrics
depend on, and each tasking's `conditions` field tags what it exercises.

| Condition | Meaning | Carried by |
|---|---|---|
| multi_source | Relevant documents span more than one source type | eval-t01 through eval-t08, eval-t10 |
| multi_clearance | Relevant documents sit at more than one clearance level | eval-t01, t02, t03, t04, t08, t09 |
| below_cutoff | The relevant set exceeds the composition budget N=8 | eval-t01 (10), eval-t02 (10), eval-t03 (9) |

The below-cutoff condition holds structurally: when a tasking's relevant set
is larger than the declared composition budget, at least that many documents
minus N sit below the cutoff on every run, whatever the ranking does. That is
the property the recall comparison needs -- relevant documents exist to be
lost or recovered by ordering -- and it is established from the label count
and the declared N alone, so validating it reads no retrieval output. The
rebalanced retrieval collection (GH-286) serves every labeled document; the
harness's per-tasking results report each relevant document's presence in the
composed set, which is where a per-document rank becomes visible, after
labeling and without feeding back into it.

Taskings run at three clearance levels -- unclassified (t05, t06, t07, t10),
confidential (t03, t04, t08), and secret (t01, t02, t09) -- so the levels the
criteria depend on are exercised by the taskings themselves.

## Fictionality

The taskings are demonstration artifacts. None is, or derives from, an
operational requirement. The corpus they label mixes real published
reporting (the assessment, news, strike, and loss tiers) with an entirely
invented humint tier, per the retrieval corpus's own notice; every figure the
evaluation reports carries that mixed real-and-synthetic qualification in the
output itself (srd014 R6.1, srd017 R9.4). No document in either corpus is,
or derives from, actual classified material.

## Assertion segmentation

Citation coverage needs a denominator, and srd014 R3.2 requires the rule
that produces it to be declared and deterministic, so a coverage number
cannot be moved by re-segmenting. The rule, versioned with this corpus:

1. Split the brief on newlines.
2. Drop lines that are empty after trimming whitespace.
3. Drop markdown heading lines (a trimmed line starting with `#`).
4. Strip a single leading list marker (`- ` or `* `) from what remains.

Each surviving line is one assertion. A multi-sentence paragraph on one line
is one assertion; the unit is the line, because that is the unit the briefs
are written in and the unit a reviewer reads. An assertion is covered when a
citation span from the response's structured citation metadata overlaps the
line's rune range and that citation's record id is in the turn's composed
set. A citation that does not resolve to a composed document covers nothing
(srd014 R3.3), and coverage is computed from the citation metadata alone,
never by scraping the answer text (R3.4).

## Versioning

`corpus.yaml` carries `version`. Any change to a tasking, a label, or an
assumption is a version change, and results measured over different versions
are not comparable. Version 1.1 rephrased every tasking into the
command-staff brief form the tier selector routes to the composition tier;
version 1.0's imperative phrasings ("Assess...", "Report on...") classified
to the plain chat word, which composes the same document set but cites
nothing, so nine of ten briefs measured zero coverage for a reason that was
the tasking's grammar rather than the system's citing. The subjects, the
labels, and the assumptions are unchanged from 1.0. Version 1.2 added the
analyst edit-segment assumption beside the baseline; taskings and labels are
unchanged from 1.1. The half-day first-draft baseline lives in the same file
as a stated assumption (srd014 R4.3), so the number the comparison uses is
versioned with the taskings it serves.

## The recall figures and the sweep (GH-576)

`mage evaluation:recall` reports four figures per corpus, each labeled for
what it measures. Recall@N under the rerank order is the headline (srd014
R2.2: measured at the set composition drew on). Recall@N under the cosine
order — the untrimmed run's composition order truncated to the same N — is
its comparator, so the pair isolates ordering (R2.3): a superset always
out-recalls its own cut, and the old comparison against the untrimmed set
measured the trim, not the rerank. The retrieval boundary is recall over the
untrimmed fan-out (R2.7): what breadth alone reaches, so a miss the boundary
never returned reads as a retrieval gap and one the trim dropped reads as an
ordering or budget gap. The ceiling-normalised figure divides each at-N
recall by min(N, relevant)/relevant per tasking (R2.8), because a
10-document labeled set scored at N=8 caps at 0.8 by construction and a raw
figure against a 0.90 threshold is uninformative there.

`mage evaluation:sweep` varies the deployed pipeline — `retrievalBreadth` ×
`rerankTopN` × `cascade`, from `EVAL_SWEEP_BREADTHS`, `EVAL_SWEEP_TOPNS`,
and `EVAL_SWEEP_CASCADES` — and never the corpus (srd014 E7). Each
configuration patches the rendered literals in the deployed profiles, rolls
the chatbot, runs the corrected pair, and appends its row to
`recall-sweep.json` and `recall-sweep.md`, so an interrupted sweep keeps its
partial evidence. The deployed shape is restored on every path.

## Publication review

Corrected 2026-11-02 (GH-919.1). The first review, in GH-910.2, called
`generated-corpus.yaml` free of source text after checking only its taskings and
identifiers. It also held thirty reports the generator composed from ISW and
Telegram excerpts, 35,265 characters with verbatim phrases of up to twelve words,
and those reached the first public export. Under srd017 R9.7 a report composed
from keep-private excerpts is keep-private, so `mage evaluation:generate` now
writes reports to `evaluation/generated-reports.yaml` in the private data checkout
and the tracked file keeps taskings, identifiers, and labels.

What stays here carries short common phrases at most, each recorded with its reason
in `docs/guides/overlap-reviewed.yaml`. `mage corpus:overlap` measures this word by
word against the private checkout and fails on anything unreviewed. A figure
measured over these corpora still needs the private tiers to reproduce.
