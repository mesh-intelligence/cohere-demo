<!-- Copyright (c) 2026 Mesh Intelligence Inc. -->
<!-- SPDX-License-Identifier: MIT-0 -->

# Sable Strait Synthetic Intelligence Corpus

Every document here is invented. The states, units, vessels, places, and events
named in this corpus do not exist and correspond to no real classified,
personal, or operational material. A figure measured over this corpus describes
how the system behaves on synthetic data and is not a performance claim about
any real intelligence holding.

## What this corpus is for

The mesh retrieves across this corpus, reranks the candidates, and composes a
command-staff brief in which every assertion cites the document it came from.
The corpus is authored to hold retrieval conditions rather than merely to hold
documents: without a document above the lower level of a clearance pair, the
rel11.0 access-control tracer withholds nothing, and without relevant documents
that embedding retrieval ranks below the composition cutoff, the difference
Rerank makes has nowhere to appear.

Those conditions are declared in [corpus.yaml](corpus.yaml) alongside the
ordered clearance set, the source-type vocabulary, and the Admiralty scales.
The build reads that file; so does the corpus-condition check that asserts the
conditions still hold after an edit.

## Document schema

Each document under `documents/` is Markdown with YAML front matter. The front
matter is the document's declared metadata and the build copies it onto every
chunk without reading the prose to decide any of it -- the clearance level
because something that reads a document to decide how restricted it is has
already read it, and the Admiralty rating because a rating a model assigned is
not a source evaluation.

```yaml
---
source_id: HUM-0114              # unique within the corpus
source_type: humint              # humint | osint | sigint
clearance: confidential          # a level named in corpus.yaml
admiralty:
  source_reliability: B          # A through F
  information_credibility: 2     # 1 through 6
keywords: [port-ivensk, cable-handling]
synthetic: true
---
```

A document whose `clearance` is absent, or names a level not in the ordered
set, is chunked at the highest declared level rather than the lowest. The
shipped corpus declares a valid level on every document; the fail-closed path
is exercised by build tests against a fixture, not by shipping a broken
document.

## Layout

| Path | Role |
|---|---|
| `corpus.yaml` | Corpus version, ordered clearance set, source types, Admiralty scales, declared retrieval conditions |
| `documents/` | The corpus documents, one file per source |
| `store/` | The chunk store the build writes and the demo seeds from |

The store is a versioned artifact split by licence verdict (srd017 R9.7,
GH-910): tiers whose verdict is publishable are tracked here under
`store/documents/` (the synthetic `hr-*` families), and keep-private tiers
(`isw-*`, `tg-*`, `losses-*`, `missile-*`) are versioned in the private
`mesh-intelligence/cohere-demo-data` repository, cloned beside this one and
read together with the tracked tiers as one store. A clone of this repository
alone seeds and runs the demo over the publishable tiers without provider
access and without first running a build; the full corpus needs the private
checkout beside it. `store/store.yaml` stays tracked here as the index of the
whole corpus — it carries counts, parameters, and identities, no source text.
The store carries the retrieval corpus alone: taskings, labeled relevant
sets, and expected answers belong to the evaluation corpus and are built over
these documents rather than inside this store.

## Building the store

```bash
mage corpus:build
```

The build chunks the corpus, embeds each chunk once, and writes the records.
Which family it writes depends on the credential: with `COHERE_API_KEY`
exported, or a `cohere` key in the `.secrets/keys.json` operator store, it
builds the Cohere identity the chart declares, and without either the
Ollama identity the demo values declare. It computes a vector only for a chunk
and identity pair the store does not already hold, so running it twice over an
unchanged corpus issues no requests and leaves the files byte-identical.

Both families coexist on the same records, each under its own identity with its
own dimension, so repointing the embedding family is a choice of which stored
vectors to seed rather than a rebuild. To add the second family, export the
other provider's credential and run the build again; the vectors already stored
are left in place.

The store's index (`store/store.yaml`) names the identities it holds; today that
is the Cohere `embed-v4.0` at dimension 1536. A release declaring that identity
seeds without provider access and without first running a build, and a release
declaring another identity is refused until a build has stored its vectors.

Each identity records the provider that produced it. A build at one family
copies that provenance forward for the others rather than rewriting it, because
the integration target reads the provider to decide which client can embed a
query at the identity a collection was seeded under.

## Seeding from the store

`mage demo:seed` reads the store, selects the vectors whose recorded identity
matches the embedding model the deployed release declares, and writes them with
their metadata into each RAG unit's collection. It calls no embedding provider.
A store that holds no vectors for the deployed identity fails the seed, naming
the identity it wanted and the identities the store holds; it does not fall back
to computing them, because a fallback restores both the provider spend and the
identity drift the store exists to remove, and it does so silently.
