package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"

	"github.com/coder/acp-go-sdk"
)

func (a *Agent) fetchModels(ctx context.Context) ([]ModelEntry, []acp.AvailableCommand, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, a.path,
		"--output-format", "stream-json",
		"--input-format", "stream-json",
		"--verbose",
	)
	cmd.Dir = a.defaultCwd
	if a.env != nil {
		cmd.Env = a.env
	}
	cmd.Stderr = a.stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, nil, fmt.Errorf("stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, nil, fmt.Errorf("start claude: %w", err)
	}

	defer func() {
		_ = stdin.Close()
		_ = cmd.Wait()
	}()

	const reqID = "wingman-init"
	req := initControlRequest{Type: "control_request", RequestID: reqID}
	req.Request.Subtype = "initialize"
	b, err := json.Marshal(req)
	if err != nil {
		return nil, nil, err
	}
	if _, err := stdin.Write(append(b, '\n')); err != nil {
		return nil, nil, fmt.Errorf("write initialize: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var resp initControlResponse
		if json.Unmarshal(line, &resp) != nil {
			continue
		}
		if resp.Type != "control_response" || resp.Response.RequestID != reqID {
			continue
		}
		if resp.Response.Subtype != "success" {
			return nil, nil, fmt.Errorf("claude initialize failed: %s", resp.Response.Subtype)
		}
		return modelsFromCLI(resp.Response.Response.Models), commandsFromCLI(resp.Response.Response.Commands), nil
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, fmt.Errorf("read claude init: %w", err)
	}
	return nil, nil, fmt.Errorf("claude closed without an initialize response")
}

func modelsFromCLI(list []cliModel) []ModelEntry {
	out := make([]ModelEntry, 0, len(list))
	for _, m := range list {
		name := m.DisplayName
		if name == "" {
			name = m.Value
		}
		var efforts []string
		if m.SupportsEffort {
			efforts = m.SupportedEffortLevels
		}
		out = append(out, ModelEntry{
			ID:            m.Value,
			Name:          name,
			Description:   m.Description,
			ResolvedModel: m.ResolvedModel,
			EffortLevels:  efforts,
		})
	}
	return out
}

var hiddenCommands = map[string]bool{
	"clear": true, "cost": true, "keybindings-help": true, "login": true,
	"logout": true, "output-style:new": true, "release-notes": true, "todos": true,
}

func commandsFromCLI(list []cliCommand) []acp.AvailableCommand {
	out := make([]acp.AvailableCommand, 0, len(list))
	for _, c := range list {
		if c.Name == "" || hiddenCommands[c.Name] {
			continue
		}
		cmd := acp.AvailableCommand{Name: c.Name, Description: c.Description}
		if c.ArgumentHint != "" {
			cmd.Input = &acp.AvailableCommandInput{Unstructured: &acp.UnstructuredCommandInput{Hint: c.ArgumentHint}}
		}
		out = append(out, cmd)
	}
	return out
}

type initControlRequest struct {
	Type      string `json:"type"`
	RequestID string `json:"request_id"`
	Request   struct {
		Subtype string `json:"subtype"`
	} `json:"request"`
}

type initControlResponse struct {
	Type     string `json:"type"`
	Response struct {
		Subtype   string `json:"subtype"`
		RequestID string `json:"request_id"`
		Response  struct {
			Models   []cliModel   `json:"models"`
			Commands []cliCommand `json:"commands"`
		} `json:"response"`
	} `json:"response"`
}

type cliCommand struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	ArgumentHint string `json:"argumentHint"`
}

type cliModel struct {
	Value                 string   `json:"value"`
	DisplayName           string   `json:"displayName"`
	Description           string   `json:"description"`
	ResolvedModel         string   `json:"resolvedModel"`
	SupportsEffort        bool     `json:"supportsEffort"`
	SupportedEffortLevels []string `json:"supportedEffortLevels"`
}
