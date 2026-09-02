package acp

import (
	"context"

	acpsdk "github.com/coder/acp-go-sdk"
)

// Notify sends one session update, wrapping the notification envelope every
// bridge would otherwise spell out at each call site.
func Notify(ctx context.Context, conn *acpsdk.AgentSideConnection, sid acpsdk.SessionId, update acpsdk.SessionUpdate) error {
	return conn.SessionUpdate(ctx, acpsdk.SessionNotification{SessionId: sid, Update: update})
}
