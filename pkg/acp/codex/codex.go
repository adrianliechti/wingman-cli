package codex

import (
	"context"
	"encoding/json"
	"sync"
)

type notificationHandler func(threadID, method string, params json.RawMessage)

type codexClient struct {
	rpc *rpcClient

	mu                   sync.Mutex
	handlers             map[string]*threadHandlers
	onGlobalNotification notificationHandler
}

type threadHandlers struct {
	onNotification        func(method string, params json.RawMessage)
	onExecApproval        func(params execApprovalParams) execApprovalResponse
	onFileApproval        func(params fileApprovalParams) fileApprovalResponse
	onPermissionsApproval func(params permissionsApprovalParams) permissionsApprovalResponse
	onElicitation         func(params elicitationParams) elicitationResponse
}

func newCodexClient(rpc *rpcClient) *codexClient {
	c := &codexClient{rpc: rpc, handlers: make(map[string]*threadHandlers)}
	rpc.onNotification = c.dispatchNotification
	rpc.onRequest = c.dispatchRequest
	return c
}

func (c *codexClient) setThreadHandlers(threadID string, h *threadHandlers) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if h == nil {
		delete(c.handlers, threadID)
	} else {
		c.handlers[threadID] = h
	}
}

func (c *codexClient) setGlobalNotificationHandler(h notificationHandler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onGlobalNotification = h
}

func (c *codexClient) notificationHandlers(threadID string) (notificationHandler, *threadHandlers) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.onGlobalNotification, c.handlers[threadID]
}

func (c *codexClient) handlersFor(threadID string) *threadHandlers {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.handlers[threadID]
}

func (c *codexClient) dispatchNotification(method string, params json.RawMessage) {
	var probe struct {
		ThreadID string `json:"threadId"`
	}
	_ = json.Unmarshal(params, &probe)

	// Account- and process-scoped notifications carry no threadId; only per-thread handlers need one.
	global, local := c.notificationHandlers(probe.ThreadID)
	if global != nil {
		global(probe.ThreadID, method, params)
	}
	if probe.ThreadID != "" && local != nil && local.onNotification != nil {
		local.onNotification(method, params)
	}
}

func (c *codexClient) dispatchRequest(_ context.Context, method string, params json.RawMessage) (any, *rpcError) {
	switch method {
	case "item/commandExecution/requestApproval":
		var p execApprovalParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}
		}
		if h := c.handlersFor(p.ThreadID); h != nil && h.onExecApproval != nil {
			return h.onExecApproval(p), nil
		}
		return execApprovalResponse{Decision: "cancel"}, nil
	case "item/fileChange/requestApproval":
		var p fileApprovalParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}
		}
		if h := c.handlersFor(p.ThreadID); h != nil && h.onFileApproval != nil {
			return h.onFileApproval(p), nil
		}
		return fileApprovalResponse{Decision: "cancel"}, nil
	case "item/permissions/requestApproval":
		var p permissionsApprovalParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}
		}
		if h := c.handlersFor(p.ThreadID); h != nil && h.onPermissionsApproval != nil {
			return h.onPermissionsApproval(p), nil
		}
		return rejectPermissionsResponse(), nil
	case "mcpServer/elicitation/request":
		var p elicitationParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: err.Error()}
		}
		if h := c.handlersFor(p.ThreadID); h != nil && h.onElicitation != nil {
			return h.onElicitation(p), nil
		}
		return elicitationResponse{Action: "decline"}, nil
	}
	return nil, &rpcError{Code: -32601, Message: "method not found: " + method}
}

type clientInfo struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version"`
}

type initializeParams struct {
	ClientInfo   clientInfo `json:"clientInfo"`
	Capabilities any        `json:"capabilities"`
}

// experimentalApi gates thread/settings/update, collaborationMode/list and experimental notifications.
type clientCapabilities struct {
	ExperimentalAPI    bool `json:"experimentalApi"`
	RequestAttestation bool `json:"requestAttestation"`
}

type threadStartParams struct {
	Cwd            string         `json:"cwd,omitempty"`
	Model          string         `json:"model,omitempty"`
	ApprovalPolicy any            `json:"approvalPolicy,omitempty"`
	Config         map[string]any `json:"config,omitempty"`
}

type threadStartResponse struct {
	Thread          threadInfo `json:"thread"`
	Model           string     `json:"model"`
	ReasoningEffort *string    `json:"reasoningEffort"`
}

type threadInfo struct {
	ID    string    `json:"id"`
	Cwd   string    `json:"cwd"`
	Path  *string   `json:"path,omitempty"`
	Turns []rawTurn `json:"turns,omitempty"`
}

type rawTurn struct {
	ID    string            `json:"id"`
	Items []json.RawMessage `json:"items"`
}

type threadResumeParams struct {
	ThreadID      string         `json:"threadId"`
	Cwd           string         `json:"cwd,omitempty"`
	Model         string         `json:"model,omitempty"`
	ModelProvider string         `json:"modelProvider,omitempty"`
	Config        map[string]any `json:"config,omitempty"`
}

