// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// The rel13.1 generator (srd014 R7): a second labeled corpus built backwards
// from ground truth. References are sampled first, a report is composed from
// those references alone, and the tasking derives from the report, so the
// label exists before the question does and independence from any evaluation
// run holds by construction (R7.6). The result is committed, versioned, and
// never merged into the analyst-labeled corpus (R7.4).

const (
	generatedCorpusPath          = "evaluation/generated-corpus.yaml"
	generatedCorpusVersion       = "1.0"
	generatedPromptVersion       = "1.1"
	generatedSetsEnv             = "EVAL_GENERATE_SETS"
	generatedSeedEnv             = "EVAL_GENERATE_SEED"
	generatedDefaultSets         = 30
	generatedDefaultSeed         = 13
	generatedMinDocsPerSet       = 3
	generatedMaxDocsPerSet       = 4
	generatedMaxChunksPerDoc     = 2
	generatedChatTimeout         = 90 * time.Second
	cohereChatPath               = "/v2/chat"
	generatedReportPromptHeader  = "Write a short intelligence report of 120 to 180 words grounded ONLY in the excerpts below. Lead with the concrete events, actors, places, and dates the excerpts describe; state only what the excerpts support. Do not mention excerpt numbers, identifiers, file names, or that you were given excerpts.\n\n"
	generatedTaskingPromptHeader = "You are an intelligence analyst. Derive one specific tasking (a single sentence, phrased as an analyst would file it) that the following report answers and a report on a different week or a different topic would not. Anchor it in the concrete actors, locations, systems, events, and timeframe the report covers. Do not mention sources or identifiers. Reply with the tasking sentence only.\n\nReport:\n"
)

// generatedEvaluationCorpus is the committed artifact. It mirrors the
// analyst-labeled corpus's identity discipline (versioned, provenance-carrying,
// synthetic-labeled) while remaining a separate file with its own denominator.
type generatedEvaluationCorpus struct {
	ID                     string              `yaml:"id"`
	Version                string              `yaml:"version"`
	RetrievalCorpus        string              `yaml:"retrieval_corpus"`
	RetrievalCorpusVersion string              `yaml:"retrieval_corpus_version"`
	Synthetic              string              `yaml:"synthetic"`
	SyntheticNotice        string              `yaml:"synthetic_notice"`
	Generator              generatedProvenance `yaml:"generator"`
	Taskings               []generatedTasking  `yaml:"taskings"`
}

type generatedProvenance struct {
	Model         string `yaml:"model"`
	PromptVersion string `yaml:"prompt_version"`
	Seed          int64  `yaml:"seed"`
	SampledFrom   int    `yaml:"sampled_from_embedded_chunks"`
}

type generatedTasking struct {
	ID        string `yaml:"id"`
	Clearance string `yaml:"clearance"`
	Text      string `yaml:"text"`
	// The report the tasking was derived from. It is composed from corpus excerpts,
	// so it inherits their keep-private verdict (srd017 R9.7): the generator writes
	// it to the private checkout and it never reaches the tracked file.
	Report   string `yaml:"report,omitempty"`
	Relevant []struct {
		ID     string `yaml:"id"`
		Source string `yaml:"source"`
	} `yaml:"relevant"`
}

func loadGeneratedEvaluationCorpus(root string) (generatedEvaluationCorpus, error) {
	var corpus generatedEvaluationCorpus
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(generatedCorpusPath)))
	if err != nil {
		return corpus, fmt.Errorf("read generated evaluation corpus: %w", err)
	}
	if err := yaml.Unmarshal(data, &corpus); err != nil {
		return corpus, fmt.Errorf("parse generated evaluation corpus: %w", err)
	}
	if corpus.Version == "" || len(corpus.Taskings) == 0 {
		return corpus, fmt.Errorf("generated evaluation corpus is unversioned or empty")
	}
	return corpus, nil
}

// generatedReferenceSet is one sampled ground truth: the documents the tasking
// will be scored against, with the excerpt texts the report is grounded in.
type generatedReferenceSet struct {
	Documents []string
	Tier      string
	Clearance string
	Excerpts  []string
}

