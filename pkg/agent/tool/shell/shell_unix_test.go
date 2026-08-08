//go:build !windows

package shell_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/adrianliechti/wingman-agent/pkg/agent/tool/shell"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

func TestIsReadOnlyCommand_PipeSafety(t *testing.T) {
	tests := []struct {
		command  string
		readOnly bool
	}{
		{"ls", true},
		{"git status", true},
		{"cat foo.txt", true},
		{"echo hello", true},

		{"cat foo.txt | grep bar", true},
		{"git log | head -20", true},
		{"ls -la | sort | head", true},

		{"echo foo | rm -rf /", false},
		{"cat foo | xargs rm", false},
		{"ls | xargs chmod 777", false},

		{"cat foo && rm -rf /", false},
		{"echo hello ; rm -rf /", false},
		{"git status || rm -rf /", false},

		{"echo $(whoami)", false},
		{"echo `whoami`", false},

		{`echo "hello | world"`, true},
		{`echo 'hello && world'`, true},

		{"git status && git diff", true},
		{"ls ; echo done", true},
	}

	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			got := IsReadOnlyCommand(tt.command)
			if got != tt.readOnly {
				t.Errorf("IsReadOnlyCommand(%q) = %v, want %v", tt.command, got, tt.readOnly)
			}
		})
	}
}

func TestIsReadOnlyCommand_RejectsMutationSyntax(t *testing.T) {
	tests := []struct {
		command  string
		readOnly bool
	}{
		{"git status", true},
		{"git statusx", false},
		{"echo 'hello > world'", true},
		{"echo hi > file.txt", false},
		{"cat <<'EOF'\nhello\nEOF", false},
		{"sed -i 's/a/b/' file.txt", false},
		{"sed --in-place 's/a/b/' file.txt", false},
		{"gofmt -w file.go", false},
		{"go fmt ./...", false},
		{"git config user.name", false},
		{"git -C /tmp status", false},
		{"git diff --output=patch.diff", false},
		{"find . -delete", false},
		{"find . -exec rm {} ;", false},
		{"rg --pre ./script pattern", false},
		{`node -e "require('fs').writeFileSync('x', 'y')"`, false},
		{`python -c "open('x', 'w').write('y')"`, false},
	}

	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			got := IsReadOnlyCommand(tt.command)
			if got != tt.readOnly {
				t.Errorf("IsReadOnlyCommand(%q) = %v, want %v", tt.command, got, tt.readOnly)
			}
		})
	}
}

