// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// gcp:syncData (GH-910): the private corpus bucket is the operator-independent
// copy of the private data checkout -- the source datasets and the keep-private
// store tiers that mesh-intelligence/cohere-demo-data versions. "up" mirrors
// the checkout into the bucket; "restore" brings a fresh workstation's checkout
// path back from it. The private repository stays the source of truth, which is
// why up mirrors (deletes what the checkout no longer has) and restore does
// not (it fills a directory and leaves whatever else is there alone).

// gcpSyncDataExcludes keeps git internals and the decompressed Telegram CSV
// out of the bucket: the CSV is 267MB and derivable from the tracked gzip
// beside it, and the gcloud rsync exclude is one regex over relative paths.
const gcpSyncDataExcludes = `^\.git/.*$|^data/ukraine_news/news\.csv$`

// SyncData mirrors the private data checkout to the corpus bucket (up) or
// restores it from the bucket (restore). The checkout is resolved the way the
// corpus build resolves it: CORPUS_PRIVATE_ROOT, else the sibling convention.
func (Gcp) SyncData(direction string) error {
	root, err := cohereApplicationRoot()
	if err != nil {
		return err
	}
	settings, err := loadGcpSettings(root)
	if err != nil {
		return err
	}
	private := corpusPrivateRoot(root)
	if private == "" {
		if direction == "restore" {
			// Restoring is how a machine without the checkout gets one: fill
			// the sibling path the resolution will find afterwards.
			private = filepath.Join(root, "..", "cohere-demo-data")
		} else {
			return corpusMissingPrivateStore(root)
		}
	}
	arguments, err := gcpSyncDataCommand(direction, private, settings.corpusBucket())
	if err != nil {
		return err
	}
	started := time.Now()
	fmt.Printf("gcp:syncData: gcloud %s\n", strings.Join(arguments, " "))
	out, err := gcpDefaultRun("gcloud", arguments...)
	if err != nil {
		return fmt.Errorf("gcloud storage rsync: %w: %s", err, strings.TrimSpace(string(out)))
	}
	gcpLogPhase(settings.defaultCluster(), "syncData", direction, started,
		"gs://"+settings.corpusBucket())
	if direction == "restore" {
		compressed := filepath.Join(private, "data", "ukraine_news", "news.csv.gz")
		if _, err := os.Stat(compressed); err == nil {
			fmt.Printf("gcp:syncData: decompress the Telegram tier once:"+
				" gunzip -k %s\n", compressed)
		}
	}
	return nil
}

// gcpSyncDataCommand builds the one gcloud invocation each direction runs,
// kept pure so the argv is provable without a bucket.
func gcpSyncDataCommand(direction, private, bucket string) ([]string, error) {
	if strings.TrimSpace(private) == "" {
		return nil, fmt.Errorf("gcp:syncData: no private data checkout to sync")
	}
	bucketURL := "gs://" + bucket
	switch direction {
	case "up":
		return []string{"storage", "rsync", "--recursive",
			"--exclude=" + gcpSyncDataExcludes,
			"--delete-unmatched-destination-objects",
			private, bucketURL}, nil
	case "restore":
		return []string{"storage", "rsync", "--recursive",
			bucketURL, private}, nil
	default:
		return nil, fmt.Errorf("gcp:syncData: direction is up or restore, not %q", direction)
	}
}