type threadResumeResponse struct {
	Thread          threadInfo `json:"thread"`
	Model           string     `json:"model"`
	ReasoningEffort *string    `json:"reasoningEffort"`
}

type threadForkParams struct {
	ThreadID      string         `json:"threadId"`
	Cwd           string         `json:"cwd,omitempty"`
	ModelProvider string         `json:"modelProvider,omitempty"`
	Config        map[string]any `json:"config,omitempty"`
}

type threadForkResponse struct {
	Thread          threadInfo `json:"thread"`
	Model           string     `json:"model"`
	ReasoningEffort *string    `json:"reasoningEffort"`
}

type threadReadParams struct {
	ThreadID     string `json:"threadId"`
	IncludeTurns bool   `json:"includeTurns,omitempty"`
}

type threadReadResponse struct {
	Thread threadInfo `json:"thread"`
}

type threadUnsubscribeParams struct {
	ThreadID string `json:"threadId"`
}

type threadSetNameParams struct {
	ThreadID string `json:"threadId"`
	Name     string `json:"name"`
}

type threadSettingsUpdateParams struct {
	ThreadID          string                 `json:"threadId"`
	CollaborationMode codexCollaborationMode `json:"collaborationMode"`
}

type codexCollaborationMode struct {
	Mode     string                    `json:"mode"`
	Settings collaborationModeSettings `json:"settings"`
}

type collaborationModeSettings struct {
	Model                 string  `json:"model"`
	ReasoningEffort       *string `json:"reasoning_effort"`
	DeveloperInstructions *string `json:"developer_instructions"`
}

type configReadParams struct {
	IncludeLayers bool `json:"includeLayers"`
}

type configReadResponse struct {
	Config map[string]any `json:"config"`
}

type threadListParams struct {
	Cursor         *string  `json:"cursor,omitempty"`
	Limit          *int     `json:"limit,omitempty"`
	Cwd            any      `json:"cwd,omitempty"`
	SourceKinds    []string `json:"sourceKinds,omitempty"`
	ModelProviders []string `json:"modelProviders,omitempty"`
}

type threadListResponse struct {
	Data       []threadSummary `json:"data"`
	NextCursor *string         `json:"nextCursor,omitempty"`
}

type modelListParams struct {
	Cursor        *string `json:"cursor,omitempty"`
	Limit         *int    `json:"limit,omitempty"`
	IncludeHidden *bool   `json:"includeHidden,omitempty"`
}

type modelListResponse struct {
	Data       []codexModel `json:"data"`
	NextCursor *string      `json:"nextCursor,omitempty"`
}

type codexModel struct {
	ID                        string                  `json:"id"`
	DisplayName               string                  `json:"displayName"`
	Description               string                  `json:"description"`
	Hidden                    bool                    `json:"hidden"`
	SupportedReasoningEfforts []reasoningEffortOption `json:"supportedReasoningEfforts"`
	IsDefault                 bool                    `json:"isDefault"`
}

type reasoningEffortOption struct {
	ReasoningEffort string `json:"reasoningEffort"`
	Description     string `json:"description"`
}

type threadSummary struct {
	ID        string  `json:"id"`
	Cwd       string  `json:"cwd"`
	Name      *string `json:"name"`
	Preview   string  `json:"preview"`
	UpdatedAt int64   `json:"updatedAt"`
}

type turnStartParams struct {
	ThreadID       string `json:"threadId"`
	Input          []any  `json:"input"`
	ApprovalPolicy any    `json:"approvalPolicy,omitempty"`
	SandboxPolicy  any    `json:"sandboxPolicy,omitempty"`
	Model          string `json:"model,omitempty"`
	Effort         string `json:"effort,omitempty"`
}

type turnStartResponse struct {
	Turn turn `json:"turn"`
}

type turn struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type turnCompleted struct {
	ThreadID string `json:"threadId"`
	Turn     turn   `json:"turn"`
}

type turnInterruptParams struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
}

type turnSteerParams struct {
	ThreadID            string `json:"threadId"`
	ExpectedTurnID      string `json:"expectedTurnId"`
	ClientUserMessageID string `json:"clientUserMessageId,omitempty"`
	Input               []any  `json:"input"`
}

type turnSteerResponse struct {
	TurnID string `json:"turnId"`
}

type threadArchiveParams struct {
	ThreadID string `json:"threadId"`
}

type execApprovalParams struct {
	ThreadID                        string                   `json:"threadId"`
	TurnID                          string                   `json:"turnId"`
	ItemID                          string                   `json:"itemId"`
	Reason                          string                   `json:"reason,omitempty"`
	Command                         string                   `json:"command,omitempty"`
	Cwd                             string                   `json:"cwd,omitempty"`
	ProposedExecpolicyAmendment     []string                 `json:"proposedExecpolicyAmendment,omitempty"`
	ProposedNetworkPolicyAmendments []networkPolicyAmendment `json:"proposedNetworkPolicyAmendments,omitempty"`
	NetworkApprovalContext          *networkApprovalContext  `json:"networkApprovalContext,omitempty"`
}

