package memory

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	instructionsFile = "AGENTS.md"
	soulFile         = "SOUL.md"
	maxContentBytes  = 25 * 1024 // 25KB per file
)

// Store manages per-agent and global AGENTS.md instructions for claw.
//
// Directory layout under ~/.wingman/claw/agents/:
//
//	{dir}/
//	  global/
//	    AGENTS.md              -- shared instructions across all agents
//	  {agent}/
//	    AGENTS.md              -- agent-specific instructions
//	    workspace/             -- agent's working directory (files, data)
//	    tasks/                 -- agent's scheduled tasks
type Store struct {
	dir string
}

func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "global"), 0755); err != nil {
		return nil, err
	}

	return &Store{dir: dir}, nil
}

func (s *Store) Dir() string { return s.dir }

// GlobalDir returns the path to the global memory directory.
func (s *Store) GlobalDir() string {
	return filepath.Join(s.dir, "global")
}

// AgentDir returns the path to an agent's root directory.
func (s *Store) AgentDir(name string) string {
	return filepath.Join(s.dir, name)
}

// WorkspaceDir returns the path to an agent's workspace directory.
func (s *Store) WorkspaceDir(name string) string {
	return filepath.Join(s.dir, name, "workspace")
}

// TasksDir returns the path to an agent's tasks directory.
func (s *Store) TasksDir(name string) string {
	return filepath.Join(s.dir, name, "tasks")
}

// EnsureAgent creates the full directory structure for an agent.
// Creates a default SOUL.md if one doesn't exist.
func (s *Store) EnsureAgent(name string) error {
	for _, sub := range []string{"", "workspace"} {
		if err := os.MkdirAll(filepath.Join(s.AgentDir(name), sub), 0755); err != nil {
			return err
		}
	}

	// Create default SOUL.md if missing
	soulPath := filepath.Join(s.AgentDir(name), soulFile)

	if _, err := os.Stat(soulPath); os.IsNotExist(err) {
		os.WriteFile(soulPath, []byte(defaultSoul), 0644)
	}

	return nil
}

const defaultSoul = `I solve problems by doing, not by describing what I would do.
I keep responses short unless depth is asked for.
I say what I know, flag what I don't, and never fake confidence.
I treat the user's time as the scarcest resource, and their trust as the most valuable.
`

// RemoveAgent deletes an agent's entire directory.
func (s *Store) RemoveAgent(name string) error {
	return os.RemoveAll(s.AgentDir(name))
}

// ListAgents returns names of all registered agents (directories
// under the store, excluding "global").
func (s *Store) ListAgents() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() && e.Name() != "global" {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

// AgentExists checks if an agent directory exists.
func (s *Store) AgentExists(name string) bool {
	info, err := os.Stat(s.AgentDir(name))
	return err == nil && info.IsDir()
}

// GlobalContent reads the global AGENTS.md.
func (s *Store) GlobalContent() string {
	return readFileTruncated(filepath.Join(s.GlobalDir(), instructionsFile))
}

// AgentContent reads an agent's AGENTS.md from its workspace.
func (s *Store) AgentContent(name string) string {
	return readFileTruncated(filepath.Join(s.WorkspaceDir(name), instructionsFile))
}

// Content returns the combined instructions for an agent:
// global instructions first, then agent-specific.
func (s *Store) Content(name string) string {
	global := s.GlobalContent()
	local := s.AgentContent(name)

	if global == "" {
		return local
	}

	if local == "" {
		return global
	}

	return global + "\n\n---\n\n" + local
}

// WriteGlobal writes content to the global AGENTS.md.
func (s *Store) WriteGlobal(content string) error {
	return os.WriteFile(filepath.Join(s.GlobalDir(), instructionsFile), []byte(content), 0644)
}

// SoulContent reads an agent's SOUL.md (outside workspace, not writable by agent).
func (s *Store) SoulContent(name string) string {
	return readFileTruncated(filepath.Join(s.AgentDir(name), soulFile))
}

// WriteAgent writes content to an agent's AGENTS.md in its workspace.
func (s *Store) WriteAgent(name string, content string) error {
	return os.WriteFile(filepath.Join(s.WorkspaceDir(name), instructionsFile), []byte(content), 0644)
}

func readFileTruncated(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	content := strings.TrimSpace(string(data))
	if content == "" {
		return ""
	}

	if len(content) > maxContentBytes {
		truncated := content[:maxContentBytes]
		if idx := strings.LastIndex(truncated, "\n"); idx > 0 {
			truncated = truncated[:idx]
		}

		content = truncated + "\n\n> WARNING: File exceeded 25KB and was truncated."
	}

	return content
}
