<!-- Copyright (c) 2026 Mesh Intelligence Inc. -->
<!-- SPDX-License-Identifier: MIT-0 -->

# Investment research case

The mesh bound to a second regulated domain: US energy-sector research for 2024. An
analyst asks a research question at an access level and gets a first-draft
investment-committee memo citing issuer facts, government analysis, macro releases,
and internal notes the analyst may read. srd018 is the specification.

**This is a demonstration over a small corpus. It is not investment advice, it makes
no recommendation, and a figure measured over it says nothing about investment
performance.** Three tiers are real public material and `analyst_note` is invented.

## Who can run it

Anyone can read this directory. Running the mesh needs organization access, because
the runtime modules, image, and interface kit are private (GH-928). The store and the
guards read local files and run with that access and no cluster.

## What was proven

`mage integration:investmentCase` passed on 2026-11-02 against a kind deployment with
the pinned runtime and Cohere. A Public requester received a conformant memo with
citations resolving to case documents. The same question about an invented deal composed
invented notes at Wall-crossed and none at Public, where the model answered that it could
not find anything. The document read path withheld a Wall-crossed note at Public,
Internal, and Restricted list, and returned it at Wall-crossed.

The defense profile's suites pass in unit form and its default chart render is asserted
unchanged. They were not rerun against a live deployment in this change.

## Layout

| Path | Contents |
|---|---|
| `corpus/corpus.yaml` | Manifest: tiers, access mapping, ratings, conditions |
| `corpus/store/` | The built chunk store with its vectors |
| `data/<tier>/` | Tracked source data and that tier's terms and verdict |

## Use

```bash
CORPUS_CASE=investment-research GOWORK=off mage corpus:build
CORPUS_CASE=investment-research GOWORK=off mage app:up
CORPUS_CASE=investment-research GOWORK=off mage app:seed
```

The case runs on the kind demo. `gcp:deploy` still layers `gcp-values.yaml`, which binds
no report, so a GKE deployment needs its own overlay first.

`mage corpus:fetch` refreshes the three real tiers, and needs `contact: <email>` in
`fetch.local.yaml` here, which git ignores, because the SEC asks scripts to declare a
contact. A build reads tracked data and needs no network.

## How it binds

| Mechanism | Defense profile | This case | Bound by |
|---|---|---|---|
| Access level | Clearance | Information barrier | `access_level_labels` and `clearance_markings` in the manifest |
| Source rating | Admiralty | The same scale, this case's own evaluation | `tier_admiralty` in the manifest |
| Report template | INTSUM | Investment-committee memo | `memo-schema.yaml` and `chatbot.report` in the values file |
| Corpus | Ukraine 2022 | US energy 2024 | This directory |
| Turn, filter, citation path, check | Shared | Shared | Not changed |

No agent's machine definition changes.

## What could not be bound

These surfaces carry the defense vocabulary here, because changing them means editing
what every profile runs (srd018 R7.2):

| Surface | What a reader sees | Why it stays |
|---|---|---|
| Wire values of the ordered access set | `clearance: secret` in store records, and an `X-Analyst-Clearance` header | Trusted profile configuration in the chatbot; rag-server enforces against it (srd018 R3.5) |

Two surfaces this table used to carry moved to runtime configuration (GH-929):
the selectors show this case's labels (Public, Internal, Restricted list,
Wall-crossed) over the unchanged wire values, and the right panel renders the
memo template instead of the INTSUM form — both bound by `chatbot.ui` in the
values file, served to the SPA as the ui route's declared
`static_assets.config` (srd029 R5.10).
| Conformance names | Signals and check words such as `check_bluf_section` | Internal identifiers; a verdict's defects and version do name the memo |
| Other model instructions | The summarization and tool-selection prompts mention a command-staff brief | Literals in `request-declarations.yaml` |
| The `/osint` path | Reaches the approved defense domains | Out of scope; the tracer does not use it |

Binding the interface and these prompts by configuration is follow-up work.

## Known limits, measured

- A turn's composition follows the question and rarely mixes real tiers. A broad question
  composed EIA chunks, a question naming issuers composed filing facts, and a question
  naming the Committee composed macro releases.
- Long memos sometimes exceed the chatbot's request budget for Cohere chat. The tracer
  retries and prints each timeout. The budget belongs to every profile.
- Conformance depends on Command honoring the instruction, so a memo can earn a true
  `non_conformant`. The tracer retries the broad leg up to four times.
- A live run needs the runtime source at the commit `go.mod` pins. With the sibling
  checkout elsewhere, point a scratch `go.work` at a worktree of that commit.
