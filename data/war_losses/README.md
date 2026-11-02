# Reported Russian Loss Figures

Daily cumulative figures for Russian personnel and equipment losses as reported by the Ukrainian General Staff. This is the `sigint_series` tier of the rel10.5 corpus.

## Provenance

| Field | Value |
|-------|-------|
| Source | 2022 Ukraine Russian War, Petro Ivaniuk, kaggle.com/datasets/piterfm/2022-ukraine-russian-war |
| Compiled from | General Staff of the Armed Forces of Ukraine daily releases, per the dataset description |
| Retrieved | 2026-08-31 |
| Records | 1,648 days, 2022-02-25 to 2026-08-30 |
| Size | 0.2 MB |
| Licence | CC BY-NC-SA 4.0 |
| Verdict | **Keep private** (srd017 R9.7): same reasoning as `data/missile_attacks/README.md`, same licence |

## Licence

Creative Commons Attribution-NonCommercial-ShareAlike 4.0, read from the dataset's own Kaggle metadata on 2026-08-31 rather than inferred. The NonCommercial and ShareAlike clauses bind anything built from this tier, including the chunk store; see `data/missile_attacks/README.md`, which carries the same licence, for what that means in practice.

Attribution is required wherever the corpus is published: name Petro Ivaniuk and the dataset, and link the Kaggle page.

## What the figures are

These are one belligerent's claims about its opponent's losses, published daily during a war. They are a record of what was reported, not an independent count, and a demonstration measured over them should say so. The corpus treats the tier as official-release reporting and rates it accordingly.

## Use in this repository

Decided 2026-08-31 (GH-281): the corpus is used privately and the licence is not treated as a constraint on that use. This repository is private, the demonstration is not published, and nothing built from the corpus is redistributed, so the NonCommercial and ShareAlike clauses have nothing to bite on and no attribution obligation arises.

That holds only while all three stay true. Publishing the repository, sharing the chunk store, or putting a figure measured over this tier in front of an audience outside it changes the position, and the clauses above are what would then apply.

## Files

| File | Contents |
|------|----------|
| `russia_losses_personnel.csv` | `date`, `day`, `personnel`, `personnel*`, `POW` |
| `russia_losses_equipment.csv` | `date`, `day`, and twenty equipment columns including `tank`, `APC`, `field artillery`, `MRL`, `drone`, `aircraft`, `helicopter`, `naval ship`, `cruise missiles` |
| `russia_losses_equipment_correction.csv` | Corrections the source issued against the equipment series |

## Shape the build depends on

Three properties of these files drove decisions in the corpus build.

The rows are cumulative running totals rather than daily counts. A document stating the total answers how many since February 2022, which is a question about the war rather than about the day an analyst asked for, so the build reports the difference between consecutive days with the running total beside it.

The rows arrive in descending date order. A delta taken in the file's own order is the day's reporting with the sign flipped, which reads as a plausible figure and is simply wrong, so the loader sorts ascending before differencing.

The two files cover the same days, so the tier's unit is the day read from both rather than the file. One envelope per file per day would double the tier against what the release measures.

A column the source did not report on a given day is blank or `NaN`. The build passes over it rather than reporting zero, because an unreported figure and a figure of zero are different claims.

The payload files of every tier are versioned in the private `mesh-intelligence/cohere-demo-data` repository (GH-910), cloned beside this one; keep-private tiers' built chunks live there too. This README remains the canonical provenance record.
