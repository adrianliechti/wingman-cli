package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/changes"
)

type GitFileStatus struct {
	Path           string `json:"path"`
	OriginalPath   string `json:"original_path,omitempty"`
	IndexStatus    string `json:"index_status,omitempty"`
	WorktreeStatus string `json:"worktree_status,omitempty"`
	Staged         bool   `json:"staged"`
	Changed        bool   `json:"changed"`
	Conflict       bool   `json:"conflict,omitempty"`
}

type GitStatus struct {
	Branch    string          `json:"branch"`
	Upstream  string          `json:"upstream,omitempty"`
	Ahead     int             `json:"ahead"`
	Behind    int             `json:"behind"`
	HasRemote bool            `json:"has_remote"`
	Files     []GitFileStatus `json:"files"`
}

type GitBranch struct {
	Name    string `json:"name"`
	Remote  string `json:"remote,omitempty"`
	Current bool   `json:"current,omitempty"`
}

type GitBranches struct {
	Branches []GitBranch `json:"branches"`
	Warning  string      `json:"warning,omitempty"`
}

type GitCommit struct {
	Hash       string   `json:"hash"`
	Parents    []string `json:"parents"`
	Summary    string   `json:"summary"`
	Author     string   `json:"author"`
	AuthoredAt string   `json:"authored_at"`
	Refs       []string `json:"refs"`
}

type GitCompare struct {
	Base          string      `json:"base"`
	Head          string      `json:"head"`
	BaseHash      string      `json:"base_hash"`
	HeadHash      string      `json:"head_hash"`
	MergeBaseHash string      `json:"merge_base_hash,omitempty"`
	Files         []DiffEntry `json:"files"`
}

type gitPathsRequest struct {
	Paths []string `json:"paths"`
}

type gitCommitRequest struct {
	Message string `json:"message"`
}

type gitBranchRequest struct {
	Name   string `json:"name"`
	Remote string `json:"remote,omitempty"`
}

const gitRequestLimit = 1 << 20

func (s *Server) handleGitInit(w http.ResponseWriter, r *http.Request) {
	if err := s.workspace.GitInit(); err != nil {
		writeGitError(w, err)
		return
	}
	s.broadcast(Frame{Type: EvtCapabilitiesChanged})
	s.flushFiles()
	s.gitMutationNoContent(w)
}

func (s *Server) handleGitStatus(w http.ResponseWriter, r *http.Request) {
	status, err := s.workspace.GitStatus(r.Context())
	if err != nil {
		writeGitError(w, err)
		return
	}
	result := GitStatus{
		Branch: status.Branch, Upstream: status.Upstream,
		Ahead: status.Ahead, Behind: status.Behind, HasRemote: status.HasRemote,
		Files: make([]GitFileStatus, 0, len(status.Files)),
	}
	for _, file := range status.Files {
		result.Files = append(result.Files, GitFileStatus{
			Path: file.Path, OriginalPath: file.OriginalPath,
			IndexStatus: file.IndexStatus, WorktreeStatus: file.WorktreeStatus,
			Staged: file.Staged, Changed: file.Changed, Conflict: file.Conflict,
		})
	}
	writeJSON(w, result)
}

func (s *Server) handleGitBranches(w http.ResponseWriter, r *http.Request) {
	result, err := s.gitBranches(r.Context(), false)
	if err != nil {
		writeGitError(w, err)
		return
	}
	writeJSON(w, result)
}

func (s *Server) handleGitFetch(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	result, err := s.gitBranches(ctx, true)
	if err != nil {
		writeGitError(w, err)
		return
	}
	s.broadcast(Frame{Type: EvtDiffsChanged})
	writeJSON(w, result)
}

func (s *Server) gitBranches(ctx context.Context, refresh bool) (GitBranches, error) {
	branches, warning, err := s.workspace.GitBranches(ctx, refresh)
	if err != nil {
		return GitBranches{}, err
	}
	result := GitBranches{Branches: make([]GitBranch, 0, len(branches)), Warning: warning}
	for _, branch := range branches {
		result.Branches = append(result.Branches, GitBranch{
			Name: branch.Name, Remote: branch.Remote, Current: branch.Current,
		})
	}
	return result, nil
}

func (s *Server) handleGitHistory(w http.ResponseWriter, r *http.Request) {
	commits, err := s.workspace.GitHistory(r.Context())
	if err != nil {
		writeGitError(w, err)
		return
	}
	result := make([]GitCommit, 0, len(commits))
	for _, commit := range commits {
		result = append(result, GitCommit{
			Hash: commit.Hash, Parents: commit.Parents, Summary: commit.Summary,
			Author: commit.Author, AuthoredAt: commit.AuthoredAt.Format(time.RFC3339), Refs: commit.Refs,
		})
	}
	writeJSON(w, result)
}

func (s *Server) handleGitCompare(w http.ResponseWriter, r *http.Request) {
	base := strings.TrimSpace(r.URL.Query().Get("base"))
	head := strings.TrimSpace(r.URL.Query().Get("head"))
	if base == "" || head == "" {
		http.Error(w, "base and head revisions are required", http.StatusBadRequest)
		return
	}
	mode := r.URL.Query().Get("mode")
	if mode == "" {
		mode = "direct"
	}
	if mode != "direct" && mode != "merge-base" {
		http.Error(w, "invalid compare mode", http.StatusBadRequest)
		return
	}
	comparison, err := s.workspace.GitCompare(r.Context(), base, head, mode == "merge-base")
	if err != nil {
		writeGitError(w, err)
		return
	}
	files := make([]DiffEntry, 0, len(comparison.Diffs))
	for _, diff := range comparison.Diffs {
		// The compare view renders unified patches only; omit full file contents.
		entry := diffEntry(diff)
		entry.Original, entry.Modified = "", ""
		files = append(files, entry)
	}
	writeJSON(w, GitCompare{
		Base: base, Head: head, BaseHash: comparison.BaseHash, HeadHash: comparison.HeadHash,
		MergeBaseHash: comparison.MergeBaseHash, Files: files,
	})
}

