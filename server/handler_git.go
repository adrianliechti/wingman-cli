package server

import (
	"context"
	"encoding/json"
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

func (s *Server) handleGitInit(w http.ResponseWriter, r *http.Request) {
	if err := s.workspace.GitInit(); err != nil {
		writeGitError(w, err)
		return
	}
	s.broadcast(Frame{Type: EvtCapabilitiesChanged})
	s.flushFiles()
	s.gitMutationComplete(w, "Initialized Git repository")
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
	refresh := r.URL.Query().Get("refresh") != "0"
	ctx := r.Context()
	if refresh {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
	}
	branches, warning, err := s.workspace.GitBranches(ctx, refresh)
	if err != nil {
		writeGitError(w, err)
		return
	}
	result := GitBranches{Branches: make([]GitBranch, 0, len(branches)), Warning: warning}
	for _, branch := range branches {
		result.Branches = append(result.Branches, GitBranch{
			Name: branch.Name, Remote: branch.Remote, Current: branch.Current,
		})
	}
	if refresh {
		s.broadcast(Frame{Type: EvtDiffsChanged})
	}
	writeJSON(w, result)
}

func (s *Server) handleGitCreateBranch(w http.ResponseWriter, r *http.Request) {
	var body gitBranchRequest
	if err := decodeLimitedJSON(w, r, &body); err != nil {
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
	if err := decodeLimitedJSON(w, r, &body); err != nil {
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
	paths, ok := s.decodeGitPaths(w, r)
	if !ok {
		return
	}
	if err := s.workspace.GitStage(r.Context(), paths); err != nil {
		writeGitError(w, err)
		return
	}
	s.gitMutationComplete(w, "")
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
	s.gitMutationComplete(w, "")
}

func (s *Server) handleGitCommit(w http.ResponseWriter, r *http.Request) {
	var body gitCommitRequest
	if err := decodeLimitedJSON(w, r, &body); err != nil {
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
	if err := decodeLimitedJSON(w, r, &body); err != nil {
		return nil, false
	}
	if len(body.Paths) == 0 {
		http.Error(w, "at least one path is required", http.StatusBadRequest)
		return nil, false
	}
	paths := make([]string, 0, len(body.Paths))
	for _, path := range body.Paths {
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

func (s *Server) gitCheckoutComplete(w http.ResponseWriter, output string) {
	s.flushFiles()
	s.gitMutationComplete(w, output)
}

func decodeLimitedJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return err
	}
	return nil
}

func writeGitError(w http.ResponseWriter, err error) {
	status := http.StatusConflict
	if errors.Is(err, changes.ErrNotGitRepository) {
		status = http.StatusNotFound
	}
	http.Error(w, err.Error(), status)
}
