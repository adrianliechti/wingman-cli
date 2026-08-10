package tool

import (
	"context"
	"testing"
)

type progressContextKey struct{}

func TestProgressPassesReportingContextToSink(t *testing.T) {
	var gotContext string
	var gotCallID string
	var gotText string

	ctx := WithProgressSink(context.Background(), func(ctx context.Context, callID, text string) {
		gotContext, _ = ctx.Value(progressContextKey{}).(string)
		gotCallID = callID
		gotText = text
	})
	ctx = context.WithValue(ctx, progressContextKey{}, "session-1")
	ctx = WithProgressCall(ctx, "call-1")

	report := Progress(ctx)
	if report == nil {
		t.Fatal("expected progress reporter")
	}
	report("working")

	if gotContext != "session-1" || gotCallID != "call-1" || gotText != "working" {
		t.Fatalf("progress = context %q, call %q, text %q", gotContext, gotCallID, gotText)
	}
}
