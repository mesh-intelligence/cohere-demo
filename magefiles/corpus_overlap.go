// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// The verbatim-overlap sweep (srd017 R9.7, GH-919). A path check proves no
// keep-private file is tracked; it says nothing about keep-private text that
// reached a tracked file some other way, which is how thirty machine-written
// reports reached the first public export. The sweep compares words rather than
// paths: every run of overlapShingleWords consecutive words a tracked file
// shares with the private checkout is reported, and a run nobody has reviewed
// fails the target.
//
// It collects shingles from the tracked tree and streams the private checkout
// past them, because the tracked tree is small and the private checkout is
// hundreds of megabytes.

const (
	overlapShingleWords = 8
	overlapReviewedPath = "docs/guides/overlap-reviewed.yaml"
	// Below this, a string in a private store record is a field value rather
	// than source text.
	overlapMinimumStoreString = 40
)

// overlapPublishablePrivateFiles live in the private checkout because data/ moves
// as one root, but their verdict is publishable, so a tracked copy is not a
// finding: the invented humint reports the hr-* store documents are built from.
// data/humint/rumour_candidates.jsonl is Telegram text and stays in scope.
var overlapPublishablePrivateFiles = map[string]bool{
	"data/humint/humint_reports.jsonl": true,
	"data/humint/gen_humint.py":        true,
}

// overlapRun is one maximal stretch of a tracked file found in private text.
type overlapRun struct {
	File  string
	Words int
	Text  string
}

// fingerprint identifies a reviewed run without the review file quoting it.
func (r overlapRun) fingerprint() string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.Join(overlapTokens(r.Text), " "))))
	return hex.EncodeToString(sum[:8])
}

// overlapReviewedEntry records a human decision that one run may stay public.
type overlapReviewedEntry struct {
	File        string `yaml:"file"`
	Fingerprint string `yaml:"fingerprint"`
	Words       int    `yaml:"words"`
	Reason      string `yaml:"reason"`
}

type overlapReviewedFile struct {
	Reviewed []overlapReviewedEntry `yaml:"reviewed"`
}

// overlapTokens splits text into words of letters, in any script. Digits and
// punctuation are separators, so a reformatted date or a changed comma does not
// hide a copied sentence.
func overlapTokens(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool { return !unicode.IsLetter(r) })
}

func overlapShingle(words []string) string {
	return strings.ToLower(strings.Join(words, " "))
}

// overlapCandidates is every shingle a set of tracked files holds.
func overlapCandidates(tracked map[string]string) map[string]bool {
	candidates := map[string]bool{}
	for _, text := range tracked {
		words := overlapTokens(text)
		for i := 0; i+overlapShingleWords <= len(words); i++ {
			candidates[overlapShingle(words[i:i+overlapShingleWords])] = false
		}
	}
	return candidates
}

// overlapSplitWords is a bufio.SplitFunc yielding runs of letters, so a large
// private file streams without being held in memory.
func overlapSplitWords(data []byte, atEOF bool) (int, []byte, error) {
	start := 0
	for start < len(data) {
		r, width := utf8.DecodeRune(data[start:])
		if r == utf8.RuneError && width <= 1 && !atEOF && !utf8.FullRune(data[start:]) {
			return start, nil, nil
		}
		if unicode.IsLetter(r) {
			break
		}
		start += width
	}
	for i := start; i < len(data); {
		r, width := utf8.DecodeRune(data[i:])
		if r == utf8.RuneError && width <= 1 && !atEOF && !utf8.FullRune(data[i:]) {
			break
		}
		if !unicode.IsLetter(r) {
			return i + width, data[start:i], nil
		}
		i += width
	}
	if atEOF && start < len(data) {
		return len(data), data[start:], nil
	}
	return start, nil, nil
}

// overlapMarkStream flags every candidate shingle the stream contains.
func overlapMarkStream(stream io.Reader, candidates map[string]bool) error {
	scanner := bufio.NewScanner(stream)
	scanner.Buffer(make([]byte, 1<<20), 1<<26)
	scanner.Split(overlapSplitWords)
	window := make([]string, 0, overlapShingleWords)
	for scanner.Scan() {
		if len(window) == overlapShingleWords {
			copy(window, window[1:])
			window = window[:overlapShingleWords-1]
		}
		window = append(window, scanner.Text())
		if len(window) == overlapShingleWords {
			key := overlapShingle(window)
			if _, held := candidates[key]; held {
				candidates[key] = true
			}
		}
	}
	return scanner.Err()
}

