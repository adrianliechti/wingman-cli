package layout

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestProjectRootsOrder(t *testing.T) {
	roots := ProjectRoots("/work", "skills")

	want := []string{
		filepath.Join("/work", ".wingman", "skills"),
		filepath.Join("/work", ".agents", "skills"),
		filepath.Join("/work", ".claude", "skills"),
	}

	if !slices.Equal(roots, want) {
		t.Fatalf("ProjectRoots = %v, want %v", roots, want)
	}
}

func TestPersonalRootsUseHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	roots := PersonalRoots("plugins")

	want := []string{
		filepath.Join(home, ".wingman", "plugins"),
		filepath.Join(home, ".agents", "plugins"),
		filepath.Join(home, ".claude", "plugins"),
	}

	if !slices.Equal(roots, want) {
		t.Fatalf("PersonalRoots = %v, want %v", roots, want)
	}
}

func TestRootsPutProjectFirst(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	roots := Roots("/work", "agents")

	if len(roots) != 6 {
		t.Fatalf("Roots returned %d entries: %v", len(roots), roots)
	}

	for i, root := range roots[:3] {
		if want := filepath.Join("/work", Dirs[i], "agents"); root != want {
			t.Fatalf("Roots[%d] = %q, want %q", i, root, want)
		}
	}

	for i, root := range roots[3:] {
		if want := filepath.Join(home, Dirs[i], "agents"); root != want {
			t.Fatalf("Roots[%d] = %q, want %q", i+3, root, want)
		}
	}
}
