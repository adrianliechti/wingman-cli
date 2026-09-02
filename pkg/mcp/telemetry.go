package mcp

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"

	sdkjsonrpc "github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/adrianliechti/wingman-agent/pkg/telemetry"
)

func mcpClientTelemetryMiddleware(tel *telemetry.Telemetry, transport telemetry.MCPTransport) sdkmcp.Middleware {
	return func(next sdkmcp.MethodHandler) sdkmcp.MethodHandler {
		return func(ctx context.Context, method string, request sdkmcp.Request) (sdkmcp.Result, error) {
			operationRequest := mcpOperationRequest(method, request, transport)
			ctx, operation := tel.StartMCPClient(ctx, operationRequest)
			if params := request.GetParams(); params != nil {
				params.SetMeta(tel.InjectMCPContext(ctx, params.GetMeta()))
			}

			result, err := next(ctx, method, request)
			operation.End(mcpOperationResult(result, err))
			return result, err
		}
	}
}

func mcpServerTelemetryMiddleware(tel *telemetry.Telemetry, transport telemetry.MCPTransport) sdkmcp.Middleware {
	return func(next sdkmcp.MethodHandler) sdkmcp.MethodHandler {
		return func(ctx context.Context, method string, request sdkmcp.Request) (sdkmcp.Result, error) {
			operationRequest := mcpOperationRequest(method, request, transport)
			if params := request.GetParams(); params != nil {
				ctx = tel.ExtractMCPContext(ctx, params.GetMeta())
			}
			ctx, operation := tel.StartMCPServer(ctx, operationRequest)
			result, err := next(ctx, method, request)
			operation.End(mcpOperationResult(result, err))
			return result, err
		}
	}
}

func mcpOperationRequest(method string, request sdkmcp.Request, transport telemetry.MCPTransport) telemetry.MCPClientRequest {
	result := telemetry.MCPClientRequest{
		MCPTransport: transport,
		Method:       method,
	}
	if session, ok := request.GetSession().(*sdkmcp.ClientSession); ok {
		result.SessionID = session.ID()
		if initialized := session.InitializeResult(); initialized != nil {
			result.ProtocolVersion = initialized.ProtocolVersion
		}
	}

	params := request.GetParams()
	if params == nil {
		return result
	}
	if result.ProtocolVersion == "" {
		if version, ok := params.GetMeta()[sdkmcp.MetaKeyProtocolVersion].(string); ok {
			result.ProtocolVersion = version
		}
	}

	switch params := params.(type) {
	case *sdkmcp.InitializeParams:
		if params != nil {
			result.ProtocolVersion = params.ProtocolVersion
		}
	case *sdkmcp.CallToolParams:
		if params != nil {
			result.ToolName = params.Name
			result.Arguments = params.Arguments
		}
	case *sdkmcp.GetPromptParams:
		if params != nil {
			result.PromptName = params.Name
			result.PromptVariables = params.Arguments
		}
	case *sdkmcp.ReadResourceParams:
		if params != nil {
			result.ResourceURI = params.URI
		}
	case *sdkmcp.SubscribeParams:
		if params != nil {
			result.ResourceURI = params.URI
		}
	case *sdkmcp.UnsubscribeParams:
		if params != nil {
			result.ResourceURI = params.URI
		}
	case *sdkmcp.ResourceUpdatedNotificationParams:
		if params != nil {
			result.ResourceURI = params.URI
		}
	case *sdkmcp.CompleteParams:
		if params != nil && params.Ref != nil {
			switch params.Ref.Type {
			case "ref/prompt":
				result.PromptName = params.Ref.Name
			case "ref/resource":
				result.ResourceURI = params.Ref.URI
			}
		}
	}
	return result
}

func mcpOperationResult(result sdkmcp.Result, err error) telemetry.MCPClientResult {
	observed := telemetry.MCPClientResult{Outcome: telemetry.Outcome{Err: err}}
	if initialized, ok := result.(*sdkmcp.InitializeResult); ok && initialized != nil {
		observed.ProtocolVersion = initialized.ProtocolVersion
	}
	if err != nil {
		var rpcErr *sdkjsonrpc.Error
		if errors.As(err, &rpcErr) {
			observed.StatusCode = strconv.FormatInt(rpcErr.Code, 10)
			observed.ErrorType = observed.StatusCode
			observed.StatusDescription = rpcErr.Message
		}
		return observed
	}

	if toolResult, ok := result.(*sdkmcp.CallToolResult); ok && toolResult != nil {
		observed.ToolError = toolResult.IsError
		if !toolResult.IsError {
			if toolResult.StructuredContent != nil {
				observed.Result = toolResult.StructuredContent
			} else if len(toolResult.Content) > 0 {
				observed.Result = map[string]any{"content": toolResult.Content}
			}
		}
	}
	return observed
}

func mcpSessionRequest(session *sdkmcp.ClientSession, server ServerConfig) telemetry.MCPSessionRequest {
	request := telemetry.MCPSessionRequest{MCPTransport: mcpTransport(server)}
	if session != nil {
		if initialized := session.InitializeResult(); initialized != nil {
			request.ProtocolVersion = initialized.ProtocolVersion
		}
	}
	return request
}

func mcpTransport(server ServerConfig) telemetry.MCPTransport {
	if server.Command != "" {
		return telemetry.MCPTransport{NetworkTransport: "pipe"}
	}
	parsed, err := url.Parse(server.URL)
	if err != nil || parsed.Hostname() == "" {
		return telemetry.MCPTransport{}
	}

	transport := telemetry.MCPTransport{
		NetworkTransport: "tcp",
		ServerAddress:    parsed.Hostname(),
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		transport.NetworkProtocolName = "http"
	case "ws", "wss":
		transport.NetworkProtocolName = "websocket"
	}
	if port := parsed.Port(); port != "" {
		transport.ServerPort, _ = strconv.Atoi(port)
	} else {
		switch strings.ToLower(parsed.Scheme) {
		case "http", "ws":
			transport.ServerPort = 80
		case "https", "wss":
			transport.ServerPort = 443
		}
	}
	return transport
}
