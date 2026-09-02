package tool

import "testing"

func TestPresentCompactsBuiltInTools(t *testing.T) {
	tests := []struct {
		name, kind, args string
		hasLocation      bool
		want             Presentation
	}{
		{
			name: "read", args: `{"file_path":"pkg/main.go","offset":12,"limit":4}`,
			want: Presentation{Title: "Read file", Kind: "read", Args: `{"limit":4}`, Path: "pkg/main.go", Line: 12},
		},
		{
			name: "exec_command", args: `{"command":"go test ./...","workdir":"pkg"}`,
			want: Presentation{Title: "Run command", Kind: "execute", Args: `{"workdir":"pkg"}`, Hint: "go test ./..."},
		},
		{
			name: "Find files", kind: "search", args: `{"pattern":"README*","limit":20}`,
			want: Presentation{Title: "Find files", Kind: "search", Args: `{"limit":20}`, Hint: "README*"},
		},
		{
			name: "spawn_agent", args: `{"prompt":"Review the tool UX","reasoning_effort":"high"}`,
			want: Presentation{Title: "Delegate task", Args: `{"reasoning_effort":"high"}`, Hint: "Review the tool UX"},
		},
		{
			name: "Follow up with agent", args: `{"message":"Continue the review","target":"reviewer"}`,
			want: Presentation{Title: "Follow up with agent", Args: `{"target":"reviewer"}`, Hint: "Continue the review"},
		},
		{
			name: "exec_session", args: `{"session_id":42,"input":"yes\n","wait":10}`,
			want: Presentation{Title: "Continue command", Kind: "execute", Args: `{"wait":10}`, Hint: "session 42: yes"},
		},
		{
			name: "task_send", args: `{"id":"agent-1","message":"Check aliases"}`,
			want: Presentation{Title: "Follow up with agent", Hint: "agent-1: Check aliases"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Present(tc.name, tc.kind, tc.args, tc.hasLocation); got != tc.want {
				t.Fatalf("Present() = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestPresentDerivesHintsForHumanizedNames(t *testing.T) {
	input := Present("Request input", "", `{"questions":[{"question":"Which layout?"}]}`, false)
	if input.Hint != "Which layout?" || input.Args == "" {
		t.Fatalf("input presentation = %#v", input)
	}
}

func TestPresentSummarizesMultipleWebQueries(t *testing.T) {
	got := Present("Web search", "fetch", `{"queries":["first","second","third"],"allowed_domains":["example.com"]}`, false)
	if got.Hint != "first · second +1" || got.Args != `{"allowed_domains":["example.com"]}` {
		t.Fatalf("presentation = %#v", got)
	}
}

func TestPresentKeepsPartialInputUsable(t *testing.T) {
	got := Present("shell", "", `{"command":"go test ./`, false)
	if got.Title != "Run command" || got.Kind != "execute" || got.Hint != "go test ./" || got.Args == "" {
		t.Fatalf("presentation = %#v", got)
	}
}