// documentTier is the leading alphabetic segments of a document id, before the
// first numeric token: isw-2022-12-16 -> isw, tg-rian_ru-187503 -> tg-rian_ru.
// Windows are cut within a tier so a set's documents share a reporting stream.
func documentTier(docID string) string {
	parts := strings.Split(docID, "-")
	var kept []string
	for _, part := range parts {
		if part != "" && part[0] >= '0' && part[0] <= '9' {
			break
		}
		kept = append(kept, part)
	}
	if len(kept) == 0 {
		return docID
	}
	return strings.Join(kept, "-")
}

// generatedClearanceRank orders a level by the manifest's totally ordered
// set; an undeclared level ranks highest, failing closed like the store build.
func generatedClearanceRank(manifest corpusManifest, level string) int {
	for index, declared := range manifest.ClearanceLevels {
		if declared == level {
			return index
		}
	}
	return len(manifest.ClearanceLevels)
}

// sampleGeneratedReferenceSets cuts k windows of consecutive documents within
// a tier from the embedded (retrievable) chunk population. Sampling is fully
// determined by rng, so a seed reproduces the corpus. Only chunks carrying a
// vector participate: a reference that was never embedded cannot be retrieved,
// and scoring it measures the selection, not the retrieval.
func sampleGeneratedReferenceSets(
	records map[string]corpusChunkRecord,
	manifest corpusManifest,
	k int,
	rng *rand.Rand,
) ([]generatedReferenceSet, int) {
	chunksByDoc := map[string][]corpusChunkRecord{}
	embedded := 0
	for _, record := range records {
		if len(record.Vectors) == 0 {
			continue
		}
		embedded++
		doc := parentDocumentID(record.ChunkID)
		if record.Parent != nil && record.Parent.DocumentID != "" {
			doc = record.Parent.DocumentID
		}
		chunksByDoc[doc] = append(chunksByDoc[doc], record)
	}
	docsByTier := map[string][]string{}
	for doc := range chunksByDoc {
		tier := documentTier(doc)
		docsByTier[tier] = append(docsByTier[tier], doc)
	}
	var tiers []string
	for tier, docs := range docsByTier {
		sort.Strings(docs)
		docsByTier[tier] = docs
		if len(docs) >= generatedMinDocsPerSet {
			tiers = append(tiers, tier)
		}
	}
	sort.Strings(tiers)
	if len(tiers) == 0 {
		return nil, embedded
	}
	seen := map[string]bool{}
	var sets []generatedReferenceSet
	for attempts := 0; len(sets) < k && attempts < k*10; attempts++ {
		tier := tiers[rng.Intn(len(tiers))]
		docs := docsByTier[tier]
		size := generatedMinDocsPerSet
		if span := generatedMaxDocsPerSet - generatedMinDocsPerSet; span > 0 {
			size += rng.Intn(span + 1)
		}
		if size > len(docs) {
			size = len(docs)
		}
		start := rng.Intn(len(docs) - size + 1)
		window := docs[start : start+size]
		key := strings.Join(window, "|")
		if seen[key] {
			continue
		}
		seen[key] = true
		set := generatedReferenceSet{Documents: append([]string(nil), window...), Tier: tier}
		rank := -1
		for _, doc := range window {
			chunks := chunksByDoc[doc]
			sort.Slice(chunks, func(i, j int) bool { return chunks[i].ChunkID < chunks[j].ChunkID })
			for index, chunk := range chunks {
				if index >= generatedMaxChunksPerDoc {
					break
				}
				set.Excerpts = append(set.Excerpts, chunk.Text)
				if r := generatedClearanceRank(manifest, chunk.Metadata.Clearance); r > rank {
					rank = r
					set.Clearance = chunk.Metadata.Clearance
				}
			}
		}
		if set.Clearance == "" {
			set.Clearance = manifest.highestClearance()
		}
		sets = append(sets, set)
	}
	return sets, embedded
}

