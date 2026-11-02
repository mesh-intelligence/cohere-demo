# Telegram News Channel Messages

Messages from twelve Russian, Ukrainian, and Western news channels on Telegram, covering the first seventeen months of the invasion. This is the `osint_news` tier of the rel10.5 corpus, and the raw material the synthetic HUMINT tier was written over.

## Provenance

| Field | Value |
|-------|-------|
| Source | Public Telegram news corpus; the upstream publication was not located at the time of writing |
| Retrieved | 2026-08-31 |
| Records | 386,889 messages, 2022-02-24 to 2023-07-03 |
| Size | 280.1 MB |
| Licence | CC BY-SA 4.0 attested secondhand; upstream searched for and not located 2026-09-03 -- see below |
| Verdict | **Keep private** (srd017 R9.7): the attestation is secondhand and the messages mirror the channels' publishers, whose own rights the dataset licence cannot grant |

## Licence

`data/humint/README.md` records this corpus as CC BY-SA 4.0, written when the HUMINT tier was generated from it. That is a secondhand attestation within this repository, not a reading of the upstream terms, and a search for the originating dataset did not locate it.

The ShareAlike clause, if the attestation holds, means the chunk store the build writes carries the same licence, and attribution is required wherever the corpus is published.

A search on 2026-09-03 did not locate the upstream: the twelve channel handles, the 386,889-message count with the 2022-02-24 to 2023-07-03 range, and the exact CSV schema (id, group_id, group_name, text, date) were each searched across Kaggle, Hugging Face, Zenodo, Figshare, and the open web, and no published dataset matched. The CC BY-SA 4.0 attestation therefore remains secondhand and unverified, and the conservative reading is that the ShareAlike and attribution obligations hold while the underlying messages carry their outlets' own rights besides.

Before this material is redistributed or shown outside a private demonstration, the upstream still has to be identified and its terms read; replace this section with what is found. The channels below and the exact date range remain the identifying fingerprint to search on.

## Channels

Twelve channels, spanning Russian state media, Russian independent media, Ukrainian outlets, and Western wire services:

`bbcrussian`, `meduzalive`, `novaya_pishet`, `nytimes`, `rbc_news`, `rian_ru`, `tass_agency`, `u_now`, `ukraina24tv`, `ukrainavshoke_repost`, `uniannet`, `washingtonpost`

The mix is a property worth keeping. A tasking answered from this tier alone draws on outlets that contradict each other, which is what makes the corpus a test of retrieval rather than a test of paraphrase.

## Use in this repository

Decided 2026-08-31 (GH-281): the corpus is used privately and the unresolved licence is not treated as blocking that use. This repository is private, the demonstration is not published, and nothing built from the corpus is redistributed.

That holds only while all three stay true. Before this tier is published, shared, or shown to an audience outside the repository, its terms have to be established -- the section above says what is unknown and what to do about it.

## Files

| File | Contents |
|------|----------|
| `news.csv` | `id`, `group_id`, `group_name`, `text`, `date` |

## Shape the build depends on

The `id` column is not unique. Each channel numbers its own messages, so the same number appears across channels -- 1,345 collisions in a single month of the corpus window. A document identifier built from the number alone silently overwrites one channel's message with another's, which is how it was found: the store held fewer documents than the loader reported. The adapter mints `tg-<channel>-<id>`, and the slice loader fails a build whose document identifiers collide.

Messages from Russian outlets designated as foreign agents carry a boilerplate disclaimer appended verbatim to every message. The adapter separates it into its own segment and marks it unsearchable, so it stays readable in the served document without putting the same paragraph into thousands of vectors.

The window holds roughly 225,000 of these messages, far more than the corpus needs. The slice loader samples per channel per day, keeping the longest messages, which favours reporting over one-line headlines.

The payload files of every tier are versioned in the private `mesh-intelligence/cohere-demo-data` repository (GH-910), cloned beside this one; keep-private tiers' built chunks live there too. This README remains the canonical provenance record.
