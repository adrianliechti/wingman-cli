package fs

import "testing"

func TestAllowedRootCanBeOnDifferentWindowsVolume(t *testing.T) {
	workspace := `C:\work\project`
	systemRoot := `D:\Wingman\skills\.system`
	skillFile := `D:\Wingman\skills\.system\feature-dev\SKILL.md`

	if !isOutsideWorkspace(skillFile, workspace) {
		t.Fatalf("%q was treated as inside workspace %q", skillFile, workspace)
	}
	root, sub, ok := matchAllowedRoot(skillFile, []string{systemRoot})
	if !ok {
		t.Fatalf("%q did not match allowed root %q", skillFile, systemRoot)
	}
	if root != systemRoot || sub != `feature-dev\SKILL.md` {
		t.Fatalf("allowed-root match = (%q, %q), want (%q, %q)", root, sub, systemRoot, `feature-dev\SKILL.md`)
	}
}