// generatedTaskingLeaks rejects a tasking that names its ground truth (srd014
// R7.3). A tasking answered by string match produces recall evidence about
// grep, so a leak is a discard, never a shipped label.
func generatedTaskingLeaks(tasking string, set generatedReferenceSet) bool {
	lowered := strings.ToLower(tasking)
	for _, word := range []string{"excerpt", "chunk", "identifier", "corpus"} {
		if strings.Contains(lowered, word) {
			return true
		}
	}
	for _, doc := range set.Documents {
		if strings.Contains(lowered, strings.ToLower(doc)) {
			return true
		}
	}
	return false
}

// cohereChatText sends one user message to the Cohere chat operation and
// returns the assistant text, following cohereEmbedText's transport shape.
func cohereChatText(model, message string) (string, error) {
	payload, err := json.Marshal(map[string]interface{}{
		"model":    model,
		"messages": []map[string]string{{"role": "user", "content": message}},
	})
	if err != nil {
		return "", fmt.Errorf("encode chat request: %w", err)
	}
	baseURL := envOrDefault("COHERE_BASE_URL", cohereEmbedDefaultBase)
	request, err := http.NewRequest(http.MethodPost,
		strings.TrimSuffix(baseURL, "/")+cohereChatPath, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("build chat request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+cohereCredential())
	response, err := (&http.Client{Timeout: generatedChatTimeout}).Do(request)
	if err != nil {
		return "", fmt.Errorf("POST %s: %w", cohereChatPath, err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return "", fmt.Errorf("read chat response: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("POST %s returned %d for model %q: %s",
			cohereChatPath, response.StatusCode, model, strings.TrimSpace(string(body)))
	}
	var decoded struct {
		Message struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return "", fmt.Errorf("decode chat response: %w", err)
	}
	for _, block := range decoded.Message.Content {
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			return strings.TrimSpace(block.Text), nil
		}
	}
	return "", fmt.Errorf("chat response carries no text block")
}

// Generate builds the rel13.1 generated-reference corpus (srd014 R7): sample
// reference windows from the embedded chunk store, compose a report grounded
// only in each window's excerpts, derive the tasking from the report, reject
// leaks, and commit the versioned result. Generation is one-time; runs over
// the committed file cost nothing to reproduce.
func (Evaluation) Generate() error {
	if cohereCredential() == "" {
		return fmt.Errorf("evaluation:generate needs a Cohere credential (%s or %s); "+
			"refusing to emit a partial corpus", cohereCredentialEnv, cohereCredentialFile)
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	documents, err := loadCorpusDocumentStore(root)
	if err != nil {
		return err
	}
	// Generation samples reference windows from the whole corpus; a store
	// missing the keep-private tiers would silently skew the sample.
	if err := requireFullCorpusStore(root, documents); err != nil {
		return err
	}
	records := map[string]corpusChunkRecord{}
	for _, document := range documents {
		for _, chunk := range document.Chunks {
			records[chunk.ChunkID] = chunk
		}
	}
	manifest, err := readCorpusManifest(root)
	if err != nil {
		return err
	}
	model, err := corpusCohereChatModel(root)
	if err != nil {
		return err
	}
	k := generatedDefaultSets
	if raw := envOrDefault(generatedSetsEnv, ""); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			k = parsed
		}
	}
	seed := int64(generatedDefaultSeed)
	if raw := envOrDefault(generatedSeedEnv, ""); raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil {
			seed = parsed
		}
	}
	sets, embedded := sampleGeneratedReferenceSets(records, manifest, k, rand.New(rand.NewSource(seed)))
	if len(sets) == 0 {
		return fmt.Errorf("the chunk store yielded no embedded reference windows to sample")
	}
	corpus := generatedEvaluationCorpus{
		ID:                     "evaluation-generated-" + manifest.ID,
		Version:                generatedCorpusVersion,
		RetrievalCorpus:        manifest.ID,
		RetrievalCorpusVersion: manifest.Version,
		Synthetic:              "mixed",
		SyntheticNotice: "These taskings were machine-generated backwards from sampled corpus " +
			"references for this demonstration. Every figure measured over them must carry " +
			"that qualification, in the evaluation output itself.",
		Generator: generatedProvenance{
			Model: model, PromptVersion: generatedPromptVersion, Seed: seed, SampledFrom: embedded,
		},
	}
	discarded := 0
	for index, set := range sets {
		var excerpts strings.Builder
		for n, text := range set.Excerpts {
			fmt.Fprintf(&excerpts, "Excerpt %d:\n%s\n\n", n+1, text)
		}
		report, err := cohereChatText(model, generatedReportPromptHeader+excerpts.String())
		if err != nil {
			return fmt.Errorf("set %d report: %w", index+1, err)
		}
		tasking := ""
		for attempt := 0; attempt < 2; attempt++ {
			candidate, err := cohereChatText(model, generatedTaskingPromptHeader+report)
			if err != nil {
				return fmt.Errorf("set %d tasking: %w", index+1, err)
			}
			if !generatedTaskingLeaks(candidate, set) {
				tasking = candidate
				break
			}
		}
		if tasking == "" {
			discarded++
			fmt.Printf("evaluation:generate: set %d discarded after leaking taskings\n", index+1)
			continue
		}
		entry := generatedTasking{
			ID:        fmt.Sprintf("gen-%03d", len(corpus.Taskings)+1),
			Clearance: set.Clearance,
			Text:      tasking,
			Report:    report,
		}
		for _, doc := range set.Documents {
			entry.Relevant = append(entry.Relevant, struct {
				ID     string `yaml:"id"`
				Source string `yaml:"source"`
			}{ID: doc, Source: set.Tier})
		}
		corpus.Taskings = append(corpus.Taskings, entry)
		fmt.Printf("evaluation:generate: %s (%s, %d refs, clearance %s)\n",
			entry.ID, set.Tier, len(entry.Relevant), entry.Clearance)
	}
	if len(corpus.Taskings) == 0 {
		return fmt.Errorf("every sampled set was discarded by the leakage guard")
	}
	if err := writeGeneratedReports(root, corpus); err != nil {
		return err
	}
	data, err := yaml.Marshal(withoutGeneratedReports(corpus))
	if err != nil {
		return err
	}
	header := "# Copyright (c) 2026 Mesh Intelligence Inc.\n" +
		"# SPDX-License-Identifier: MIT-0\n" +
		"#\n" +
		"# The generated-reference evaluation corpus (srd014 R7, rel13.1). Built\n" +
		"# backwards from ground truth by `mage evaluation:generate`: references\n" +
		"# sampled first, the report composed from them alone, the tasking derived\n" +
		"# from the report. Never merged into evaluation/corpus.yaml -- the two\n" +
		"# labeling methods keep separate denominators (R7.4). The reports are\n" +
		"# derived from keep-private tiers and live in the private data checkout\n" +
		"# (srd017 R9.7).\n"
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(generatedCorpusPath)),
		append([]byte(header), data...), 0o644); err != nil {
		return err
	}
	fmt.Printf("evaluation:generate: %d tasking(s) committed to %s (%d discarded), model %s, seed %d\n",
		len(corpus.Taskings), generatedCorpusPath, discarded, model, seed)
	return nil
}

