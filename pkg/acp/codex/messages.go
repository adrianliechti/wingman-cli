package codex

import "github.com/coder/acp-go-sdk"

func userMessageUpdate(content acp.ContentBlock, id string) acp.SessionUpdate {
	u := acp.UpdateUserMessage(content)
	if id != "" {
		u.UserMessageChunk.MessageId = new(id)
	}
	return u
}

func agentMessageUpdate(text, id, phase string) acp.SessionUpdate {
	u := acp.UpdateAgentMessageText(text)
	if id != "" {
		u.AgentMessageChunk.MessageId = new(id)
	}
	if phase != "" {
		u.AgentMessageChunk.Meta = map[string]any{"codex": map[string]any{"phase": phase}}
	}
	return u
}

func agentThoughtUpdate(text, id string) acp.SessionUpdate {
	u := acp.UpdateAgentThoughtText(text)
	if id != "" {
		u.AgentThoughtChunk.MessageId = new(id)
	}
	return u
}

func sessionTitleUpdate(title string) acp.SessionUpdate {
	return acp.SessionUpdate{SessionInfoUpdate: &acp.SessionSessionInfoUpdate{
		SessionUpdate: "session_info_update",
		Title:         &title,
	}}
}
