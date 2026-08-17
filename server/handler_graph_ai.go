package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/graph"
	"golang.org/x/sync/errgroup"
)

const (
	maxSummaryBatch     = 12
	summaryConcurrency  = 3
	summaryInstructions = "You label modules of a codebase. Given a module's path, files, and key symbols, answer with ONE short sentence fragment (at most 12 words) describing what the module does. Lowercase start, no quotes, no trailing period, no restating the path."
)

// generationTarget resolves a role ("" = the session's main model, "utility",
// "plan") to a concrete model plus its lowest supported reasoning effort —
// summarizing and ranking need no deliberation, so the cheapest effort wins.
// Unknown capability keeps effort unset rather than guessing.
func (s *Server) generationTarget(role string) (model, effort string) {
	if s.config.RoleModel != nil {
		if option, ok := s.config.RoleModel(role); ok {
			if len(option.Efforts) > 0 {
				effort = option.Efforts[0]
			}
			if option.ID != "" {
				return option.ID, effort
			}
		}
	}
	if role == "" && s.config.Model != nil {
		model = s.config.Model()
	}
	return model, effort
}

type summaryEntry struct {
	Digest  string `json:"digest"`
	Summary string `json:"summary"`
}

type summaryStore struct {
	mu      sync.Mutex
	path    string
	loaded  bool
	entries map[string]summaryEntry
}

func (s *summaryStore) load() {
	if s.loaded {
		return
	}
	s.loaded = true
	s.entries = map[string]summaryEntry{}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	_ = json.Unmarshal(data, &s.entries)
}

func (s *summaryStore) save() {
	data, err := json.Marshal(s.entries)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(s.path, data, 0o644)
}

func (s *Server) moduleSummaries() *summaryStore {
	s.summariesMu.Lock()
	defer s.summariesMu.Unlock()
	if s.summaries == nil {
		s.summaries = &summaryStore{
			path: filepath.Join(s.workspace.GraphStateDir(), "summaries.json"),
		}
	}
	return s.summaries
}

type graphSummariesRequest struct {
	Modules    []string `json:"modules"`
	CachedOnly bool     `json:"cached_only"`
}

func (s *Server) handleGraphSummaries(w http.ResponseWriter, r *http.Request) {
	engine := s.graphEngine(w)
	if engine == nil {
		return
	}

	var request graphSummariesRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	if err := decoder.Decode(&request); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	limit := maxSummaryBatch
	if request.CachedOnly {
		limit = 300
	}
	if len(request.Modules) > limit {
		request.Modules = request.Modules[:limit]
	}

	profiles, err := engine.ModuleProfiles(r.Context(), request.Modules, 20)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	store := s.moduleSummaries()
	store.mu.Lock()
	store.load()

	type job struct {
		module  string
		profile graph.ModuleProfile
	}
	summaries := map[string]string{}
	var jobs []job
	for _, module := range request.Modules {
		profile, ok := profiles[module]
		if !ok || len(profile.Files) == 0 {
			continue
		}
		if entry, ok := store.entries[module]; ok && entry.Digest == profile.Digest {
			summaries[module] = entry.Summary
			continue
		}
		if !request.CachedOnly {
			jobs = append(jobs, job{module: module, profile: profile})
		}
	}
	store.mu.Unlock()

	if len(jobs) > 0 {
		var resultsMu sync.Mutex
		group, ctx := errgroup.WithContext(r.Context())
		group.SetLimit(summaryConcurrency)
		generated := map[string]summaryEntry{}
		for _, j := range jobs {
			group.Go(func() error {
				summary, err := s.generateModuleSummary(ctx, j.profile)
				if err != nil || summary == "" {
					return nil
				}
				resultsMu.Lock()
				generated[j.module] = summaryEntry{Digest: j.profile.Digest, Summary: summary}
				resultsMu.Unlock()
				return nil
			})
		}
		_ = group.Wait()

		if len(generated) > 0 {
			store.mu.Lock()
			for module, entry := range generated {
				store.entries[module] = entry
				summaries[module] = entry.Summary
			}
			store.save()
			store.mu.Unlock()
		}
	}

	writeJSON(w, map[string]any{"summaries": summaries})
}

func (s *Server) generateModuleSummary(ctx context.Context, profile graph.ModuleProfile) (string, error) {
	var input strings.Builder
	fmt.Fprintf(&input, "Module: %s\n", profile.Module)
	fmt.Fprintf(&input, "Files: %s\n", strings.Join(trimList(profile.Files, 30), ", "))
	input.WriteString("Key symbols:\n")
	for _, node := range profile.Symbols {
		fmt.Fprintf(&input, "- %s (%s)\n", node.Name, node.Kind)
	}

	model, effort := s.generationTarget("utility")
	result, err := s.config.Generate(ctx, agent.GenerateOptions{
		Model:           model,
		Effort:          effort,
		Instructions:    summaryInstructions,
		Input:           input.String(),
		MaxOutputTokens: 600,
	})
	if err != nil {
		return "", err
	}
	summary := strings.TrimSpace(strings.Trim(result.Text, `"'.`))
	if runes := []rune(summary); len(runes) > 120 {
		summary = string(runes[:120])
	}
	return summary, nil
}

func trimList(values []string, limit int) []string {
	if len(values) <= limit {
		return values
	}
	out := append([]string{}, values[:limit]...)
	return append(out, fmt.Sprintf("… %d more", len(values)-limit))
}
