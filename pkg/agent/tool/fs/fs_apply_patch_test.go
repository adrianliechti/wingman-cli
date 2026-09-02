package fs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newPatchTestRoot(t *testing.T) (*os.Root, string) {
	t.Helper()
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return root, dir
}

func runPatch(t *testing.T, root *os.Root, patch string) (string, error) {
	t.Helper()
	result, err := ApplyPatchTool(root, NewFreshness(root)).ExecuteText(context.Background(), patch)
	return result.Content, err
}

func TestApplyPatchEditsSeveralFilesInOneCall(t *testing.T) {
	root, dir := newPatchTestRoot(t)
	if err := os.WriteFile(filepath.Join(dir, "app.txt"), []byte("header\nold value\nfooter\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gone.txt"), []byte("remove me\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	output, err := runPatch(t, root, `*** Begin Patch
*** Update File: app.txt
@@
 header
-old value
+new value
 footer
*** Add File: nested/new.txt
+one
+two
*** Delete File: gone.txt
*** End Patch`)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "app.txt")); err != nil || string(got) != "header\nnew value\nfooter\n" {
		t.Fatalf("app.txt = %q, err=%v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "nested", "new.txt")); err != nil || string(got) != "one\ntwo\n" {
		t.Fatalf("new.txt = %q, err=%v", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "gone.txt")); !os.IsNotExist(err) {
		t.Fatalf("gone.txt still exists: %v", err)
	}
	for _, want := range []string{"M app.txt", "A nested/new.txt", "D gone.txt"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output %q does not contain %q", output, want)
		}
	}
}

func TestApplyPatchValidationIsAtomic(t *testing.T) {
	root, dir := newPatchTestRoot(t)
	for _, name := range []string{"one.txt", "two.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("before\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	_, err := runPatch(t, root, `*** Begin Patch
*** Update File: one.txt
@@
-before
+after
*** Update File: two.txt
@@
-missing
+after
*** End Patch`)
	if err == nil || !strings.Contains(err.Error(), "failed to find expected lines") {
		t.Fatalf("error = %v", err)
	}
	for _, name := range []string{"one.txt", "two.txt"} {
		got, readErr := os.ReadFile(filepath.Join(dir, name))
		if readErr != nil || string(got) != "before\n" {
			t.Fatalf("%s changed despite failed patch: %q, err=%v", name, got, readErr)
		}
	}
}

func TestApplyPatchMovesFileAndPreservesMode(t *testing.T) {
	root, dir := newPatchTestRoot(t)
	source := filepath.Join(dir, "old.sh")
	if err := os.WriteFile(source, []byte("#!/bin/sh\r\necho old\r\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := runPatch(t, root, `*** Begin Patch
*** Update File: old.sh
*** Move to: bin/new.sh
@@
-echo old
+echo new
*** End Patch`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("old path still exists: %v", err)
	}
	destination := filepath.Join(dir, "bin", "new.sh")
	got, err := os.ReadFile(destination)
	if err != nil || string(got) != "#!/bin/sh\r\necho new\r\n" {
		t.Fatalf("new file = %q, err=%v", got, err)
	}
	info, err := os.Stat(destination)
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("new mode = %v, err=%v", info.Mode().Perm(), err)
	}
}

func TestApplyPatchRejectsWorkspaceEscapeAndDuplicatePaths(t *testing.T) {
	root, dir := newPatchTestRoot(t)
	for _, patch := range []string{
		"*** Begin Patch\n*** Add File: ../escape.txt\n+no\n*** End Patch",
		"*** Begin Patch\n*** Add File: " + filepath.Join(filepath.Dir(dir), "escape.txt") + "\n+no\n*** End Patch",
		"*** Begin Patch\n*** Add File: same.txt\n+one\n*** Add File: same.txt\n+two\n*** End Patch",
	} {
		if _, err := runPatch(t, root, patch); err == nil {
			t.Fatalf("patch unexpectedly succeeded:\n%s", patch)
		}
	}
}

func TestApplyPatchAcceptsAbsolutePathInsideWorkspace(t *testing.T) {
	root, dir := newPatchTestRoot(t)
	path := filepath.Join(dir, "inside.txt")
	if err := os.WriteFile(path, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := runPatch(t, root, "*** Begin Patch\n*** Update File: "+path+"\n@@\n-before\n+after\n*** End Patch")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "after\n" {
		t.Fatalf("inside.txt = %q, err=%v", got, err)
	}
}
