// Package runbooks implements keyword-overlap retrieval over the markdown
// runbook library. This is an honest port of the original Python
// "local scan" fallback — it is explicitly NOT semantic search and must
// never be presented as "RAG" or "embedding-based" retrieval. The original
// Python implementation's SHA-256 hash-bucket "embeddings" (confirmed in
// docs/migration-plan.md as a defect) are not reproduced here at all: this
// package only does the honest keyword-overlap method, reported truthfully
// via MatchMethod in every result.
package runbooks

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// MatchMethod is the fixed, honest string reported to API clients for how
// a result was found. It must never be changed to imply a model this
// package does not run (e.g. "embedding-cosine") unless that method is
// actually implemented.
const MatchMethod = "keyword-overlap"

// Result is one retrieved runbook.
type Result struct {
	Title       string
	Source      string
	Content     string
	MatchMethod string
	Score       int
}

// Library loads and searches the runbook markdown files from a directory.
type Library struct {
	dir  string
	docs []document
}

type document struct {
	title   string
	source  string
	content string
	lower   string // precomputed lowercase content for scoring
	stem    string // lowercase filename stem, matched against query terms too
}

// Load reads all *.md files from dir into memory. It returns an error if
// the directory cannot be read; a directory with zero *.md files is not an
// error (Retrieve simply returns no results), since an empty runbook set is
// a valid (if degenerate) configuration.
func Load(dir string) (*Library, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	lib := &Library{dir: dir}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		stem := strings.TrimSuffix(e.Name(), ".md")
		lib.docs = append(lib.docs, document{
			title:   titleCase(stem),
			source:  e.Name(),
			content: string(content),
			lower:   strings.ToLower(string(content)),
			stem:    strings.ToLower(stem),
		})
	}
	// Deterministic ordering regardless of filesystem directory order.
	sort.Slice(lib.docs, func(i, j int) bool { return lib.docs[i].source < lib.docs[j].source })
	return lib, nil
}

// Retrieve returns up to `limit` runbooks ranked by keyword overlap between
// the query and each document's content/filename stem. Ties are broken by
// source filename for determinism (same as Load's ordering).
func (l *Library) Retrieve(query string, limit int) []Result {
	terms := queryTerms(query)
	type scored struct {
		doc   document
		score int
	}
	var candidates []scored
	for _, d := range l.docs {
		score := 0
		for _, term := range terms {
			if term == "" {
				continue
			}
			if strings.Contains(d.lower, term) || strings.Contains(d.stem, term) {
				score++
			}
		}
		candidates = append(candidates, scored{doc: d, score: score})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].doc.source < candidates[j].doc.source
	})

	if limit <= 0 || limit > len(candidates) {
		limit = len(candidates)
	}
	out := make([]Result, 0, limit)
	for _, c := range candidates[:limit] {
		out = append(out, Result{
			Title:       c.doc.title,
			Source:      c.doc.source,
			Content:     c.doc.content,
			MatchMethod: MatchMethod,
			Score:       c.score,
		})
	}
	return out
}

func queryTerms(query string) []string {
	q := strings.ToLower(strings.ReplaceAll(query, "_", " "))
	return strings.Fields(q)
}

func titleCase(stem string) string {
	words := strings.FieldsFunc(stem, func(r rune) bool { return r == '_' || r == '-' })
	for i, w := range words {
		if w == "" {
			continue
		}
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}
