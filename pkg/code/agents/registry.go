package agents

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/adrianliechti/wingman-agent/pkg/acp/claude"
	"github.com/adrianliechti/wingman-agent/pkg/acp/codex"
	"github.com/adrianliechti/wingman-agent/pkg/acp/pi"
	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/code"
	codeacp "github.com/adrianliechti/wingman-agent/pkg/code/acp"
	codeagent "github.com/adrianliechti/wingman-agent/pkg/code/agent"
	"github.com/adrianliechti/wingman-agent/pkg/external"
	claudecli "github.com/adrianliechti/wingman-agent/pkg/external/claude"
	codexcli "github.com/adrianliechti/wingman-agent/pkg/external/codex"
	picli "github.com/adrianliechti/wingman-agent/pkg/external/pi"
)

type Constructor func(context.Context, *code.Workspace) (code.Agent, error)

type Registration struct {
	ID          string
	Name        string
	Constructor Constructor
}

func ID(name string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(name)), " ", "-")
}

func nativeClaudeOptions(root string, env []string) claude.Options {
	return claude.Options{Cwd: root, Env: env}
}

func nativeCodexOptions(root string, env []string) codex.Options {
	return codex.Options{Dir: root, Env: env}
}

func nativePiOptions(root string, env []string) pi.Options {
	return pi.Options{
		Path:        picli.BinPath(),
		Dir:         root,
		Env:         env,
		SessionsDir: picli.NativeSessionsDir(),
	}
}

type inProcessAgent interface {
	acpsdk.Agent
	SetAgentConnection(*acpsdk.AgentSideConnection)
	Close() error
}

func wrapInProcess(ws *code.Workspace, name string, srv inProcessAgent) (code.Agent, error) {
	a, err := codeacp.NewInProcess(ws, name, srv, srv.SetAgentConnection, srv.Close)
	if err != nil {
		_ = srv.Close()
		return nil, err
	}
	return a, nil
}

func detected() []Registration {
	var out []Registration

	if _, err := exec.LookPath(claudecli.BinPath()); err == nil {
		out = append(out, Registration{
			ID: "claude", Name: "Claude",
			Constructor: func(_ context.Context, ws *code.Workspace) (code.Agent, error) {
				srv := claude.New(nativeClaudeOptions(ws.RootPath, os.Environ()))
				return wrapInProcess(ws, "claude", srv)
			},
		})
	}

	if _, err := exec.LookPath(codexcli.BinPath()); err == nil {
		out = append(out, Registration{
			ID: "codex", Name: "Codex",
			Constructor: func(ctx context.Context, ws *code.Workspace) (code.Agent, error) {
				srv, err := codex.Spawn(ctx, nativeCodexOptions(ws.RootPath, os.Environ()))
				if err != nil {
					return nil, fmt.Errorf("codex spawn: %w", err)
				}
				return wrapInProcess(ws, "codex", srv)
			},
		})
	}

	for _, def := range []code.AgentDef{
		{Name: "copilot", Command: external.LookupPath("copilot", "copilot"), Args: []string{"--acp", "--stdio"}},
		{Name: "opencode", Command: external.LookupPath("opencode", "opencode"), Args: []string{"acp"}},
	} {
		path, err := exec.LookPath(def.Command)
		if err != nil {
			continue
		}
		def.Command = path
		name := def.Name
		out = append(out, Registration{
			ID: ID(name), Name: name,
			Constructor: func(_ context.Context, ws *code.Workspace) (code.Agent, error) {
				return codeacp.New(ws, def)
			},
		})
	}

	if _, err := exec.LookPath(picli.BinPath()); err == nil {
		out = append(out, Registration{
			ID: "pi", Name: "Pi",
			Constructor: func(_ context.Context, ws *code.Workspace) (code.Agent, error) {
				srv := pi.New(nativePiOptions(ws.RootPath, os.Environ()))
				return wrapInProcess(ws, "pi", srv)
			},
		})
	}

	return out
}

func configured() []Registration {
	defs := code.LoadAgents()
	out := make([]Registration, 0, len(defs))
	for _, def := range defs {
		id := ID(def.Name)
		if id == code.BuiltinAgentName {
			continue
		}
		name := def.Name
		def.Name = id
		out = append(out, Registration{
			ID: id, Name: name,
			Constructor: func(_ context.Context, ws *code.Workspace) (code.Agent, error) {
				return codeacp.New(ws, def)
			},
		})
	}
	return out
}

// Available merges automatically detected agents with agents.json entries.
// An explicitly configured entry replaces a detected entry with the same ID.
func Available() []Registration {
	out := detected()
	index := make(map[string]int, len(out))
	for i, r := range out {
		index[r.ID] = i
	}
	for _, r := range configured() {
		if i, ok := index[r.ID]; ok {
			out[i] = r
			continue
		}
		index[r.ID] = len(out)
		out = append(out, r)
	}
	return out
}

func names(registrations []Registration) []string {
	names := []string{code.BuiltinAgentName}
	for _, r := range registrations {
		names = append(names, r.ID)
	}
	return names
}

func Names() []string { return names(Available()) }

// New constructs the built-in agent or any detected/configured ACP agent.
// builtinConfig lets long-lived hosts reuse their existing Wingman config.
func New(ctx context.Context, ws *code.Workspace, name string, builtinConfig *agent.Config) (code.Agent, error) {
	name = ID(name)
	if name == "" || name == code.BuiltinAgentName {
		if builtinConfig == nil {
			var err error
			builtinConfig, err = agent.DefaultConfig()
			if err != nil {
				return nil, err
			}
		}
		return codeagent.New(ws, builtinConfig, nil), nil
	}

	available := Available()
	for _, r := range available {
		if r.ID == name {
			return r.Constructor(ctx, ws)
		}
	}
	return nil, fmt.Errorf("unknown agent %q (available: %s)", name, strings.Join(names(available), ", "))
}
