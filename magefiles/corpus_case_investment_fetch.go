// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Fetching the case's real tiers (srd018 R2). The results are tracked, so a build
// needs no network; this runs only when the tiers are refreshed. Each source's reuse
// terms are recorded in its tier README, and a source enters only after they are read.

const (
	investmentContactPath = "cases/investment-research/fetch.local.yaml"
	// Under the SEC's published ten requests per second, and applied to every host.
	investmentRequestInterval = 250 * time.Millisecond
	investmentArticleCap      = 60
)

// investmentIssuers are the filers filing_facts reads. The analyst-note guard also
// reads this list, so a note cannot name one of them (srd018 R4.2).
var investmentIssuers = []struct{ Name, Ticker, CIK string }{
	{"Exxon Mobil Corporation", "XOM", "0000034088"},
	{"Chevron Corporation", "CVX", "0000093410"},
	{"ConocoPhillips", "COP", "0001163165"},
	{"EOG Resources, Inc.", "EOG", "0000821189"},
	{"Occidental Petroleum Corporation", "OXY", "0000797468"},
	{"Marathon Petroleum Corporation", "MPC", "0001510295"},
}

// investmentFOMCDates are the 2024 statement dates from the Board's meeting calendar.
var investmentFOMCDates = []string{
	"20240131", "20240320", "20240501", "20240612", "20240731", "20240918", "20241107", "20241218",
}

// investmentCPIDates are the 2024 Consumer Price Index release dates.
var investmentCPIDates = []string{
	"01112024", "02132024", "03122024", "04102024", "05152024", "06122024",
	"07112024", "08142024", "09112024", "10102024", "11132024", "12112024",
}

// investmentArticleTopics decides which EIA articles the sector study reads, on
// EIA's own tags.
var investmentArticleTopics = []string{
	"oil/petroleum", "crude oil", "liquid fuels", "natural gas", "lng", "liquefied natural gas",
	"refineries", "refining", "gasoline", "diesel", "pipelines", "production/supply", "tight oil", "shale",
}

type investmentFetcher struct {
	client    *http.Client
	userAgent string
	last      time.Time
}

// get fetches one URL, spaced from the previous request, and fails on any status
// but 200, so a refused page never lands in the corpus as text.
func (f *investmentFetcher) get(url string) (string, error) {
	if wait := investmentRequestInterval - time.Since(f.last); wait > 0 {
		time.Sleep(wait)
	}
	f.last = time.Now()
	request, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("User-Agent", f.userAgent)
	response, err := f.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("fetch %s: %w", url, err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", url, err)
	}
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch %s: status %d", url, response.StatusCode)
	}
	return string(body), nil
}

// investmentOperatorContact reads the contact the SEC's access rules ask a script to
// declare. It lives in a gitignored file so it never reaches a tracked one (R2.6).
func investmentOperatorContact(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(investmentContactPath)))
	if err != nil {
		return "", fmt.Errorf("corpus:fetch needs an operator contact for the SEC user agent:"+
			" write `contact: <email>` to %s, which git ignores: %w", investmentContactPath, err)
	}
	var file struct {
		Contact string `yaml:"contact"`
	}
	if err := yaml.Unmarshal(data, &file); err != nil {
		return "", fmt.Errorf("parse %s: %w", investmentContactPath, err)
	}
	if strings.TrimSpace(file.Contact) == "" {
		return "", fmt.Errorf("%s declares no contact", investmentContactPath)
	}
	return strings.TrimSpace(file.Contact), nil
}

// Fetch refreshes the investment-research case's real tiers from their sources and
// writes them under the case's data directory.
func (Corpus) Fetch() error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	contact, err := investmentOperatorContact(root)
	if err != nil {
		return err
	}
	fetcher := &investmentFetcher{
		client:    &http.Client{Timeout: 60 * time.Second},
		userAgent: "Mesh Intelligence cohere-demo " + contact,
	}
	dataRoot := filepath.Join(root, corpusCasesDir, investmentCaseName, corpusCaseDataDir)

	facts, err := fetchInvestmentFacts(fetcher)
	if err != nil {
		return err
	}
	if err := writeInvestmentJSONL(dataRoot, investmentFactsPath, facts); err != nil {
		return err
	}
	articles, err := fetchInvestmentArticles(fetcher)
	if err != nil {
		return err
	}
	if err := writeInvestmentJSONL(dataRoot, investmentAnalysisPath, articles); err != nil {
		return err
	}
	releases, err := fetchInvestmentReleases(fetcher)
	if err != nil {
		return err
	}
	if err := writeInvestmentJSONL(dataRoot, investmentMacroPath, releases); err != nil {
		return err
	}
	fmt.Printf("corpus:fetch: %d facts, %d articles, %d releases under %s\n",
		len(facts), len(articles), len(releases), dataRoot)
	return nil
}

