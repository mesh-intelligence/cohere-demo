<!-- Copyright (c) 2026 Mesh Intelligence Inc. -->
<!-- SPDX-License-Identifier: MIT-0 -->

# Going public

How this repository becomes a public one without republishing the material
GH-910 moved out of it. The split itself is done: the keep-private tiers'
source data and built chunks live in the private
`mesh-intelligence/cohere-demo-data` repository (srd017 R9.7), this tree
tracks only the publishable tiers, and a guard test holds that. What remains
is history, and history is why the public repository is a fresh export rather
than this repository flipped public.

## Why a fresh export, not a history rewrite

The harvested store tiers were tracked here from GH-158 until GH-910: every
commit in that span carries the ISW assessments and the Telegram news chunks
verbatim. Flipping this repository public republishes all of it, whatever the
tip looks like. `git filter-repo` over those paths could strip them, but it
rewrites every SHA after the first touched commit, which orphans the
`Issue-URL`/commit references threaded through nine hundred issues and every
PR discussion, and a rewrite that misses one ref republishes anyway. A fresh
export has neither failure mode: the public repository starts at a commit
whose tree has already passed the sweeps below, and this repository stays
private as the working repo of record.

## The export

From a clean checkout of `main` (no untracked files staged, `git status`
clean):

```bash
git archive --format=tar HEAD | (mkdir -p /tmp/cohere-demo-export && tar -x -C /tmp/cohere-demo-export)
cd /tmp/cohere-demo-export
git init -b main && git add -A && git commit -m "cohere-demo public export"
```

The export is pushed to a new public repository (`gh repo create --public`)
only after every pre-flight check below passes against it. Day-one history
begins at the export commit; the private repository keeps the full record.

## Pre-flight checks

Run all of these against the export tree, not against this repository. Every
one must come back empty or pass.

```bash
# 1. The guard test holds in the source repository.
GOWORK=off go test ./magefiles -run 'TestKeepPrivateStoreTiersAreNeitherIgnorableNorTracked|TestDatasetPayloadIsIgnoredAndItsReadmesAreNot' -count=1

# 2. No keep-private store family and no data payload in the export tree.
ls corpus/store/documents | grep -E '^(isw|tg|losses|missile)-' ; test $? -eq 1
find data -type f ! -name README.md | grep . ; test $? -eq 1

# 3. No credential material. keys.json and .secrets never ship.
find . -name 'keys.json' -o -name '.secrets' -o -name 'id_rsa*' -o -name '*.pem' | grep . ; test $? -eq 1
git grep -lE '(api[_-]?key|secret)["'\'']?\s*[:=]\s*["'\''][A-Za-z0-9_\-]{20,}' -- . ':!*.md' | grep . ; test $? -eq 1

# 4. Nothing copyrighted anywhere in the export's refs (trivial for a fresh
#    export with one commit; the check is the contract, in case the export is
#    ever rebuilt with imported history).
git log --all --diff-filter=A --pretty=format: --name-only -- 'data/**' | grep -v README.md | grep -v '^$' | grep . ; test $? -eq 1
git log --all --diff-filter=A --pretty=format: --name-only -- 'corpus/store/documents/isw-*' 'corpus/store/documents/tg-*' 'corpus/store/documents/losses-*' 'corpus/store/documents/missile-*' | grep -v '^$' | grep . ; test $? -eq 1

# 5. The licence verdicts are present where a reader will look for them.
grep -l 'Verdict' data/*/README.md | wc -l   # expect 5
```

Then one check that runs in the source repository, because it needs the private
checkout:

```bash
# 6. No keep-private text reached a tracked file by any route.
GOWORK=off mage corpus:overlap
```

Checks 2 and 4 look at paths, so they cannot see text that moved inside another
file. That gap let thirty derived reports into the first public export. Check 6
compares words: it fails on any run of eight or more words shared with the private
checkout unless `docs/guides/overlap-reviewed.yaml` records a reason for keeping it.

Then two reads no script replaces: the README top-to-bottom as a stranger
would read it (the private-checkout setup must make sense to someone without
the checkout), and `gcp.yaml`/`demo.yaml` for anything operator-specific that
should not travel.

## Standing rules

- **This repository stays private.** The public repository is the export; new
  work lands here and is re-exported, or development moves to the public
  repository only after a decision recorded in an issue.
- **`mesh-intelligence/cohere-demo-data` is never made public** and never
  linked from public prose in a way that implies its content is available.
- **The public repository can be read, not built, without org access.** Its Go
  modules (`mesh-intelligence/declarative-agents`), runtime image, and npm kit are
  private, which an anonymous `go mod download` confirmed on 2026-11-02 (exit 1).
  With org access and no private data checkout, a clone builds and seeds the
  synthetic tier alone.

## Dry-run record

2026-11-01, on the gh-910-publishable-corpus branch (the GH-910.5 dry run;
no public repository was created and nothing was pushed):

| Check | Result |
|---|---|
| Guard tests | pass |
| Keep-private families in export tree | none |
| Data payloads in export tree | none |
| Credential material | none |
| `--diff-filter=A` sweeps over the export's refs | empty |
| Verdict lines in tier READMEs | 5 of 5 |

Correction, 2026-11-02 (GH-919.1): that dry run and the first public export passed
every check then in this guide and still published thirty reports derived from
keep-private tiers, because no check looked inside files. Check 6 was added, the
reports moved to the private checkout, and public main was replaced with one clean
commit. A commit replaced this way can stay reachable by SHA until GitHub collects
it; deleting and recreating the repository is the complete purge.
