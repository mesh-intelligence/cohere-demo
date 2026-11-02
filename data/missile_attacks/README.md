# Missile and Drone Attack Records

Per-strike records of missiles and drones launched at Ukraine and intercepted, compiled from official Ukrainian Air Force reporting. This is the `sigint_event` tier of the rel10.5 corpus.

## Provenance

| Field | Value |
|-------|-------|
| Source | Massive Missile Attacks on Ukraine, Petro Ivaniuk, kaggle.com/datasets/piterfm/massive-missile-attacks-on-ukraine |
| Compiled from | Air Force Command of the Ukrainian Armed Forces and the General Staff, per the dataset description |
| Retrieved | 2026-08-31 |
| Records | 4,090 rows, 2022-09-28 to 2026-08-29 |
| Size | 0.9 MB |
| Licence | CC BY-NC-SA 4.0 |
| Verdict | **Keep private** (srd017 R9.7): the licence permits sharing, but ShareAlike would bind the chunk store and the MIT repository to CC BY-NC-SA terms, so this repository does not track it |

## Licence

Creative Commons Attribution-NonCommercial-ShareAlike 4.0, read from the dataset's own Kaggle metadata on 2026-08-31 rather than inferred.

Two clauses bind anything built from this tier. NonCommercial means the corpus, and any demonstration that carries it, is not licensed for commercial use, which is a question to answer before this material is shown in a commercial setting rather than after. ShareAlike means a derivative carries the same licence, and the chunk store the build writes is a derivative.

Attribution is required wherever the corpus is published: name Petro Ivaniuk and the dataset, and link the Kaggle page.

## Use in this repository

Decided 2026-08-31 (GH-281): the corpus is used privately and the licence is not treated as a constraint on that use. This repository is private, the demonstration is not published, and nothing built from the corpus is redistributed, so the NonCommercial and ShareAlike clauses have nothing to bite on and no attribution obligation arises.

That holds only while all three stay true. Publishing the repository, sharing the chunk store, or putting a figure measured over this tier in front of an audience outside it changes the position, and the clauses above are what would then apply.

## Files

| File | Contents |
|------|----------|
| `missile_attacks_daily.csv` | One row per reported strike group. Columns include `time_start`, `time_end`, `model`, `launch_place`, `target`, `launched`, `destroyed`, `affected region` |
| `missiles_and_uavs.csv` | Reference table of the munition types the daily rows name |

## Shape the build depends on

The tier begins on 2022-09-28, months after the invasion, because the campaign it records -- sustained strikes against infrastructure -- began in October 2022. The sparseness of July through September in the corpus window is a property of the war rather than a gap in the data, and it is worth saying out loud when the corpus is shown.

A day carries several rows, one per munition type reported that night. The slice loader folds a day's rows into one digest, naming every row it absorbed, because a reader asking what was launched on a date wants the night rather than one of its rows.

Free-text columns contain embedded newlines, so the CSV reader is configured to tolerate ragged rows rather than reject the file.

The payload files of every tier are versioned in the private `mesh-intelligence/cohere-demo-data` repository (GH-910), cloned beside this one; keep-private tiers' built chunks live there too. This README remains the canonical provenance record.