func writeInvestmentJSONL[T any](dataRoot, relative string, records []T) error {
	var builder strings.Builder
	encoder := json.NewEncoder(&builder)
	encoder.SetEscapeHTML(false)
	for _, record := range records {
		if err := encoder.Encode(record); err != nil {
			return err
		}
	}
	path := filepath.Join(dataRoot, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeFileAtomically(path, []byte(builder.String()))
}

func fetchInvestmentFacts(fetcher *investmentFetcher) ([]investmentFact, error) {
	var facts []investmentFact
	for _, issuer := range investmentIssuers {
		body, err := fetcher.get("https://data.sec.gov/api/xbrl/companyfacts/CIK" + issuer.CIK + ".json")
		if err != nil {
			return nil, err
		}
		selected, err := selectInvestmentFacts(body, issuer.Name, issuer.Ticker, issuer.CIK)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", issuer.Ticker, err)
		}
		facts = append(facts, selected...)
	}
	return facts, nil
}

// selectInvestmentFacts keeps the concepts this case reads, in USD, filed in the
// window on a periodic report. The taxonomy's labels and descriptions in the
// response are dropped: only the numbers and their identifiers are kept.
func selectInvestmentFacts(body, name, ticker, cik string) ([]investmentFact, error) {
	// Only the concepts read here are decoded further: the response also holds
	// per-share decimals and the taxonomy's own descriptions, which this tier leaves.
	var response struct {
		Facts map[string]map[string]struct {
			Units map[string]json.RawMessage `json:"units"`
		} `json:"facts"`
	}
	if err := json.Unmarshal([]byte(body), &response); err != nil {
		return nil, err
	}
	var facts []investmentFact
	for _, entry := range investmentConceptLabels {
		raw, reported := response.Facts["us-gaap"][entry.Concept].Units["USD"]
		if !reported {
			continue
		}
		var rows []struct {
			Start string  `json:"start"`
			End   string  `json:"end"`
			Val   float64 `json:"val"`
			Accn  string  `json:"accn"`
			FY    int     `json:"fy"`
			FP    string  `json:"fp"`
			Form  string  `json:"form"`
			Filed string  `json:"filed"`
		}
		if err := json.Unmarshal(raw, &rows); err != nil {
			return nil, fmt.Errorf("%s: %w", entry.Concept, err)
		}
		for _, row := range rows {
			if row.Filed < investmentWindowStart || row.Filed > investmentWindowEnd {
				continue
			}
			if row.Form != "10-K" && row.Form != "10-Q" {
				continue
			}
			facts = append(facts, investmentFact{
				Issuer: name, Ticker: ticker, CIK: cik, Concept: entry.Concept,
				Value: int64(row.Val), Unit: "USD", Start: row.Start, End: row.End,
				Form: row.Form, FiscalPer: row.FP, FiscalYr: row.FY,
				Accession: row.Accn, Filed: row.Filed,
			})
		}
	}
	sort.SliceStable(facts, func(i, j int) bool {
		a, b := facts[i], facts[j]
		if a.Accession != b.Accession {
			return a.Accession < b.Accession
		}
		if a.Concept != b.Concept {
			return a.Concept < b.Concept
		}
		if a.End != b.End {
			return a.End < b.End
		}
		return a.Start < b.Start
	})
	return facts, nil
}

