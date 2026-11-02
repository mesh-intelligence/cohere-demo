// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The split store (srd015 R6.1, srd017 R9.7). The chunk store is one corpus in
// two locations: tiers whose licence verdict is publishable are tracked in this
// repository under corpus/store/documents, and tiers whose verdict is
// keep-private are versioned in the private data repository and read from its
// checkout beside this one. The verdicts live in the per-tier READMEs under
// data/; the prefixes here mirror them, keyed by document identifier because
// the identifier is the one tier marker every store path already carries.

// corpusPrivateDataRepo is the private repository that holds the source
// datasets and the keep-private store tiers. It must never be made public.
const corpusPrivateDataRepo = "mesh-intelligence/cohere-demo-data"

// corpusPrivateRootEnv overrides where the private data checkout is looked for.
// Unset means the sibling-checkout convention: ../cohere-demo-data next to this
// repository, which holds from a gh-* worktree too since worktrees share the
// parent directory.
const corpusPrivateRootEnv = "CORPUS_PRIVATE_ROOT"

// corpusKeepPrivatePrefixes names the keep-private tiers by document-identifier
// prefix, mirroring the Verdict rows in data/*/README.md: the ISW assessments
// (all rights reserved), the Telegram news channels (publishers' own text), and
// the two CC BY-NC-SA Kaggle tiers whose ShareAlike clause would bind this MIT
// repository. The synthetic humint tier (hr-*) is publishable and tracked here.
var corpusKeepPrivatePrefixes = []string{"isw-", "tg-", "losses-", "missile-"}

// corpusDocumentKeepPrivate reports whether a document belongs to a
// keep-private tier.
func corpusDocumentKeepPrivate(documentID string) bool {
	lowered := strings.ToLower(documentID)
	for _, prefix := range corpusKeepPrivatePrefixes {
		if strings.HasPrefix(lowered, prefix) {
			return true
		}
	}
	return false
}

// corpusPrivateRoot resolves the private data checkout. The environment
// variable wins; otherwise the sibling convention. An empty string means no
// private checkout is present, which is the publishable-tiers-only mode a
// public clone runs in.
func corpusPrivateRoot(root string) string {
	if corpusRootIsCase(root) {
		return ""
	}
	//nolint:forbidigo // the private checkout is operator-supplied, like the datasets.
	if value := strings.TrimSpace(os.Getenv(corpusPrivateRootEnv)); value != "" {
		return value
	}
	sibling := filepath.Join(root, "..", "cohere-demo-data")
	if _, err := os.Stat(sibling); err == nil {
		return sibling
	}
	return ""
}

// corpusPrivateStoreDocumentsDir is where the keep-private document directories
// live inside the private checkout, mirroring corpus/store/documents here.
// Empty when no private checkout is present.
func corpusPrivateStoreDocumentsDir(root string) string {
	private := corpusPrivateRoot(root)
	if private == "" {
		return ""
	}
	return filepath.Join(private, "store", "documents")
}

// corpusMissingPrivateStore is the one message every path that needs the
// keep-private tiers and cannot find them reports, so the fix is named where
// the failure is.
func corpusMissingPrivateStore(root string) error {
	return fmt.Errorf("the keep-private store tiers are not here: clone %s beside"+
		" this repository (or point %s at its checkout); this repository tracks"+
		" only the publishable tiers (srd017 R9.7)",
		corpusPrivateDataRepo, corpusPrivateRootEnv)
}

// corpusStoreMissingTiersNote reports which keep-private tiers a loaded store
// is missing, or "" when all are present. Seeding over the publishable tiers
// alone is allowed and says so rather than being silent about it.
func corpusStoreMissingTiersNote(root string, documents map[string]corpusStoredDocument) string {
	if corpusRootIsCase(root) {
		return ""
	}
	missing := corpusMissingKeepPrivateTiers(documents)
	if len(missing) == 0 {
		return ""
	}
	return fmt.Sprintf("corpus store: keep-private tiers absent (%s); proceeding"+
		" over the publishable tiers alone. For the full corpus, clone %s beside"+
		" this repository or set %s.",
		strings.Join(missing, ", "), corpusPrivateDataRepo, corpusPrivateRootEnv)
}

// corpusMissingKeepPrivateTiers names the keep-private prefixes with no
// document in the store, sorted.
func corpusMissingKeepPrivateTiers(documents map[string]corpusStoredDocument) []string {
	present := make(map[string]bool, len(corpusKeepPrivatePrefixes))
	for id := range documents {
		lowered := strings.ToLower(id)
		for _, prefix := range corpusKeepPrivatePrefixes {
			if strings.HasPrefix(lowered, prefix) {
				present[prefix] = true
			}
		}
	}
	missing := make([]string, 0, len(corpusKeepPrivatePrefixes))
	for _, prefix := range corpusKeepPrivatePrefixes {
		if !present[prefix] {
			missing = append(missing, prefix+"*")
		}
	}
	sort.Strings(missing)
	return missing
}

// requireFullCorpusStore is the guard for targets whose evidence is the whole
// rel10.5 corpus: a partial store would not fail them loudly on its own, it
// would quietly change what they measure.
func requireFullCorpusStore(root string, documents map[string]corpusStoredDocument) error {
	missing := corpusMissingKeepPrivateTiers(documents)
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("the store holds no documents for the keep-private tiers"+
		" %s; %w", strings.Join(missing, ", "), corpusMissingPrivateStore(root))
}
