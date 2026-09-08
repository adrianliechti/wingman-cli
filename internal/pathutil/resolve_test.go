package pathutil_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman-agent/internal/pathutil"
	"github.com/adrianliechti/wingman-agent/internal/testenv"
)

func TestResolveDirectoryLinks(t *testing.T) {
	for _, kind := range []string{"symlink", "junction"} {
		t.Run(kind, func(t *testing.T) {
			base := t.TempDir()
			target := filepath.Join(base, "Shared Skills")
			// Exercise paths beyond MAX_PATH as well as ordinary file and
			// directory paths through chained links.
			nested := filepath.Join(strings.Repeat("nested-", 15), strings.Repeat("resource-", 15))
			if err := os.MkdirAll(filepath.Join(target, nested), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(target, nested, "guide.md"), []byte("guide"), 0644); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(base, "skills")
			testenv.DirLink(t, kind, target, link)
			chain := filepath.Join(base, "chain")
			testenv.DirLink(t, kind, link, chain)
			testenv.DirLink(t, kind, target, filepath.Join(target, "loop"))
			for _, alias := range []string{link, chain, filepath.Join(link, "loop", "loop")} {
				for _, suffix := range []string{"", nested, filepath.Join(nested, "guide.md")} {
					want, err := pathutil.Resolve(filepath.Join(target, suffix))
					if err != nil {
						t.Fatal(err)
					}
					got, err := pathutil.Resolve(filepath.Join(alias, suffix))
					if err != nil || got != want {
						t.Errorf("Resolve(%q) = %q, %v; want %q", filepath.Join(alias, suffix), got, err, want)
					}
				}
			}
		})
	}
}

func TestResolveBrokenDirectoryLink(t *testing.T) {
	for _, kind := range []string{"symlink", "junction"} {
		t.Run(kind, func(t *testing.T) {
			base := t.TempDir()
			target, link := filepath.Join(base, "target"), filepath.Join(base, "link")
			if err := os.Mkdir(target, 0755); err != nil {
				t.Fatal(err)
			}
			testenv.DirLink(t, kind, target, link)
			if err := os.Remove(target); err != nil {
				t.Fatal(err)
			}
			if _, err := pathutil.Resolve(link); err == nil {
				t.Fatal("resolved a dangling directory link")
			}
		})
	}
}

func TestResolveExistingPrefixDirectoryLinks(t *testing.T) {
	for _, kind := range []string{"symlink", "junction"} {
		t.Run(kind, func(t *testing.T) {
			base := t.TempDir()
			target, link := filepath.Join(base, "target"), filepath.Join(base, "link")
			if err := os.Mkdir(target, 0755); err != nil {
				t.Fatal(err)
			}
			testenv.DirLink(t, kind, target, link)
			resolved, err := pathutil.Resolve(target)
			if err != nil {
				t.Fatal(err)
			}
			for _, suffix := range []string{"", filepath.Join("missing", "output")} {
				path := filepath.Join(link, suffix)
				if got, err := pathutil.ResolveExistingPrefix(path); err != nil || got != filepath.Join(resolved, suffix) {
					t.Fatalf("ResolveExistingPrefix(%q) = %q, %v", path, got, err)
				}
			}
			if err := os.Remove(target); err != nil {
				t.Fatal(err)
			}
			for _, suffix := range []string{"", filepath.Join("missing", "output")} {
				if _, err := pathutil.ResolveExistingPrefix(filepath.Join(link, suffix)); err == nil {
					t.Fatalf("accepted dangling %s with suffix %q", kind, suffix)
				}
			}
		})
	}
}

func TestResolveExistingPrefixRejectsFileParent(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := pathutil.ResolveExistingPrefix(filepath.Join(file, "output")); err == nil {
		t.Fatal("accepted a regular file as the parent of an output path")
	}
}
