// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// embedWords are the two interchangeable query-embedding words. Which one the
// turn dispatches is a configuration choice (srd009 R2.3), and srd009 R6.3
// requires the turn to run on the Ollama word where the provider is disabled.
var embedWords = []string{"embed_query", "embed_query_cohere"}

// embedLabel is the machine label both embedding transitions carry. Reading the
// label rather than a word name is what lets the packaging swap the provider
// without touching any word that consumes the result.
const embedLabel = "query_embedding"

// embedSeamReaders are the words allowed to address the embedding result. Each
// provider has its own capture word because the two response shapes differ, and
// declare_question lifts the tasking into command state. Everything later reads
// those, not the provider.
//
// They address the machine's query_embedding label rather than a word name, so
// they read whichever provider ran; the check below allows either form, since
// naming the label is what the tier switch depends on and naming the word is
// what it must not reintroduce.
var embedSeamReaders = map[string]bool{
	"capture_query_vector_cohere": true,
	"capture_query_vector_ollama": true,
	"declare_question":            true,
}

// TestEmbedSeamStaysConfinedToItsReaders keeps the query-embedding provider from
// spreading back through the turn. Four words once read the embedding word's
// carried input directly, which made repointing the embed word -- configuration
// under srd009 R2.3 -- a four-site edit across files the chart packages
// verbatim, and made the tier switch in GH-222 intractable.
//
// The check is on $from(<embed word>) selectors, which is how one word addresses
// another's result. A word's own declaration naming itself, and the machine
// transition that dispatches it, are not readers and are excluded (GH-221).
func TestEmbedSeamStaysConfinedToItsReaders(t *testing.T) {
	chatbot := agentDir(t, "chatbot")
	entries, err := os.ReadDir(chatbot)
	if err != nil {
		t.Fatalf("read the chatbot agent directory: %v", err)
	}

	selector := regexp.MustCompile(`\$from\((` + strings.Join(embedWords, "|") + `)\)`)
	label := regexp.MustCompile(`\$from\(` + embedLabel + `\)`)
	var labelReaders int
	// The declaring line of a word, so a reader can be attributed to the word
	// it belongs to rather than to the file.
	declaration := regexp.MustCompile(`^\s*-\s+name:\s+(\S+)`)

	var offenders []string
	var scanned int
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(chatbot, entry.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		var word string
		for i, line := range strings.Split(string(body), "\n") {
			if match := declaration.FindStringSubmatch(line); match != nil {
				word = match[1]
			}
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			if label.MatchString(line) {
				labelReaders++
			}
			if !selector.MatchString(line) {
				continue
			}
			scanned++
			if embedSeamReaders[word] {
				continue
			}
			offenders = append(offenders, entry.Name()+":"+itoa(i+1)+" ("+word+"): "+strings.TrimSpace(line))
		}
	}

	// The seam has to exist for the scan above to mean anything: if nothing read
	// the label either, the turn would simply have stopped embedding.
	if labelReaders == 0 {
		t.Fatal("no word reads $from(" + embedLabel + "); the scan would pass vacuously")
	}
	_ = scanned
	sort.Strings(offenders)
	for _, offender := range offenders {
		t.Errorf(
			"%s addresses the embedding provider directly. Only %s may; every later"+
				" word reads the tasking from declare_question and the vector from"+
				" a capture word, so repointing the embed word stays a"+
				" configuration change rather than an edit across the turn",
			offender, readerList())
	}
}

func readerList() string {
	names := make([]string, 0, len(embedSeamReaders))
	for name := range embedSeamReaders {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, " and ")
}

// itoa avoids pulling strconv in for one call site.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
