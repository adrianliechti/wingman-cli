package acp

import (
	"fmt"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"
)

// ValidateMCPServers checks the transport union and the fields needed to hand
// an ACP-provided server to an underlying agent. Stdio is part of baseline ACP;
// optional transports must be advertised through capabilities.
func ValidateMCPServers(servers []acpsdk.McpServer, capabilities acpsdk.McpCapabilities) error {
	seen := make(map[string]bool, len(servers))
	for i, server := range servers {
		count := 0
		for _, present := range []bool{server.Stdio != nil, server.Http != nil, server.Sse != nil, server.Acp != nil} {
			if present {
				count++
			}
		}
		if count != 1 {
			return fmt.Errorf("MCP server %d must specify exactly one transport", i+1)
		}
		name := ""
		switch {
		case server.Stdio != nil:
			name = strings.TrimSpace(server.Stdio.Name)
			if strings.TrimSpace(server.Stdio.Command) == "" {
				return fmt.Errorf("MCP server %d (%s): stdio command is required", i+1, name)
			}
			for _, variable := range server.Stdio.Env {
				if strings.TrimSpace(variable.Name) == "" {
					return fmt.Errorf("MCP server %d (%s): environment variable name is required", i+1, name)
				}
			}
		case server.Http != nil:
			name = strings.TrimSpace(server.Http.Name)
			if !capabilities.Http {
				return fmt.Errorf("MCP server %d (%s): HTTP transport is not supported", i+1, name)
			}
			if strings.TrimSpace(server.Http.Url) == "" {
				return fmt.Errorf("MCP server %d (%s): HTTP URL is required", i+1, name)
			}
		case server.Sse != nil:
			name = strings.TrimSpace(server.Sse.Name)
			if !capabilities.Sse {
				return fmt.Errorf("MCP server %d (%s): SSE transport is not supported", i+1, name)
			}
			if strings.TrimSpace(server.Sse.Url) == "" {
				return fmt.Errorf("MCP server %d (%s): SSE URL is required", i+1, name)
			}
		case server.Acp != nil:
			name = strings.TrimSpace(server.Acp.Name)
			if !capabilities.Acp {
				return fmt.Errorf("MCP server %d (%s): ACP transport is not supported", i+1, name)
			}
		}
		if name == "" {
			return fmt.Errorf("MCP server %d has no name", i+1)
		}
		if seen[name] {
			return fmt.Errorf("duplicate MCP server name %q", name)
		}
		seen[name] = true
	}
	return nil
}

// StableMCPServers converts the unstable session/fork transport union to the
// stable union used by new/resume and validates it against the agent's
// advertised transport capabilities.
func StableMCPServers(servers []acpsdk.UnstableMcpServer, capabilities acpsdk.McpCapabilities) ([]acpsdk.McpServer, error) {
	out := make([]acpsdk.McpServer, 0, len(servers))
	for _, server := range servers {
		converted := acpsdk.McpServer{Stdio: server.Stdio}
		if server.Http != nil {
			converted.Http = &acpsdk.McpServerHttpInline{
				Meta: server.Http.Meta, Headers: server.Http.Headers,
				Name: server.Http.Name, Type: server.Http.Type, Url: server.Http.Url,
			}
		}
		if server.Sse != nil {
			converted.Sse = &acpsdk.McpServerSseInline{
				Meta: server.Sse.Meta, Headers: server.Sse.Headers,
				Name: server.Sse.Name, Type: server.Sse.Type, Url: server.Sse.Url,
			}
		}
		if server.Acp != nil {
			converted.Acp = &acpsdk.McpServerAcpInline{
				Meta: server.Acp.Meta, Id: acpsdk.McpServerAcpId(server.Acp.Id),
				Name: server.Acp.Name, Type: server.Acp.Type,
			}
		}
		out = append(out, converted)
	}
	if err := ValidateMCPServers(out, capabilities); err != nil {
		return nil, err
	}
	return out, nil
}

// UnstableConfigOptions maps the stable config-option union to the duplicate
// unstable union used by ACP's session/fork response.
func UnstableConfigOptions(options []acpsdk.SessionConfigOption) []acpsdk.UnstableSessionConfigOption {
	out := make([]acpsdk.UnstableSessionConfigOption, 0, len(options))
	for _, option := range options {
		converted := acpsdk.UnstableSessionConfigOption{}
		if option.Select != nil {
			selectOption := option.Select
			converted.Select = &acpsdk.UnstableSessionConfigOptionSelect{
				Meta: selectOption.Meta, Category: selectOption.Category,
				CurrentValue: selectOption.CurrentValue, Description: selectOption.Description,
				Id: selectOption.Id, Name: selectOption.Name, Options: selectOption.Options, Type: selectOption.Type,
			}
		}
		if option.Boolean != nil {
			booleanOption := option.Boolean
			converted.Boolean = &acpsdk.UnstableSessionConfigOptionBoolean{
				Meta: booleanOption.Meta, Category: booleanOption.Category,
				CurrentValue: booleanOption.CurrentValue, Description: booleanOption.Description,
				Id: booleanOption.Id, Name: booleanOption.Name, Type: booleanOption.Type,
			}
		}
		out = append(out, converted)
	}
	return out
}
