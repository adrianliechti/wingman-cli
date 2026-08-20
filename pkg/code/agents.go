package code

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/layout"
)

const BuiltinAgentName = "wingman"

type AgentDef struct {
	Name string `json:"name"`

	Command string `json:"command"`

	Args []string `json:"args,omitempty"`

	Env map[string]string `json:"env,omitempty"`
}

func agentsConfigPath() (string, error) {
	return layout.WingmanPath("agents.json")
}

func HasAgentsConfig() bool {
	path, err := agentsConfigPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

func LoadAgents() ([]AgentDef, error) {
	path, err := agentsConfigPath()
	if err != nil {
		return nil, fmt.Errorf("resolve agents config path: %w", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read agents config %s: %w", path, err)
	}

	var defs []AgentDef
	if err := json.Unmarshal(data, &defs); err != nil {
		return nil, fmt.Errorf("parse agents config %s: %w", path, err)
	}

	out := make([]AgentDef, 0, len(defs))
	for i, d := range defs {
		d.Name = strings.TrimSpace(d.Name)
		d.Command = strings.TrimSpace(d.Command)
		if d.Name == "" {
			return nil, fmt.Errorf("agents config %s: entry %d has no name", path, i+1)
		}
		if d.Command == "" {
			return nil, fmt.Errorf("agents config %s: entry %d (%s) has no command", path, i+1, d.Name)
		}
		if strings.EqualFold(d.Name, BuiltinAgentName) {
			continue
		}
		out = append(out, d)
	}
	return out, nil
}
