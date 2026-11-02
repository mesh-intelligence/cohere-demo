# ISW Daily Campaign Assessments

Daily written assessments of the Russian invasion of Ukraine, published by the Institute for the Study of War. This is the `osint_assessment` tier of the rel10.5 corpus and the analytic backbone of it: the other four tiers report events and figures, and this one argues about them.

## Provenance

| Field | Value |
|-------|-------|
| Source | Institute for the Study of War, understandingwar.org |
| Retrieved | 2026-08-31 |
| Records | 1,634 assessments, 2022-02-25 to 2026-08-29 |
| Size | 65.0 MB |
| Licence | All rights reserved (site footer, read 2026-09-03) -- see below |
| Verdict | **Keep private** (srd017 R9.7) |

## Licence

Read from the site in a browser on 2026-09-03 (the 403 applies to scripted requests only). understandingwar.org publishes no terms-of-use, copyright, or reprint-permission page: the site's footer carries every page's only rights statement, "(c)2026 Institute for the Study of War. All rights reserved.", and the privacy policy contains no content-use language. The tier is therefore all-rights-reserved third-party content by the publisher's own statement, not by default: it is quoted in a demonstration, not republished.

Anyone redistributing this corpus, publishing figures measured over it, or showing it outside a private demonstration needs permission ISW has not published a route for; the press contact (press@understandingwar.org) is the address the Library of Congress archive listing points inquiries at. Record any answer received in this file.

Every assessment carries its own endnotes citing the primary sources it was written from. The corpus keeps that tail on the document and out of the retrieval vectors, so a citation reaches ISW's sourcing rather than only ISW's conclusion.

## Use in this repository

Decided 2026-08-31 (GH-281): the corpus is used privately and the unresolved licence is not treated as blocking that use. That decision covers the private data repository and a private demonstration only. This public repository carries none of the tier's text and nothing composed from it; `mage corpus:overlap` holds that (`docs/guides/going-public.md`).

Before this tier is published, shared, or shown to an audience outside the private demonstration, its terms have to be established -- the section above says what is unknown and what to do about it.

## Files

| File | Contents |
|------|----------|
| `isw_dailies.jsonl` | One assessment per line. Fields: `date`, `title`, `url`, `text` |
| `isw_products.json` | The harvest index the dailies were pulled from |

## Shape the build depends on

Two properties of this tier drove decisions in the corpus build, and both are worth knowing before editing anything here.

The `text` field carries no line breaks at all. An assessment is one continuous run, commonly tens of thousands of characters. A chunker that splits on blank lines therefore returns one chunk per assessment, which silently defeats every size profile. `splitOversizeParagraphs` in `magefiles/corpus_store.go` exists because of this tier.

The last roughly a quarter of each assessment is an endnote tail of bracketed numbers and URLs. The ISW adapter marks it unsearchable, so it stays readable in the served document and never reaches an embedding.

## Repaired defect

The harvest wrote one record whose date contradicted its content. It was removed on 2026-08-31 (GH-274), leaving 1,634. Anyone re-harvesting this tier should expect it back and check for it.

The record sat at `2022-02-24` carrying a title stating February 24 of 2025, and its text was the 2025 assessment. It was a duplicate: a record already existed at `2025-02-24` with the same text byte for byte and the full canonical URL, while the misdated copy carried only a truncated slug. The repair was therefore to drop it rather than re-date it, since re-dating would have produced two records on one date and the build refuses a corpus whose document identifiers collide.

The build refuses such a record rather than admitting it, because the window filter selects on `date` and a record whose date contradicts its content is admitted or dropped by accident. `checkISWTitleDate` in `magefiles/corpus_adapters.go` is that check, and it fails the build naming the record.

To check a fresh harvest, parse the month-day-year out of each title and compare its year against the `date` field; a clean file reports no disagreements.

## Known gap

The assessment for 2022-02-24, the first day of the invasion, is absent. The misdated duplicate occupied that slot, and removing it left the series starting at 2022-02-25. The gap falls outside the rel10.5 corpus window and costs the demonstration nothing, but it is a real hole in the tier for anyone reading it beyond that window.

The payload files of every tier are versioned in the private `mesh-intelligence/cohere-demo-data` repository (GH-910), cloned beside this one; keep-private tiers' built chunks live there too. This README remains the canonical provenance record.