func (s *Server) handleGitCreateBranch(w http.ResponseWriter, r *http.Request) {
	var body gitBranchRequest
	if err := decodeGitJSON(w, r, &body); err != nil {
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if err := s.workspace.GitCreateBranch(r.Context(), body.Name); err != nil {
		writeGitError(w, err)
		return
	}
	s.gitCheckoutComplete(w, "Created branch "+body.Name)
}

func (s *Server) handleGitCheckoutBranch(w http.ResponseWriter, r *http.Request) {
	var body gitBranchRequest
	if err := decodeGitJSON(w, r, &body); err != nil {
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	body.Remote = strings.TrimSpace(body.Remote)
	if err := s.workspace.GitCheckoutBranch(r.Context(), body.Name, body.Remote); err != nil {
		writeGitError(w, err)
		return
	}
	label := body.Name
	if body.Remote != "" {
		label = body.Remote + "/" + body.Name
	}
	s.gitCheckoutComplete(w, "Switched to "+label)
}

func (s *Server) handleGitStage(w http.ResponseWriter, r *http.Request) {
	var paths []string
	if r.ContentLength != 0 {
		var body gitPathsRequest
		if err := decodeGitJSON(w, r, &body); err != nil {
			return
		}
		var ok bool
		paths, ok = s.normalizeGitPaths(w, body.Paths)
		if !ok {
			return
		}
	}
	if err := s.workspace.GitStage(r.Context(), paths); err != nil {
		writeGitError(w, err)
		return
	}
	s.gitIndexMutationComplete(w)
}

func (s *Server) handleGitUnstage(w http.ResponseWriter, r *http.Request) {
	paths, ok := s.decodeGitPaths(w, r)
	if !ok {
		return
	}
	if err := s.workspace.GitUnstage(r.Context(), paths); err != nil {
		writeGitError(w, err)
		return
	}
	s.gitIndexMutationComplete(w)
}

func (s *Server) handleGitCommit(w http.ResponseWriter, r *http.Request) {
	var body gitCommitRequest
	if err := decodeGitJSON(w, r, &body); err != nil {
		return
	}
	body.Message = strings.TrimSpace(body.Message)
	if body.Message == "" {
		http.Error(w, "commit message required", http.StatusBadRequest)
		return
	}
	if len(body.Message) > 10_000 {
		http.Error(w, "commit message is too long", http.StatusBadRequest)
		return
	}
	output, err := s.workspace.GitCommit(r.Context(), body.Message)
	if err != nil {
		writeGitError(w, err)
		return
	}
	s.gitMutationComplete(w, output)
}

func (s *Server) handleGitPull(w http.ResponseWriter, r *http.Request) {
	output, err := s.workspace.GitPull(r.Context())
	if err != nil {
		writeGitError(w, err)
		return
	}
	s.flushFiles()
	s.gitMutationComplete(w, output)
}

func (s *Server) handleGitPush(w http.ResponseWriter, r *http.Request) {
	output, err := s.workspace.GitPush(r.Context())
	if err != nil {
		writeGitError(w, err)
		return
	}
	s.gitMutationComplete(w, output)
}

func (s *Server) decodeGitPaths(w http.ResponseWriter, r *http.Request) ([]string, bool) {
	var body gitPathsRequest
	if err := decodeGitJSON(w, r, &body); err != nil {
		return nil, false
	}
	return s.normalizeGitPaths(w, body.Paths)
}

func (s *Server) normalizeGitPaths(w http.ResponseWriter, requested []string) ([]string, bool) {
	if len(requested) == 0 {
		http.Error(w, "at least one path is required", http.StatusBadRequest)
		return nil, false
	}
	paths := make([]string, 0, len(requested))
	for _, path := range requested {
		rel, ok := s.workspaceRel(path)
		if !ok {
			http.Error(w, "invalid path", http.StatusBadRequest)
			return nil, false
		}
		paths = append(paths, rel)
	}
	return paths, true
}

func (s *Server) gitMutationComplete(w http.ResponseWriter, output string) {
	s.broadcast(Frame{Type: EvtDiffsChanged})
	writeJSON(w, map[string]string{"output": output})
}

func (s *Server) gitIndexMutationComplete(w http.ResponseWriter) {
	// Staging only changes the index. Give clients a narrow event so an open
	// history or branch comparison does not contend with the status refresh.
	s.broadcast(Frame{Type: EvtGitIndexChanged})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) gitMutationNoContent(w http.ResponseWriter) {
	s.broadcast(Frame{Type: EvtDiffsChanged})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) gitCheckoutComplete(w http.ResponseWriter, output string) {
	s.flushFiles()
	s.gitMutationComplete(w, output)
}

func decodeGitJSON(w http.ResponseWriter, r *http.Request, target any) error {
	return decodeJSONRequest(w, r, target, gitRequestLimit)
}

func writeGitError(w http.ResponseWriter, err error) {
	status := http.StatusConflict
	if errors.Is(err, changes.ErrNotGitRepository) {
		status = http.StatusNotFound
	}
	http.Error(w, err.Error(), status)
}
