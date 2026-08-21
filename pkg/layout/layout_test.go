package layout

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/adrianliechti/wingman-agent/internal/testenv"
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

func TestWingmanPathUsesDefaultHome(t *testing.T) {
	home := testenv.UserHome(t)
	t.Setenv(wingmanHomeEnv, "")

	got, err := WingmanPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".wingman"); got != want {
		t.Fatalf("WingmanHome = %q, want %q", got, want)
	}
}

func TestWingmanPathUsesOverride(t *testing.T) {
	want := testenv.WingmanHome(t)

	got, err := WingmanPath()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("WingmanHome = %q, want %q", got, want)
	}
}

func TestWingmanPathMakesRelativeOverrideAbsolute(t *testing.T) {
	t.Setenv(wingmanHomeEnv, filepath.Join("testdata", "..", "state"))

	got, err := WingmanPath("config.json")
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.Abs(filepath.Join("state", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("WingmanPath = %q, want %q", got, want)
	}
}

func TestWingmanPathRejectsEscape(t *testing.T) {
	testenv.WingmanHome(t)

	if _, err := WingmanPath("..", "outside"); err == nil {
		t.Fatal("WingmanPath accepted a path outside WINGMAN_HOME")
	}
}

func TestPersonalRootsUseHome(t *testing.T) {
	home := testenv.UserHome(t)
	t.Setenv(wingmanHomeEnv, "")

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

func TestPersonalRootsUseWingmanHome(t *testing.T) {
	home := testenv.UserHome(t)
	wingmanHome := testenv.WingmanHome(t)

	roots := PersonalRoots("plugins")

	want := []string{
		filepath.Join(wingmanHome, "plugins"),
		filepath.Join(home, ".agents", "plugins"),
		filepath.Join(home, ".claude", "plugins"),
	}

	if !slices.Equal(roots, want) {
		t.Fatalf("PersonalRoots = %v, want %v", roots, want)
	}
}

func TestRootsPutProjectFirst(t *testing.T) {
	home := testenv.UserHome(t)
	t.Setenv(wingmanHomeEnv, "")

	roots := Roots("/work", "agents")

	if len(roots) != 6 {
		t.Fatalf("Roots returned %d entries: %v", len(roots), roots)
	}

	projectDirs := [...]string{wingmanDir, agentsDir, claudeDir}
	for i, root := range roots[:3] {
		if want := filepath.Join("/work", projectDirs[i], "agents"); root != want {
			t.Fatalf("Roots[%d] = %q, want %q", i, root, want)
		}
	}

	for i, root := range roots[3:] {
		if want := filepath.Join(home, projectDirs[i], "agents"); root != want {
			t.Fatalf("Roots[%d] = %q, want %q", i+3, root, want)
		}
	}
}
