<!-- Copyright (c) 2026 Mesh Intelligence Inc. -->
<!-- SPDX-License-Identifier: MIT-0 -->

# filing_facts

Reported values from the periodic reports of six US energy issuers: Exxon Mobil,
Chevron, ConocoPhillips, EOG Resources, Occidental Petroleum, and Marathon Petroleum.

| Field | Value |
|---|---|
| Source | SEC EDGAR XBRL company facts API, data.sec.gov |
| Retrieved | 2026-11-02 |
| Window | Forms 10-K and 10-Q filed in 2024 |
| Terms read | 2026-11-02, https://www.sec.gov/about/privacy-information |
| Verdict | **Publishable** (srd017 R9.7) |

## Terms and reasoning

The SEC states: "Information presented on sec.gov is considered public information and
may be copied or further distributed by users of the web site without the SEC's
permission", and asks for appropriate citation.

That statement does not settle a filer's copyright in the prose it wrote, so this tier
carries none. It holds numbers and their identifiers: concept, value, period, form,
accession, and filing date. The taxonomy's own labels and descriptions, which the API
returns, are dropped. The sentences in the built documents are this build's.

This is a judgment about reuse and not legal advice.

## Access

SEC fair-access guidance limits a user to ten requests per second and asks scripts to
declare a user agent with a contact. `mage corpus:fetch` spaces requests 250 ms apart
and reads the contact from a gitignored file.
