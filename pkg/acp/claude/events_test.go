package claude

import "testing"

func TestStreamedBlocksRemoveConsolidatedDuplicateByContent(t *testing.T) {
	streamed := &streamedBlockTracker{}
	streamed.append(0, "text", "Let me inspect ")
	streamed.append(0, "text", "the files.")

	got := streamed.consume([]cliMsgBlock{{
		Type: "text", Text: "Let me inspect the files.",
	}})
	if len(got) != 0 {
		t.Fatalf("consolidated duplicate was retained: %#v", got)
	}
	if len(streamed.blocks) != 0 {
		t.Fatalf("streamed blocks were not reset: %#v", streamed.blocks)
	}
}

func TestStreamedBlocksKeepOnlyUnstreamedRemainder(t *testing.T) {
	streamed := &streamedBlockTracker{}
	streamed.append(0, "text", "hello ")

	got := streamed.consume([]cliMsgBlock{{Type: "text", Text: "hello world"}})
	if len(got) != 1 || got[0].Text != "world" {
		t.Fatalf("remainder = %#v, want text block %q", got, "world")
	}
}

func TestStreamedBlocksPreserveUnstreamedBlockTypes(t *testing.T) {
	streamed := &streamedBlockTracker{}
	streamed.append(1, "text", "visible answer")

	got := streamed.consume([]cliMsgBlock{
		{Type: "text", Text: "visible answer"},
		{Type: "thinking", Thinking: "unstreamed reasoning"},
		{Type: "tool_use", ID: "tool-1", Name: "Read"},
	})
	if len(got) != 2 || got[0].Thinking != "unstreamed reasoning" || got[1].Type != "tool_use" {
		t.Fatalf("unstreamed content was not preserved: %#v", got)
	}
}

func TestStreamedBlocksResetAtNextMessage(t *testing.T) {
	streamed := &streamedBlockTracker{}
	streamed.append(0, "text", "cancelled answer")
	streamed.reset()

	got := streamed.consume([]cliMsgBlock{{Type: "text", Text: "next answer"}})
	if len(got) != 1 || got[0].Text != "next answer" {
		t.Fatalf("prior message suppressed the next answer: %#v", got)
	}
}