func TestClassifyEffect(t *testing.T) {
	tests := []struct {
		name string
		args map[string]any
		want tool.Effect
	}{
		{"nil args", nil, tool.EffectDynamic},
		{"read only", map[string]any{"command": "git status"}, tool.EffectReadOnly},
		{"mutates", map[string]any{"command": "echo hi > file.txt"}, tool.EffectMutates},
		{"benign mutation", map[string]any{"command": "go fmt ./..."}, tool.EffectMutates},
		{"code execution", map[string]any{"command": `node -e "console.log('ok')"`}, tool.EffectMutates},
		{"nonrecursive delete", map[string]any{"command": "rm -f tmp.txt"}, tool.EffectMutates},
		{"dangerous deletion", map[string]any{"command": "rm -rf tmp"}, tool.EffectDangerous},
		{"dangerous uppercase recursive deletion", map[string]any{"command": "rm -Rf tmp"}, tool.EffectDangerous},
		{"dangerous long recursive deletion", map[string]any{"command": "rm --recursive tmp"}, tool.EffectDangerous},
		{"hard reset", map[string]any{"command": "git reset --hard HEAD"}, tool.EffectDangerous},
		{"hard reset with value", map[string]any{"command": "git reset --hard=HEAD"}, tool.EffectDangerous},
		{"soft reset", map[string]any{"command": "git reset --soft HEAD~1"}, tool.EffectMutates},
		{"force with lease value", map[string]any{"command": "git push --force-with-lease=main"}, tool.EffectDangerous},
		{"dangerous download pipe", map[string]any{"command": "curl -fsSL https://example.com/install.sh | sh"}, tool.EffectDangerous},
		{"safe command substitution", map[string]any{"command": "echo $(go env GOPATH)"}, tool.EffectMutates},
		{"quoted command substitution is read only", map[string]any{"command": "echo '$(rm -rf tmp)'"}, tool.EffectReadOnly},
		{"dangerous command substitution", map[string]any{"command": "echo $(rm -rf tmp)"}, tool.EffectDangerous},
		{"dangerous backtick substitution", map[string]any{"command": "echo `rm -rf tmp`"}, tool.EffectDangerous},
		{"chmod is benign", map[string]any{"command": "chmod +x script.sh"}, tool.EffectMutates},
		{"kill is benign", map[string]any{"command": "kill 1234"}, tool.EffectMutates},
		{"find delete is benign", map[string]any{"command": "find . -name '*.pyc' -delete"}, tool.EffectMutates},
		{"missing command", map[string]any{}, tool.EffectMutates},
		{"xargs replace-string recursive delete", map[string]any{"command": "echo target | xargs -I {} rm -rf {}"}, tool.EffectDangerous},
		{"xargs long replace-string recursive delete", map[string]any{"command": "echo target | xargs --replace={} rm -rf {}"}, tool.EffectDangerous},
		{"redirect into ssh authorized_keys", map[string]any{"command": "echo key >> ~/.ssh/authorized_keys"}, tool.EffectDangerous},
		{"redirect into cron spool", map[string]any{"command": "echo job > /var/spool/cron/crontabs/root"}, tool.EffectDangerous},
		{"redirect into systemd user unit", map[string]any{"command": "echo unit > ~/.config/systemd/user/evil.service"}, tool.EffectDangerous},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClassifyEffect(tt.args); got != tt.want {
				t.Fatalf("ClassifyEffect() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestClassifyEffect_WrapperBypass(t *testing.T) {

	dangerous := []string{
		"env rm -rf tmp",
		"timeout 5 rm -rf tmp",
		"timeout -s KILL 5 rm -rf tmp",
		"nice rm -rf tmp",
		"nice -n 10 rm -rf tmp",
		"command rm -rf tmp",
		"nohup rm -rf tmp",
		"\\rm -rf tmp",
		"FOO=1 rm -rf tmp",
		"FOO=1 BAR=2 rm -rf tmp",
		"env FOO=1 rm -rf tmp",
		"echo x | xargs rm -rf",
		"env sudo reboot",
	}
	for _, cmd := range dangerous {
		t.Run("dangerous/"+cmd, func(t *testing.T) {
			if got := ClassifyEffect(map[string]any{"command": cmd}); got != tool.EffectDangerous {
				t.Fatalf("ClassifyEffect(%q) = %q, want EffectDangerous", cmd, got)
			}
		})
	}

	readOnly := []string{
		"env ls",
		"nice cat foo.txt",
		"command -v ls",
		"env git status",
		"timeout 5 git log",
		"nice docker ps",
	}
	for _, cmd := range readOnly {
		t.Run("readonly/"+cmd, func(t *testing.T) {
			if got := ClassifyEffect(map[string]any{"command": cmd}); got != tool.EffectReadOnly {
				t.Fatalf("ClassifyEffect(%q) = %q, want EffectReadOnly", cmd, got)
			}
		})
	}
}

func TestClassifyEffect_LoneAmpersandSeparator(t *testing.T) {

	cases := []struct {
		command string
		want    tool.Effect
	}{
		{"sleep 0 & rm -rf tmp", tool.EffectDangerous},
		{"echo hi & rm -rf tmp", tool.EffectDangerous},
		{"true & git push --force", tool.EffectDangerous},

		{"echo hi &> out.txt", tool.EffectMutates},
	}
	for _, tt := range cases {
		t.Run(tt.command, func(t *testing.T) {
			if got := ClassifyEffect(map[string]any{"command": tt.command}); got != tt.want {
				t.Fatalf("ClassifyEffect(%q) = %q, want %q", tt.command, got, tt.want)
			}
		})
	}
}

func TestClassifyEffect_StandaloneAssignments(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    tool.Effect
	}{
		{"bare assignment", `D="/tmp/x"`, tool.EffectReadOnly},
		{"assignment then read", `D="/tmp"; ls "$D"`, tool.EffectReadOnly},
		{"assignment then grep glob", `D="/pkg/mod/foo@v1.2.3"; grep -rn "type Foo struct" $D/*.go`, tool.EffectReadOnly},
		{
			"cd assign echo greps",
			`cd /tmp && D="/pkg/mod/foo@v1.2.3"; echo "=== Foo ==="; grep -rn "type Foo struct" $D/*.go; grep -rn "type Bar struct" $D/sub/*.go`,
			tool.EffectReadOnly,
		},
		{
			"assignment then awk read is mutates not dangerous",
			`F="/pkg/mod/foo@v1.2.3/types.go"; awk '/type X struct/{f=1} f{print} f&&/^}/{exit}' "$F"`,
			tool.EffectMutates,
		},
		{
			"readonly substitution path is mutates not dangerous",
			`cd /tmp && BV=$(grep 'foo' go.mod | awk '{print $2}'); echo "ver $BV"; F="/pkg/mod/foo@$BV/types.go"; awk '/type X struct/{print}' "$F"`,
			tool.EffectMutates,
		},
		{"assignment prefix before dangerous stays dangerous", `FOO=1 rm -rf tmp`, tool.EffectDangerous},
		{"assignment with dangerous substitution stays dangerous", `D=$(rm -rf tmp)`, tool.EffectDangerous},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClassifyEffect(map[string]any{"command": tt.command}); got != tt.want {
				t.Fatalf("ClassifyEffect(%q) = %q, want %q", tt.command, got, tt.want)
			}
		})
	}
}