// overlapStoreStrings returns a store record's text fields. Records are parsed
// rather than streamed because public and private records share one schema, and
// a run of field names would otherwise read as copied text.
func overlapStoreStrings(data []byte) ([]string, error) {
	var record interface{}
	if err := yaml.Unmarshal(data, &record); err != nil {
		return nil, err
	}
	var texts []string
	var walk func(interface{})
	walk = func(value interface{}) {
		switch typed := value.(type) {
		case string:
			if len(typed) > overlapMinimumStoreString {
				texts = append(texts, typed)
			}
		case map[string]interface{}:
			for key, child := range typed {
				if key != "vectors" {
					walk(child)
				}
			}
		case []interface{}:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(record)
	return texts, nil
}

// overlapMarkPrivate streams the private checkout past the candidates. READMEs
// are skipped because the tracked provenance READMEs are their own canonical
// copies, and a csv is skipped when its tracked gzip sits beside it.
func overlapMarkPrivate(private string, candidates map[string]bool) (int, error) {
	files := 0
	err := filepath.WalkDir(private, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		name := entry.Name()
		if name == "README.md" || name == ".gitignore" {
			return nil
		}
		if _, err := os.Stat(path + ".gz"); err == nil {
			return nil
		}
		relative, err := filepath.Rel(private, path)
		if err != nil {
			return err
		}
		if overlapPublishablePrivateFiles[filepath.ToSlash(relative)] {
			return nil
		}
		files++
		if strings.HasPrefix(filepath.ToSlash(relative), "store/") && strings.HasSuffix(name, corpusRecordExtension) {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			texts, err := overlapStoreStrings(data)
			if err != nil {
				return fmt.Errorf("parse %s: %w", relative, err)
			}
			for _, text := range texts {
				if err := overlapMarkStream(strings.NewReader(text), candidates); err != nil {
					return err
				}
			}
			return nil
		}
		handle, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() { _ = handle.Close() }()
		var stream io.Reader = handle
		if strings.HasSuffix(name, ".gz") {
			unzipped, err := gzip.NewReader(handle)
			if err != nil {
				return fmt.Errorf("gunzip %s: %w", relative, err)
			}
			defer func() { _ = unzipped.Close() }()
			stream = unzipped
		}
		if err := overlapMarkStream(stream, candidates); err != nil {
			return fmt.Errorf("scan %s: %w", relative, err)
		}
		return nil
	})
	return files, err
}

// overlapRuns merges the shingles found in private text back into maximal runs
// per tracked file, keeping the original characters so a reviewer reads what is
// actually there.
func overlapRuns(tracked map[string]string, candidates map[string]bool) []overlapRun {
	var runs []overlapRun
	for file, text := range tracked {
		type span struct{ start, end int }
		var words []span
		var tokens []string
		inside, start := false, 0
		for i, r := range text {
			if unicode.IsLetter(r) {
				if !inside {
					inside, start = true, i
				}
				continue
			}
			if inside {
				words = append(words, span{start, i})
				tokens = append(tokens, text[start:i])
				inside = false
			}
		}
		if inside {
			words = append(words, span{start, len(text)})
			tokens = append(tokens, text[start:])
		}
		hit := make([]bool, len(tokens))
		for i := 0; i+overlapShingleWords <= len(tokens); i++ {
			if candidates[overlapShingle(tokens[i:i+overlapShingleWords])] {
				for j := i; j < i+overlapShingleWords; j++ {
					hit[j] = true
				}
			}
		}
		for i := 0; i < len(hit); i++ {
			if !hit[i] {
				continue
			}
			j := i
			for j+1 < len(hit) && hit[j+1] {
				j++
			}
			runs = append(runs, overlapRun{
				File: file, Words: j - i + 1, Text: text[words[i].start:words[j].end],
			})
			i = j
		}
	}
	sort.Slice(runs, func(i, j int) bool {
		if runs[i].Words != runs[j].Words {
			return runs[i].Words > runs[j].Words
		}
		if runs[i].File != runs[j].File {
			return runs[i].File < runs[j].File
		}
		return runs[i].Text < runs[j].Text
	})
	return runs
}

// overlapUnreviewed returns the runs no review entry covers.
func overlapUnreviewed(runs []overlapRun, reviewed []overlapReviewedEntry) []overlapRun {
	known := make(map[string]bool, len(reviewed))
	for _, entry := range reviewed {
		known[entry.File+"\x00"+entry.Fingerprint] = true
	}
	var unreviewed []overlapRun
	for _, run := range runs {
		if !known[run.File+"\x00"+run.fingerprint()] {
			unreviewed = append(unreviewed, run)
		}
	}
	return unreviewed
}

// overlapTrackedFiles reads every tracked text file. The review file is
// excluded: it names runs by fingerprint, and sweeping it adds nothing.
func overlapTrackedFiles(root string) (map[string]string, error) {
	command := exec.Command("git", "ls-files")
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	tracked := map[string]string{}
	for _, file := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if file == "" || file == overlapReviewedPath {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(file))
		info, err := os.Lstat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if utf8.Valid(data) {
			tracked[file] = string(data)
		}
	}
	return tracked, nil
}

// Overlap fails when a tracked file shares an unreviewed run of eight or more
// words with the private data checkout. It needs that checkout, so it runs on an
// operator's machine ahead of every public export (docs/guides/going-public.md).
func (Corpus) Overlap() error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	private := corpusPrivateRoot(root)
	if private == "" {
		return corpusMissingPrivateStore(root)
	}
	tracked, err := overlapTrackedFiles(root)
	if err != nil {
		return err
	}
	candidates := overlapCandidates(tracked)
	scanned, err := overlapMarkPrivate(private, candidates)
	if err != nil {
		return err
	}
	runs := overlapRuns(tracked, candidates)
	var review overlapReviewedFile
	if data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(overlapReviewedPath))); err == nil {
		if err := yaml.Unmarshal(data, &review); err != nil {
			return fmt.Errorf("parse %s: %w", overlapReviewedPath, err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	unreviewed := overlapUnreviewed(runs, review.Reviewed)
	fmt.Printf("corpus:overlap: %d tracked files against %d private files: %d runs, %d unreviewed\n",
		len(tracked), scanned, len(runs), len(unreviewed))
	for _, run := range unreviewed {
		text := strings.Join(strings.Fields(run.Text), " ")
		if len(text) > 320 {
			text = strings.ToValidUTF8(text[:320], "") + "..."
		}
		fmt.Printf("  %4d words  %s  %s\n    %s\n", run.Words, run.fingerprint(), run.File, text)
	}
	if len(unreviewed) > 0 {
		return fmt.Errorf("%d run(s) shared with private text are unreviewed; remove the text or"+
			" record a reason in %s", len(unreviewed), overlapReviewedPath)
	}
	return nil
}
