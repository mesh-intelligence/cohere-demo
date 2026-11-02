# Synthetic HUMINT Reports

EVERY DOCUMENT IN THIS DIRECTORY IS FICTIONAL. The classification markings (CONFIDENTIAL, SECRET, SECRET//NOFORN, UNCLASSIFIED//FOUO) are demo labels for testing clearance-gated retrieval. No document here is, or derives from, actual classified material. All sources (ORACLE-7, GRANITE, SABLE, VERMILION, TALWAR, LATTICE, MURANO, KESTREL, HALCYON) and the collecting organization (DEFTECH) are invented.

This is the `humint` tier of the rel10.5 corpus and the only synthetic one. It exists because the other four tiers are published material and therefore uniformly unclassified: without it the corpus has one clearance level, no level pair for the rel11.0 assertion to iterate, and nothing for the two-level tracer to withhold.

## Provenance

| Field | Value |
|-------|-------|
| Source | Written for this repository by `gen_humint.py`, over the public Telegram corpus in `data/ukraine_news/` |
| Generated | 2026-08-31 |
| Records | 35 reports, 2022-02-26 to 2023-07-03 |
| Size | 0.1 MB |
| Licence | Same terms as this repository; the Telegram material it was written over is recorded in `data/ukraine_news/README.md` |
| Verdict | **Publishable** (srd017 R9.7): every document is invented, written for this repository; its built chunks (`hr-*`) stay tracked here |

## Construction

We extracted rumour-bearing messages from the public Telegram news corpus (`data/ukraine_news/news.csv`, CC BY-SA 4.0) by filtering for hedging markers in Russian and English ("по слухам", "якобы", "со ссылкой на источники", "reportedly", "unconfirmed"). For 24 of those rumours we wrote a fictional field report that confirms, corrects, or adds granularity to the public claim, following the structure of source-protected reporting in publicly available diplomatic cables: portion-marked numbered paragraphs, a summary, a source line with access description and protection caveat, and a field comment with corroboration references.

Report content is anchored to the real event timeline so it cross-references the other corpus tiers (ISW dailies, missile-attack events, loss figures) without contradicting them. Source reliability uses the NATO letter-number system (A-F reliability, 1-6 credibility).

## Files

| File | Contents |
|------|----------|
| `humint_reports.jsonl` | 27 reports, 2022-02-26 to 2023-07-03; fields: id, date, classification, source_id, reliability, source_access, collector, title, related_messages (Telegram provenance), tags, text |
| `rumour_candidates.jsonl` | 65 rumour-marked Telegram messages sampled per month (the selection pool) |
| `gen_humint.py` | generator; rerun with `python3 gen_humint.py` from the repo root to regenerate |

## Level distribution

UNCLASSIFIED//FOUO 2, CONFIDENTIAL 11, SECRET 7, SECRET//NOFORN 7. Several events are covered at two levels by different sources (for example Kherson at CONFIDENTIAL in HR-22-002 and SECRET in HR-22-015), so a clearance-gated query returns a different picture per level by design.

The payload files of every tier are versioned in the private `mesh-intelligence/cohere-demo-data` repository (GH-910), cloned beside this one; keep-private tiers' built chunks live there too. This README remains the canonical provenance record.