type execApprovalResponse struct {
	Decision any `json:"decision"`
}

type networkApprovalContext struct {
	Host     string `json:"host"`
	Protocol string `json:"protocol,omitempty"`
}

type networkPolicyAmendment struct {
	Host   string `json:"host"`
	Action string `json:"action"`
}

type fileApprovalParams struct {
	ThreadID  string `json:"threadId"`
	TurnID    string `json:"turnId"`
	ItemID    string `json:"itemId"`
	Reason    string `json:"reason,omitempty"`
	GrantRoot string `json:"grantRoot,omitempty"`
}

type fileApprovalResponse struct {
	Decision string `json:"decision"`
}

type permissionsApprovalParams struct {
	ThreadID      string         `json:"threadId"`
	TurnID        string         `json:"turnId"`
	ItemID        string         `json:"itemId"`
	EnvironmentID *string        `json:"environmentId,omitempty"`
	StartedAtMs   int64          `json:"startedAtMs"`
	Cwd           string         `json:"cwd"`
	Reason        string         `json:"reason,omitempty"`
	Permissions   map[string]any `json:"permissions"`
}

type permissionsApprovalResponse struct {
	Permissions      map[string]any `json:"permissions"`
	Scope            string         `json:"scope"`
	StrictAutoReview bool           `json:"strictAutoReview"`
}

type elicitationParams struct {
	ThreadID        string          `json:"threadId"`
	ServerName      string          `json:"serverName"`
	Mode            string          `json:"mode"`
	Message         string          `json:"message"`
	RequestedSchema json.RawMessage `json:"requestedSchema"`
	URL             string          `json:"url"`
	ElicitationID   string          `json:"elicitationId"`
	Meta            map[string]any  `json:"_meta"`
}

type elicitationResponse struct {
	Action  string `json:"action"`
	Content any    `json:"content"`
	Meta    any    `json:"_meta"`
}

func (c *codexClient) initialize(ctx context.Context, p initializeParams) error {
	return c.rpc.call(ctx, "initialize", p, nil)
}

func (c *codexClient) threadStart(ctx context.Context, p threadStartParams) (threadStartResponse, error) {
	var out threadStartResponse
	err := c.rpc.call(ctx, "thread/start", p, &out)
	return out, err
}

func (c *codexClient) turnStart(ctx context.Context, p turnStartParams) (turnStartResponse, error) {
	var out turnStartResponse
	err := c.rpc.call(ctx, "turn/start", p, &out)
	return out, err
}

func (c *codexClient) turnInterrupt(ctx context.Context, p turnInterruptParams) error {
	return c.rpc.call(ctx, "turn/interrupt", p, nil)
}

func (c *codexClient) turnSteer(ctx context.Context, p turnSteerParams) error {
	var out turnSteerResponse
	return c.rpc.call(ctx, "turn/steer", p, &out)
}

func (c *codexClient) threadResume(ctx context.Context, p threadResumeParams) (threadResumeResponse, error) {
	var out threadResumeResponse
	err := c.rpc.call(ctx, "thread/resume", p, &out)
	return out, err
}

func (c *codexClient) threadFork(ctx context.Context, p threadForkParams) (threadForkResponse, error) {
	var out threadForkResponse
	err := c.rpc.call(ctx, "thread/fork", p, &out)
	return out, err
}

func (c *codexClient) threadRead(ctx context.Context, p threadReadParams) (threadReadResponse, error) {
	var out threadReadResponse
	err := c.rpc.call(ctx, "thread/read", p, &out)
	return out, err
}

func (c *codexClient) threadUnsubscribe(ctx context.Context, p threadUnsubscribeParams) error {
	return c.rpc.call(ctx, "thread/unsubscribe", p, nil)
}

func (c *codexClient) threadSetName(ctx context.Context, p threadSetNameParams) error {
	return c.rpc.call(ctx, "thread/name/set", p, nil)
}

func (c *codexClient) threadSettingsUpdate(ctx context.Context, p threadSettingsUpdateParams) error {
	return c.rpc.call(ctx, "thread/settings/update", p, nil)
}

func (c *codexClient) configRead(ctx context.Context, p configReadParams) (configReadResponse, error) {
	var out configReadResponse
	err := c.rpc.call(ctx, "config/read", p, &out)
	return out, err
}

func (c *codexClient) threadArchive(ctx context.Context, p threadArchiveParams) error {
	return c.rpc.call(ctx, "thread/archive", p, nil)
}

func (c *codexClient) threadList(ctx context.Context, p threadListParams) (threadListResponse, error) {
	var out threadListResponse
	err := c.rpc.call(ctx, "thread/list", p, &out)
	return out, err
}

func (c *codexClient) modelList(ctx context.Context, p modelListParams) (modelListResponse, error) {
	var out modelListResponse
	err := c.rpc.call(ctx, "model/list", p, &out)
	return out, err
}
