package graph

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

const (
	defaultContentLimit = 10
	maxRawContentHits   = 50
	maxContentLineBytes = 1024 * 1024
	maxContentPreview   = 300
)

type ContentSearchOpts struct {
	Pattern    string
	Regex      bool
	IgnoreCase bool
	File       string
	Glob       string
	Limit      int
	Offset     int
}

type ContentHit struct {
	Node       *Node `json:"node"`
	MatchLines []int `json:"match_lines"`
	Callers    int   `json:"callers"`
	Callees    int   `json:"callees"`
	Score      int   `json:"score"`
}

type RawContentHit struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Content string `json:"content"`
}

type ContentSearchResult struct {
	Hits            []ContentHit    `json:"hits"`
	Raw             []RawContentHit `json:"raw,omitempty"`
	TotalLineHits   int             `json:"total_line_hits"`
	TotalResults    int             `json:"total_results"`
	TotalRawResults int             `json:"total_raw_results"`
	Offset          int             `json:"offset"`
	HasMore         bool            `json:"has_more"`
	RawHasMore      bool            `json:"raw_has_more"`
}

// SearchContent scans the graph's discovered source-file set, maps each
// matching line to the tightest containing definition, and deduplicates the
// result by definition. Matches outside graph coverage remain in Raw.
func (e *Engine) SearchContent(ctx context.Context, opts ContentSearchOpts) (ContentSearchResult, error) {
	pattern := opts.Pattern
	if pattern == "" {
		return ContentSearchResult{}, fmt.Errorf("pattern is required for content search")
	}
	match, err := contentMatcher(pattern, opts.Regex, opts.IgnoreCase)
	if err != nil {
		return ContentSearchResult{}, err
	}
	if opts.Glob != "" {
		if _, err := doublestar.Match(opts.Glob, ""); err != nil {
			return ContentSearchResult{}, fmt.Errorf("invalid glob pattern: %w", err)
		}
	}

	g, err := e.ensureIndexed(ctx)
	if err != nil {
		return ContentSearchResult{}, err
	}
	e.mu.RLock()
	files := make([]string, 0, len(e.files))
	for file := range e.files {
		files = append(files, file)
	}
	e.mu.RUnlock()
	sort.Strings(files)

	byNode := make(map[string]*ContentHit)
	var raw []RawContentHit
	totalRaw := 0
	totalLines := 0
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return ContentSearchResult{}, err
		}
		if opts.File != "" && !strings.Contains(file, opts.File) {
			continue
		}
		if opts.Glob != "" && !matchesContentGlob(opts.Glob, file) {
			continue
		}

		f, err := os.Open(filepath.Join(e.root, filepath.FromSlash(file)))
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 0, 64*1024), maxContentLineBytes)
		line := 0
		for scanner.Scan() {
			line++
			text := scanner.Text()
			if !match(text) {
				continue
			}
			totalLines++
			n := g.nodeAt(file, line)
			if n == nil {
				totalRaw++
				if len(raw) < maxRawContentHits {
					raw = append(raw, RawContentHit{File: file, Line: line, Content: contentPreview(text)})
				}
				continue
			}
			hit := byNode[n.ID]
			if hit == nil {
				hit = &ContentHit{Node: n, Callers: len(g.in[n.ID]), Callees: len(g.out[n.ID])}
				byNode[n.ID] = hit
			}
			hit.MatchLines = append(hit.MatchLines, line)
		}
		_ = f.Close()
	}

	hits := make([]ContentHit, 0, len(byNode))
	for _, hit := range byNode {
		hit.Score = contentHitScore(hit)
		hits = append(hits, *hit)
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		if hits[i].Node.File != hits[j].Node.File {
			return hits[i].Node.File < hits[j].Node.File
		}
		if hits[i].Node.StartLine != hits[j].Node.StartLine {
			return hits[i].Node.StartLine < hits[j].Node.StartLine
		}
		return hits[i].Node.Name < hits[j].Node.Name
	})

	total := len(hits)
	limit := opts.Limit
	if limit <= 0 {
		limit = defaultContentLimit
	}
	offset := min(max(opts.Offset, 0), total)
	end := min(offset+limit, total)

	return ContentSearchResult{
		Hits:            hits[offset:end],
		Raw:             raw,
		TotalLineHits:   totalLines,
		TotalResults:    total,
		TotalRawResults: totalRaw,
		Offset:          offset,
		HasMore:         end < total,
		RawHasMore:      totalRaw > len(raw),
	}, nil
}

func contentMatcher(pattern string, regex, ignoreCase bool) (func(string) bool, error) {
	if regex {
		if ignoreCase {
			pattern = "(?i)" + pattern
		}
		rx, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid content regex: %w", err)
		}
		return rx.MatchString, nil
	}
	if ignoreCase {
		pattern = strings.ToLower(pattern)
		return func(line string) bool { return strings.Contains(strings.ToLower(line), pattern) }, nil
	}
	return func(line string) bool { return strings.Contains(line, pattern) }, nil
}

func matchesContentGlob(pattern, file string) bool {
	if ok, _ := doublestar.Match(pattern, path.Base(file)); ok {
		return true
	}
	ok, _ := doublestar.Match(pattern, file)
	return ok
}

func contentHitScore(hit *ContentHit) int {
	score := kindBoost(hit.Node.Kind)*10 + min(hit.Callers, 50) + min(len(hit.MatchLines), 10)
	if isTestPath(hit.Node.File) {
		score -= 100
	}
	return score
}

func isTestPath(file string) bool {
	lower := strings.ToLower("/" + filepath.ToSlash(file))
	base := strings.ToLower(path.Base(file))
	return strings.Contains(lower, "/test/") || strings.Contains(lower, "/tests/") ||
		strings.Contains(lower, "/spec/") || strings.Contains(lower, "/specs/") ||
		strings.Contains(base, "_test.") || strings.Contains(base, ".test.") ||
		strings.Contains(base, "_spec.") || strings.Contains(base, ".spec.")
}

func contentPreview(line string) string {
	line = strings.TrimSpace(line)
	runes := []rune(line)
	if len(runes) <= maxContentPreview {
		return line
	}
	return string(runes[:maxContentPreview]) + "…"
}