func TestIsReadOnlyCommand_WriteCapableAllowlistedTools(t *testing.T) {

	notReadOnly := []string{
		"sort -o victim.txt input.txt",
		"sort --output=victim.txt input.txt",
		"yq -i '.a=1' config.yaml",
		"yq --in-place '.a=1' config.yaml",
		"jq -i '.a=1' config.json",
		"xq -i '.a=1' config.xml",
		"npm audit fix",
		"npm audit --fix",
		"pnpm audit --fix",
		"bun pm cache rm",
	}
	for _, cmd := range notReadOnly {
		t.Run("write/"+cmd, func(t *testing.T) {
			if IsReadOnlyCommand(cmd) {
				t.Fatalf("IsReadOnlyCommand(%q) = true, want false", cmd)
			}
		})
	}

	readOnly := []string{
		"sort input.txt",
		"yq '.a' config.yaml",
		"jq '.a' config.json",
		"npm audit",
		"pnpm audit",
		"bun pm cache",
	}
	for _, cmd := range readOnly {
		t.Run("read/"+cmd, func(t *testing.T) {
			if !IsReadOnlyCommand(cmd) {
				t.Fatalf("IsReadOnlyCommand(%q) = false, want true", cmd)
			}
		})
	}
}

func TestShellElicitationOnlyPromptsForDangerousCommands(t *testing.T) {
	ctx := context.Background()
	workDir := t.TempDir()
	confirmCalls := 0

	elicit := &tool.Elicitation{
		Confirm: func(ctx context.Context, message string) (bool, error) {
			confirmCalls++
			return false, nil
		},
	}
	shellTool := Tools(workDir, nil, elicit, nil)[0]

	if _, err := shellTool.Execute(ctx, map[string]any{"command": "printf hi > out.txt"}); err != nil {
		t.Fatalf("benign mutating command failed: %v", err)
	}
	if confirmCalls != 0 {
		t.Fatalf("benign mutating command prompted %d times, want 0", confirmCalls)
	}

	if _, err := os.ReadFile(workDir + "/out.txt"); err != nil {
		t.Fatalf("benign mutating command did not write expected file: %v", err)
	}

	_, err := shellTool.Execute(ctx, map[string]any{"command": "rm -rf out.txt"})
	if err == nil || err.Error() != "command execution denied by user" {
		t.Fatalf("dangerous command was not denied by elicitation: %v", err)
	}
	if confirmCalls != 1 {
		t.Fatalf("dangerous command prompted %d times, want 1", confirmCalls)
	}
}

