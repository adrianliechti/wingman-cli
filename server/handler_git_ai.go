package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/changes"
)

const (
	gitCommitMessageMaxPrompt = 96 << 10
	gitCommitMessageMaxOutput = 10 << 10
	gitCommitHistoryLimit     = 12
	gitCommitHistoryMaxBytes  = 8 << 10
	gitCommitSubjectMaxBytes  = 512
)

const gitCommitMessageInstructions = `Write a Git commit message for exactly the staged changes in the prompt.
Match the repository's recent subject style when examples are provided. Use a concise imperative subject that says what the change accomplishes. Add a short body only when it materially explains motivation or important behavior. Return only the commit message without Markdown, commentary, quotes, or a diff.`

var gitCommitMessageOutputSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"message": map[string]any{
			"type":        "string",
			"description": "A complete Git commit message with an optional body.",
		},
	},
	"required":             []string{"message"},
	"additionalProperties": false,
}

type gitCommitMessageCompletion struct {
	Message string `json:"message"`
}

type gitCommitMessageResponse struct {
	Message string `json:"message"`
}

type gitCommitMessageService struct {
	complete func(context.Context, string) (gitCommitMessageCompletion, error)
}

func newGitCommitMessageService(cfg *agent.Config) *gitCommitMessageService {
	service := &gitCommitMessageService{}
	service.complete = func(ctx context.Context, prompt string) (gitCommitMessageCompletion, error) {
		modelID, effort := resolveGenerationTarget(cfg, "utility", "")
		result, err := cfg.Generate(ctx, agent.GenerateOptions{
			Model:           modelID,
			Effort:          effort,
			Instructions:    gitCommitMessageInstructions,
			Input:           prompt,
			OutputSchema:    gitCommitMessageOutputSchema,
			MaxOutputTokens: 2_048,
		})
		if err != nil {
			return gitCommitMessageCompletion{}, err
		}
		var output gitCommitMessageCompletion
		if err := json.Unmarshal([]byte(result.Text), &output); err != nil {
			return gitCommitMessageCompletion{}, fmt.Errorf("decode generated commit message: %w", err)
		}
		return output, nil
	}
	return service
}

func (s *gitCommitMessageService) generate(ctx context.Context, diffs []changes.FileDiff, history []changes.GitCommit) (string, error) {
	prompt, err := buildGitCommitMessagePrompt(diffs, history)
	if err != nil {
		return "", err
	}
	completion, err := s.complete(ctx, prompt)
	if err != nil {
		return "", err
	}
	message := strings.TrimSpace(completion.Message)
	if message == "" || len(message) > gitCommitMessageMaxOutput || !utf8.ValidString(message) || strings.ContainsRune(message, 0) {
		return "", errors.New("generated commit message is empty or invalid")
	}
	return message, nil
}

func buildGitCommitMessagePrompt(diffs []changes.FileDiff, history []changes.GitCommit) (string, error) {
	if len(diffs) == 0 {
		return "", errors.New("there are no staged changes")
	}
	var out strings.Builder
	out.WriteString("<RECENT_SUBJECTS>\n")
	written := 0
	for _, commit := range history {
		summary := strings.Join(strings.Fields(commit.Summary), " ")
		if summary == "" {
			continue
		}
		summary = validUTF8Prefix(summary, gitCommitSubjectMaxBytes)
		line := "- " + summary + "\n"
		if out.Len()+len(line) > gitCommitHistoryMaxBytes {
			break
		}
		out.WriteString(line)
		written++
		if written == gitCommitHistoryLimit {
			break
		}
	}
	if written == 0 {
		out.WriteString("(none)\n")
	}
	out.WriteString("</RECENT_SUBJECTS>\n\n<STAGED_CHANGES>\n")

	const stagedChangesEnd = "\n</STAGED_CHANGES>"
	for _, file := range diffs {
		header := fmt.Sprintf("\n--- %s", file.Path)
		if file.OriginalPath != "" && file.OriginalPath != file.Path {
			header += " (from " + file.OriginalPath + ")"
		}
		header += " ---\n"
		if out.Len()+len(header) > gitCommitMessageMaxPrompt-len(stagedChangesEnd) {
			break
		}
		out.WriteString(header)
		remaining := gitCommitMessageMaxPrompt - out.Len() - len(stagedChangesEnd)
		patch := file.Patch
		if len(patch) > remaining {
			const truncated = "\n[diff truncated]\n"
			if remaining <= len(truncated) {
				patch = validUTF8Prefix(patch, remaining)
			} else {
				patch = validUTF8Prefix(patch, remaining-len(truncated)) + truncated
			}
		}
		out.WriteString(patch)
		if len(patch) < len(file.Patch) {
			break
		}
	}
	out.WriteString(stagedChangesEnd)
	return out.String(), nil
}

func validUTF8Prefix(value string, limit int) string {
	limit = min(max(limit, 0), len(value))
	for limit > 0 && limit < len(value) && !utf8.RuneStart(value[limit]) {
		limit--
	}
	return value[:limit]
}

func gitDiffSnapshot(diffs []changes.FileDiff) string {
	hash := sha256.New()
	for _, diff := range diffs {
		_, _ = hash.Write([]byte(diff.Path))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(diff.OriginalPath))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(diff.Patch))
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func (s *Server) handleGitCommitMessage(w http.ResponseWriter, r *http.Request) {
	if s.commitMessages == nil {
		http.Error(w, "commit-message generation is not configured", http.StatusNotFound)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	status, err := s.workspace.GitStatus(ctx)
	if err != nil {
		writeGitError(w, err)
		return
	}
	for _, file := range status.Files {
		if file.Conflict {
			http.Error(w, "resolve staged conflicts before generating a commit message", http.StatusConflict)
			return
		}
	}
	diffs, err := s.workspace.DiffsLayer(ctx, changes.DiffStaged)
	if err != nil {
		writeGitError(w, err)
		return
	}
	if len(diffs) == 0 {
		http.Error(w, "there are no staged changes", http.StatusConflict)
		return
	}
	snapshot := gitDiffSnapshot(diffs)
	history, historyErr := s.workspace.GitHistoryLimit(ctx, gitCommitHistoryLimit)
	if historyErr != nil {
		history = nil
	}
	message, err := s.commitMessages.generate(ctx, diffs, history)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			http.Error(w, "commit-message generation timed out", http.StatusGatewayTimeout)
			return
		}
		http.Error(w, "commit-message generation unavailable", http.StatusBadGateway)
		return
	}
	currentStatus, err := s.workspace.GitStatus(ctx)
	if err != nil {
		writeGitError(w, err)
		return
	}
	for _, file := range currentStatus.Files {
		if file.Conflict {
			http.Error(w, "staged changes changed while the commit message was being generated", http.StatusConflict)
			return
		}
	}
	current, err := s.workspace.DiffsLayer(ctx, changes.DiffStaged)
	if err != nil || gitDiffSnapshot(current) != snapshot {
		http.Error(w, "staged changes changed while the commit message was being generated", http.StatusConflict)
		return
	}
	writeJSON(w, gitCommitMessageResponse{Message: message})
}