var (
	investmentTagPattern      = regexp.MustCompile(`(?is)<[^>]+>`)
	investmentScriptPattern   = regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</(script|style)>`)
	investmentSpacePattern    = regexp.MustCompile(`\s+`)
	investmentParagraph       = regexp.MustCompile(`(?is)<p\b[^>]*>(.*?)</p>`)
	investmentParagraphOpener = regexp.MustCompile(`(?i)<p\b[^>]*>`)
	investmentArchiveRow      = regexp.MustCompile(`(?is)<span class="date">\s*([^<]+?)\s*</span>\s*<h2>\s*<a href="detail\.php\?id=(\d+)">(.*?)</a>`)
	investmentSourceBlock     = regexp.MustCompile(`(?is)<div class="source">(.*?)</div>`)
	investmentTagsBlock       = regexp.MustCompile(`(?is)Tags:(.*?)</(?:div|p)>`)
	investmentPreBlock        = regexp.MustCompile(`(?is)<pre[^>]*>(.*?)</pre>`)
	investmentFOMCArticle     = regexp.MustCompile(`(?is)<div id="article">(.*?)<div id="lastUpdate"`)
)

// investmentParagraphs returns a page fragment's paragraphs as plain text. Government
// pages leave some <p> tags unclosed, and a non-greedy match runs an unclosed one into
// the next paragraph, so each match is split at its inner openers. Nothing is dropped
// here: the callers decide what a line is.
func investmentParagraphs(fragment string) []string {
	var paragraphs []string
	for _, match := range investmentParagraph.FindAllStringSubmatch(fragment, -1) {
		for _, part := range investmentParagraphOpener.Split(match[1], -1) {
			if text := investmentPlainText(part); text != "" {
				paragraphs = append(paragraphs, text)
			}
		}
	}
	return paragraphs
}

func investmentPlainText(fragment string) string {
	text := investmentScriptPattern.ReplaceAllString(fragment, " ")
	text = investmentTagPattern.ReplaceAllString(text, " ")
	text = html.UnescapeString(text)
	text = strings.ReplaceAll(text, " ", " ")
	text = investmentSpacePattern.ReplaceAllString(text, " ")
	text = strings.NewReplacer(" ,", ",", " .", ".", " ;", ";", "( ", "(", " )", ")").Replace(text)
	return strings.TrimSpace(text)
}

// investmentListedArticle is one row of EIA's yearly archive. The archive carries no
// tags, so the topic decision is made from the article page.
type investmentListedArticle struct {
	ID    string
	Date  string
	Title string
}

// parseInvestmentArchive reads EIA's archive listing, one row per article.
func parseInvestmentArchive(page string) []investmentListedArticle {
	var listed []investmentListedArticle
	for _, row := range investmentArchiveRow.FindAllStringSubmatch(page, -1) {
		parsed, err := time.Parse("January 2, 2006", strings.TrimSpace(row[1]))
		if err != nil {
			continue
		}
		listed = append(listed, investmentListedArticle{
			ID: row[2], Date: parsed.Format(corpusEnvelopeDateLayout), Title: investmentPlainText(row[3]),
		})
	}
	return listed
}

// investmentArticleTags reads the tags EIA puts on an article page.
func investmentArticleTags(page string) string {
	match := investmentTagsBlock.FindStringSubmatch(page)
	if match == nil {
		return ""
	}
	return strings.ToLower(investmentPlainText(match[1]))
}

func investmentArticleOnTopic(tags string) bool {
	for _, topic := range investmentArticleTopics {
		if strings.Contains(tags, topic) {
			return true
		}
	}
	return false
}

// investmentSpreadSelection takes at most cap items evenly across the list, so the
// window stays covered rather than ending wherever the cap fell.
func investmentSpreadSelection[T any](items []T, limit int) []T {
	if len(items) <= limit {
		return items
	}
	selected := make([]T, 0, limit)
	for i := 0; i < limit; i++ {
		selected = append(selected, items[i*len(items)/limit])
	}
	return selected
}

func fetchInvestmentArticles(fetcher *investmentFetcher) ([]investmentArticle, error) {
	page, err := fetcher.get("https://www.eia.gov/todayinenergy/archive.php?my=2024")
	if err != nil {
		return nil, err
	}
	var listed []investmentListedArticle
	for _, row := range parseInvestmentArchive(page) {
		if row.Date >= investmentWindowStart && row.Date <= investmentWindowEnd {
			listed = append(listed, row)
		}
	}
	if len(listed) == 0 {
		return nil, fmt.Errorf("EIA's 2024 archive listed no article; the page shape may have changed")
	}
	sort.SliceStable(listed, func(i, j int) bool {
		if listed[i].Date != listed[j].Date {
			return listed[i].Date < listed[j].Date
		}
		return listed[i].ID < listed[j].ID
	})
	var candidates []investmentArticle
	offTopic, refused := 0, 0
	for _, row := range listed {
		url := "https://www.eia.gov/todayinenergy/detail.php?id=" + row.ID
		body, err := fetcher.get(url)
		if err != nil {
			return nil, err
		}
		if !investmentArticleOnTopic(investmentArticleTags(body)) {
			offTopic++
			continue
		}
		article, ok := parseInvestmentArticle(body, row, url)
		if !ok {
			refused++
			continue
		}
		candidates = append(candidates, article)
	}
	articles := investmentSpreadSelection(candidates, investmentArticleCap)
	fmt.Printf("corpus:fetch: EIA: %d listed, %d off topic, %d refused, %d on topic, %d kept\n",
		len(listed), offTopic, refused, len(candidates), len(articles))
	if len(articles) == 0 {
		return nil, fmt.Errorf("no EIA article survived; the page shape may have changed")
	}
	return articles, nil
}

// parseInvestmentArticle takes the article's own paragraphs. It refuses a page with a
// third-party rights notice, because EIA's terms say licensed material can appear on
// its pages and the public-domain statement does not reach it (srd018 R2.3).
func parseInvestmentArticle(page string, listed investmentListedArticle, url string) (investmentArticle, bool) {
	start := strings.Index(page, `class="tie-article"`)
	if start < 0 {
		return investmentArticle{}, false
	}
	body := page[start:]
	if end := strings.Index(body, "Principal contributor"); end >= 0 {
		body = body[:end]
	} else if end := strings.Index(body, "Tags:"); end >= 0 {
		body = body[:end]
	}
	article := investmentArticle{ID: listed.ID, Date: listed.Date, Title: listed.Title, URL: url}
	if match := investmentSourceBlock.FindStringSubmatch(body); match != nil {
		article.DataSource = strings.TrimPrefix(investmentPlainText(match[1]), "Data source: ")
	}
	for _, text := range investmentParagraphs(body) {
		if credit, isCredit := strings.CutPrefix(text, "Data source:"); isCredit {
			if article.DataSource == "" {
				article.DataSource = strings.TrimSpace(credit)
			}
			continue
		}
		if len(text) < 40 {
			continue
		}
		lowered := strings.ToLower(text)
		if strings.Contains(text, "©") || strings.Contains(lowered, "copyright") || strings.Contains(lowered, "reprinted with permission") {
			return investmentArticle{}, false
		}
		article.Paragraphs = append(article.Paragraphs, text)
	}
	return article, len(article.Paragraphs) > 0
}

func fetchInvestmentReleases(fetcher *investmentFetcher) ([]investmentRelease, error) {
	var releases []investmentRelease
	for _, date := range investmentFOMCDates {
		url := "https://www.federalreserve.gov/newsevents/pressreleases/monetary" + date + "a.htm"
		body, err := fetcher.get(url)
		if err != nil {
			return nil, err
		}
		release, err := parseInvestmentFOMC(body, date, url)
		if err != nil {
			return nil, err
		}
		releases = append(releases, release)
	}
	for _, date := range investmentCPIDates {
		url := "https://www.bls.gov/news.release/archives/cpi_" + date + ".htm"
		body, err := fetcher.get(url)
		if err != nil {
			return nil, err
		}
		release, err := parseInvestmentCPI(body, date, url)
		if err != nil {
			return nil, err
		}
		releases = append(releases, release)
	}
	sort.SliceStable(releases, func(i, j int) bool { return releases[i].Date < releases[j].Date })
	return releases, nil
}

// parseInvestmentFOMC takes the statement's paragraphs, stopping before the page's
// footer and related links.
func parseInvestmentFOMC(page, date, url string) (investmentRelease, error) {
	issued := date[:4] + "-" + date[4:6] + "-" + date[6:]
	match := investmentFOMCArticle.FindStringSubmatch(page)
	if match == nil {
		return investmentRelease{}, fmt.Errorf("%s: no statement body found", url)
	}
	release := investmentRelease{
		ID: "fomc-" + issued, Publisher: "Board of Governors of the Federal Reserve System",
		Date: issued, Title: "Federal Open Market Committee statement, " + issued, URL: url,
	}
	for _, text := range investmentParagraphs(match[1]) {
		if len(text) < 60 || strings.HasPrefix(text, "For release") || strings.HasPrefix(text, "For media inquiries") {
			continue
		}
		release.Paragraphs = append(release.Paragraphs, text)
	}
	if len(release.Paragraphs) == 0 {
		return release, fmt.Errorf("%s: statement body held no paragraphs", url)
	}
	return release, nil
}

// parseInvestmentCPI takes the release's narrative, which sits in a preformatted
// block ahead of its tables, and rejoins its hard-wrapped lines.
func parseInvestmentCPI(page, date, url string) (investmentRelease, error) {
	issued := date[4:] + "-" + date[:2] + "-" + date[2:4]
	match := investmentPreBlock.FindStringSubmatch(page)
	if match == nil {
		return investmentRelease{}, fmt.Errorf("%s: no release body found", url)
	}
	release := investmentRelease{
		ID: "cpi-" + issued, Publisher: "U.S. Bureau of Labor Statistics",
		Date: issued, Title: "Consumer Price Index release, " + issued, URL: url,
	}
	raw := strings.ReplaceAll(html.UnescapeString(investmentTagPattern.ReplaceAllString(match[1], "")), "\r\n", "\n")
	for _, block := range regexp.MustCompile(`\n\s*\n`).Split(raw, -1) {
		text := investmentSpacePattern.ReplaceAllString(strings.TrimSpace(block), " ")
		lowered := strings.ToLower(text)
		if strings.HasPrefix(text, "Table A") || strings.Contains(lowered, "technical note") ||
			strings.HasPrefix(lowered, "not seasonally adjusted cpi") {
			break
		}
		if strings.HasPrefix(text, "Technical information") || strings.HasPrefix(text, "Transmission of material") {
			continue
		}
		if len(text) >= 120 && !strings.Contains(text, "....") {
			release.Paragraphs = append(release.Paragraphs, text)
		}
	}
	if len(release.Paragraphs) == 0 {
		return release, fmt.Errorf("%s: release body held no narrative", url)
	}
	return release, nil
}
