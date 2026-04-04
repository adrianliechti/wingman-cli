//go:build !windows

package shell

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

func testEnv(t *testing.T) *tool.Environment {
	t.Helper()

	tmpDir := t.TempDir()
	root, err := os.OpenRoot(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })

	scratch, err := os.OpenRoot(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { scratch.Close() })

	return &tool.Environment{
		Root:    root,
		Scratch: scratch,
	}
}

func runShell(t *testing.T, command string) string {
	t.Helper()
	env := testEnv(t)
	result, err := executeShell(context.Background(), env, map[string]any{
		"command": command,
		"timeout": float64(10),
	})
	if err != nil {
		t.Fatalf("executeShell error: %v", err)
	}
	return result
}

func TestComplex_MultiLineScript(t *testing.T) {
	result := runShell(t, `
x=hello
y=world
echo "$x $y"
`)
	if !strings.Contains(result, "hello world") {
		t.Errorf("multi-line script failed, got: %q", result)
	}
}

func TestComplex_ForLoop(t *testing.T) {
	result := runShell(t, `for i in 1 2 3; do echo "num:$i"; done`)
	if !strings.Contains(result, "num:1") || !strings.Contains(result, "num:3") {
		t.Errorf("for loop failed, got: %q", result)
	}
}

func TestComplex_WhileLoop(t *testing.T) {
	result := runShell(t, `
i=0
while [ $i -lt 3 ]; do
  echo "count:$i"
  i=$((i + 1))
done
`)
	if !strings.Contains(result, "count:0") || !strings.Contains(result, "count:2") {
		t.Errorf("while loop failed, got: %q", result)
	}
}

func TestComplex_IfElse(t *testing.T) {
	result := runShell(t, `
if [ 1 -eq 1 ]; then
  echo "yes"
else
  echo "no"
fi
`)
	if !strings.Contains(result, "yes") {
		t.Errorf("if/else failed, got: %q", result)
	}
}

func TestComplex_Heredoc(t *testing.T) {
	result := runShell(t, `cat <<'EOF'
line one
line two
line three
EOF`)
	if !strings.Contains(result, "line one") || !strings.Contains(result, "line three") {
		t.Errorf("heredoc failed, got: %q", result)
	}
}

func TestComplex_HeredocWithVariables(t *testing.T) {
	result := runShell(t, `
NAME=wingman
cat <<EOF
hello $NAME
EOF
`)
	if !strings.Contains(result, "hello wingman") {
		t.Errorf("heredoc with vars failed, got: %q", result)
	}
}

func TestComplex_PipeChain(t *testing.T) {
	result := runShell(t, `echo -e "banana\napple\ncherry" | sort | head -2`)
	if !strings.Contains(result, "apple") || !strings.Contains(result, "banana") {
		t.Errorf("pipe chain failed, got: %q", result)
	}
}

func TestComplex_Subshell(t *testing.T) {
	result := runShell(t, `(echo "inside subshell")`)
	if !strings.Contains(result, "inside subshell") {
		t.Errorf("subshell failed, got: %q", result)
	}
}

func TestComplex_CommandSubstitution(t *testing.T) {
	result := runShell(t, `echo "today is $(date +%Y)"`)
	if !strings.Contains(result, "today is 20") {
		t.Errorf("command substitution failed, got: %q", result)
	}
}

func TestComplex_EmbeddedPython(t *testing.T) {
	// Skip if python3 not available
	result := runShell(t, `python3 -c "print('hello from python')" 2>/dev/null || python -c "print('hello from python')" 2>/dev/null || echo "python not found"`)
	if !strings.Contains(result, "hello from python") && !strings.Contains(result, "python not found") {
		t.Errorf("embedded python failed, got: %q", result)
	}
}

func TestComplex_EmbeddedNode(t *testing.T) {
	result := runShell(t, `node -e "console.log('hello from node')" 2>/dev/null || echo "node not found"`)
	if !strings.Contains(result, "hello from node") && !strings.Contains(result, "node not found") {
		t.Errorf("embedded node failed, got: %q", result)
	}
}

func TestComplex_StderrMergedWithStdout(t *testing.T) {
	result := runShell(t, `echo "stdout" && echo "stderr" >&2`)
	if !strings.Contains(result, "stdout") || !strings.Contains(result, "stderr") {
		t.Errorf("stderr not merged, got: %q", result)
	}
}

func TestComplex_ExitCodeReported(t *testing.T) {
	result := runShell(t, `exit 42`)
	if !strings.Contains(result, "exited with code 42") {
		t.Errorf("exit code not reported, got: %q", result)
	}
}

func TestComplex_EnvironmentVariables(t *testing.T) {
	result := runShell(t, `echo "git_editor=$GIT_EDITOR"`)
	if !strings.Contains(result, "git_editor=true") {
		t.Errorf("GIT_EDITOR not set, got: %q", result)
	}
}

func TestComplex_SpecialCharacters(t *testing.T) {
	result := runShell(t, `echo 'quotes "and" special $chars & stuff'`)
	if !strings.Contains(result, `quotes "and" special $chars & stuff`) {
		t.Errorf("special chars failed, got: %q", result)
	}
}

func TestComplex_MultiLineHeredocScript(t *testing.T) {
	// This simulates what the model would send for a complex git commit
	result := runShell(t, `cat <<'EOF'
This is a multi-line
commit message with "quotes"
and $pecial characters
EOF`)
	if !strings.Contains(result, "commit message") || !strings.Contains(result, `"quotes"`) || !strings.Contains(result, "$pecial") {
		t.Errorf("complex heredoc failed, got: %q", result)
	}
}

func TestComplex_LargeOutputTruncation(t *testing.T) {
	// Generate output larger than maxLines (2000)
	result := runShell(t, `for i in $(seq 1 3000); do echo "line $i"; done`)
	if !strings.Contains(result, "Output truncated") {
		t.Errorf("expected truncation notice, got length: %d", len(result))
	}
	// Should contain the LAST lines (tail truncation)
	if !strings.Contains(result, "line 3000") {
		t.Errorf("expected last lines preserved, got tail: %q", result[len(result)-100:])
	}
}

func TestComplex_Timeout(t *testing.T) {
	env := testEnv(t)
	_, err := executeShell(context.Background(), env, map[string]any{
		"command": "sleep 30",
		"timeout": float64(1),
	})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("expected timeout message, got: %v", err)
	}
}