// corpusCohereChatModel reads the declared composition model, the same one the
// deployed turn composes with, so the generated reports share its register.
func corpusCohereChatModel(root string) (string, error) {
	path := filepath.Join(root, "helm", "values.yaml")
	var values struct {
		Cohere struct {
			Model string `yaml:"model"`
		} `yaml:"cohere"`
	}
	if err := readCorpusBuildYAML(path, &values); err != nil {
		return "", err
	}
	if values.Cohere.Model == "" {
		return "", fmt.Errorf("%s declares no cohere.model", path)
	}
	return values.Cohere.Model, nil
}

// asEvaluationCorpus projects the generated corpus onto the harness's corpus
// type, so the rel13.0 recall machinery scores it unchanged (srd014 R7.4: the
// figures stay per-corpus; this converts the type, never the denominator).
func (g generatedEvaluationCorpus) asEvaluationCorpus() evaluationCorpus {
	corpus := evaluationCorpus{
		ID: g.ID, Version: g.Version,
		RetrievalCorpus: g.RetrievalCorpus, RetrievalCorpusVersion: g.RetrievalCorpusVersion,
		Synthetic: g.Synthetic, SyntheticNotice: g.SyntheticNotice,
	}
	for _, tasking := range g.Taskings {
		corpus.Taskings = append(corpus.Taskings, evaluationTasking{
			ID: tasking.ID, Clearance: tasking.Clearance, Text: tasking.Text,
			Relevant: tasking.Relevant,
		})
	}
	return corpus
}

