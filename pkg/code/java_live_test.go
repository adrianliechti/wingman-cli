package code

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/adrianliechti/wingman-agent/internal/testenv"
	"github.com/adrianliechti/wingman-agent/pkg/dap"
)

func TestLiveManagedJavaDebugSession(t *testing.T) {
	if os.Getenv("WINGMAN_LIVE_JAVA") == "" {
		t.Skip("set WINGMAN_LIVE_JAVA=1 to install and run managed Java tooling")
	}
	for _, command := range []string{"java", "mvn"} {
		if _, err := exec.LookPath(command); err != nil {
			t.Skipf("%s is not installed", command)
		}
	}
	testenv.WingmanHome(t)

	root := t.TempDir()
	project := `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>example</groupId>
  <artifactId>wingman-live-java</artifactId>
  <version>0.0.0</version>
  <properties><maven.compiler.release>17</maven.compiler.release></properties>
</project>
`
	source := `package example;

public final class App {
    public static void main(String[] args) {
        String message = "hello from Java";
        System.out.println(message);
    }
}
`
	if err := os.WriteFile(filepath.Join(root, "pom.xml"), []byte(project), 0o644); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(root, "src", "main", "java", "example", "App.java")
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourcePath, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	workspace, err := NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	defer workspace.Close()
	workspace.WarmUp()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	if _, err := workspace.UpdateManagedTools(ctx, ManagedEditorTools); err != nil {
		t.Fatal(err)
	}

	var session *dap.Session
	if err := workspace.WithDAPManager(func(manager *dap.Manager) error {
		var startErr error
		session, startErr = manager.Start(ctx, dap.StartOptions{
			Adapter:    "java-debug",
			ProjectDir: root,
			Configuration: map[string]any{
				"mainClass":          "example.App",
				"projectName":        "wingman-live-java",
				"cwd":                root,
				"shortenCommandLine": "auto",
			},
			Breakpoints: map[string][]dap.SourceBreakpoint{
				sourcePath: {{Line: 6}},
			},
		})
		return startErr
	}); err != nil {
		t.Fatal(err)
	}
	status := session.Status()
	if status.State != dap.StateStopped {
		status, _ = session.WaitForStop(ctx, status.StateVersion)
	}
	if status.State != dap.StateStopped {
		t.Fatalf("Java breakpoint was not reached: %+v\noutput:\n%s", status, session.Output())
	}
	frames, _, err := session.StackTrace(ctx, status.Stop.ThreadID, 0, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) == 0 || frames[0].Source == nil || !sameLiveJavaFile(frames[0].Source.Path, sourcePath) || frames[0].Line != 6 {
		t.Fatalf("Java breakpoint frames = %+v", frames)
	}
	if err := workspace.WithDAPManager(func(manager *dap.Manager) error {
		return manager.Stop(ctx, session.ID())
	}); err != nil {
		t.Fatal(err)
	}
}

func sameLiveJavaFile(first, second string) bool {
	firstInfo, firstErr := os.Stat(first)
	secondInfo, secondErr := os.Stat(second)
	return firstErr == nil && secondErr == nil && os.SameFile(firstInfo, secondInfo)
}