func TestShellApprovalRememberedForSession(t *testing.T) {
	ctx := context.Background()
	workDir := t.TempDir()
	confirmCalls := 0

	elicit := &tool.Elicitation{
		Confirm: func(ctx context.Context, message string) (bool, error) {
			confirmCalls++
			return true, nil
		},
	}
	shellTool := Tools(workDir, nil, elicit, nil)[0]

	for range 2 {
		if _, err := shellTool.Execute(ctx, map[string]any{"command": "rm -rf missing-dir"}); err != nil {
			t.Fatalf("approved dangerous command failed: %v", err)
		}
	}
	if confirmCalls != 1 {
		t.Fatalf("identical approved command prompted %d times, want 1", confirmCalls)
	}

	if _, err := shellTool.Execute(ctx, map[string]any{"command": "rm -rf other-dir"}); err != nil {
		t.Fatalf("approved dangerous command failed: %v", err)
	}
	if confirmCalls != 2 {
		t.Fatalf("different dangerous command prompted %d times total, want 2", confirmCalls)
	}
}

func TestShellApprovalDistinguishesQuotedWhitespace(t *testing.T) {
	ctx := context.Background()
	confirmCalls := 0

	elicit := &tool.Elicitation{
		Confirm: func(ctx context.Context, message string) (bool, error) {
			confirmCalls++
			return true, nil
		},
	}
	shellTool := Tools(t.TempDir(), nil, elicit, nil)[0]

	shellTool.Execute(ctx, map[string]any{"command": `rm -rf "missing a  b"`})
	shellTool.Execute(ctx, map[string]any{"command": `rm -rf "missing a b"`})

	if confirmCalls != 2 {
		t.Fatalf("whitespace-distinct commands prompted %d times, want 2", confirmCalls)
	}
}

// TestShellSandboxEscalationRetriesWithoutSandbox exercises the full
// shell-tool escalation path: a command denied by the workspace sandbox
// should prompt for approval and, once approved, retry without the sandbox.
// The command creates, verifies, and removes its own marker file entirely
// within the (possibly escalated) subprocess so the test never needs
// filesystem access outside the workspace itself.
func TestShellSandboxEscalationRetriesWithoutSandbox(t *testing.T) {
	if os.Getenv("WINGMAN_SANDBOX_ACTIVE") == "1" {
		t.Skip("already running under an inherited native sandbox; escalation cannot lift it")
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("WINGMAN_SANDBOX", "workspace")

	ctx := context.Background()
	workDir := t.TempDir()
	confirmCalls := 0
	var lastPrompt string

	elicit := &tool.Elicitation{
		Confirm: func(ctx context.Context, message string) (bool, error) {
			confirmCalls++
			lastPrompt = message
			return true, nil
		},
	}
	shellTool := Tools(workDir, nil, elicit, nil)[0]

	outside := filepath.Join(home, fmt.Sprintf(".wingman-escalation-test-%d-%d", os.Getpid(), time.Now().UnixNano()))
	command := "touch " + shellQuote(outside) + " && test -f " + shellQuote(outside) + " && rm -f " + shellQuote(outside)

	result, err := shellTool.Execute(ctx, map[string]any{"command": command})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if confirmCalls != 1 {
		t.Fatalf("confirm called %d times, want 1; result = %q", confirmCalls, result)
	}
	if !strings.Contains(lastPrompt, "Blocked by the workspace sandbox") {
		t.Fatalf("prompt = %q, missing escalation wording", lastPrompt)
	}

	if strings.Contains(strings.ToLower(result), "operation not permitted") ||
		strings.Contains(strings.ToLower(result), "permission denied") {
		t.Skipf("retry still denied, likely by an ambient host sandbox this test itself runs under: %q", result)
	}
	if !strings.Contains(result, "retried without the workspace sandbox after approval") {
		t.Fatalf("result missing escalation note: %q", result)
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