// evalUICorpusPath is the SPA's projection of the generated corpus: taskings,
// reference document ids, and the labeling the /eval bubble must carry --
// never the reports, which are generation artifacts the browser has no use
// for. evaluation:project rewrites it from the YAML; a sync test keeps the
// two from drifting.
const evalUICorpusPath = "agents/chatbot/ui/app/src/evalCorpus.json"

type evalUICorpus struct {
	ID       string          `json:"id"`
	Version  string          `json:"version"`
	Notice   string          `json:"notice"`
	Taskings []evalUITasking `json:"taskings"`
}

type evalUITasking struct {
	ID        string   `json:"id"`
	Clearance string   `json:"clearance"`
	Text      string   `json:"text"`
	Relevant  []string `json:"relevant"`
}

func projectEvalUICorpus(corpus generatedEvaluationCorpus) evalUICorpus {
	projected := evalUICorpus{ID: corpus.ID, Version: corpus.Version, Notice: corpus.SyntheticNotice}
	for _, tasking := range corpus.Taskings {
		entry := evalUITasking{ID: tasking.ID, Clearance: tasking.Clearance, Text: tasking.Text}
		for _, relevant := range tasking.Relevant {
			entry.Relevant = append(entry.Relevant, relevant.ID)
		}
		projected.Taskings = append(projected.Taskings, entry)
	}
	return projected
}

// Project rewrites the SPA's evalCorpus.json from the committed generated
// corpus. Pure projection: no provider call, no sampling, no question changes.
func (Evaluation) Project() error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	corpus, err := loadGeneratedEvaluationCorpus(root)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(projectEvalUICorpus(corpus), "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(evalUICorpusPath)),
		append(data, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Printf("evaluation:project: %d tasking(s) projected to %s\n",
		len(corpus.Taskings), evalUICorpusPath)
	return nil
}

// generatedReportsPrivatePath is where the reports live, relative to the private
// data checkout.
const generatedReportsPrivatePath = "evaluation/generated-reports.yaml"

// withoutGeneratedReports is the corpus as the repository tracks it.
func withoutGeneratedReports(corpus generatedEvaluationCorpus) generatedEvaluationCorpus {
	stripped := corpus
	stripped.Taskings = make([]generatedTasking, len(corpus.Taskings))
	copy(stripped.Taskings, corpus.Taskings)
	for index := range stripped.Taskings {
		stripped.Taskings[index].Report = ""
	}
	return stripped
}

// writeGeneratedReports records each report beside the keep-private tiers it was
// composed from, keyed by tasking, so the provenance of a tasking stays
// recoverable by anyone holding the material it derives from.
func writeGeneratedReports(root string, corpus generatedEvaluationCorpus) error {
	private := corpusPrivateRoot(root)
	if private == "" {
		return corpusMissingPrivateStore(root)
	}
	reports := make(map[string]string, len(corpus.Taskings))
	for _, tasking := range corpus.Taskings {
		reports[tasking.ID] = tasking.Report
	}
	data, err := yaml.Marshal(reports)
	if err != nil {
		return err
	}
	path := filepath.Join(private, filepath.FromSlash(generatedReportsPrivatePath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeFileAtomically(path, data)
}
