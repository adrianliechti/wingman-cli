package plugin

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/adrianliechti/wingman-agent/internal/pathutil"
	"github.com/adrianliechti/wingman-agent/internal/testenv"
)

func TestDiscoverDirectoryLinks(t *testing.T) {
	for _, kind := range []string{"symlink", "junction"} {
		for _, scope := range []string{"project", "personal"} {
			for _, level := range []string{"root", "plugin"} {
				t.Run(kind+"/"+scope+"/"+level, func(t *testing.T) {
					home, _ := isolateHome(t)
					work := t.TempDir()
					parent := work
					if scope == "personal" {
						parent = home
					}
					link := filepath.Join(parent, ".agents", "plugins")
					shared := t.TempDir()
					target := filepath.Join(shared, "demo")
					installPlugin(t, target, "demo")
					if level == "plugin" {
						link = filepath.Join(link, "demo")
						shared = target
					}
					if err := os.MkdirAll(filepath.Dir(link), 0755); err != nil {
						t.Fatal(err)
					}
					testenv.DirLink(t, kind, shared, link)
					plugins, notes := Discover(work, t.TempDir(), t.TempDir())
					want, err := pathutil.Resolve(target)
					if err != nil {
						t.Fatal(err)
					}
					if len(plugins) != 1 || plugins[0].Root != want || len(notes) != 0 {
						t.Fatalf("plugins = %#v, notes = %v; want root %q", plugins, notes, want)
					}
				})
			}
		}
	}
}

func TestPluginDirectoryLinkBoundaries(t *testing.T) {
	for _, kind := range []string{"symlink", "junction"} {
		for _, outside := range []bool{false, true} {
			name := "inside"
			if outside {
				name = "outside"
			}
			t.Run(kind+"/"+name, func(t *testing.T) {
				root := writePlugin(t, map[string]string{
					"plugin.json": validManifest,
					"mcp.json":    `{"$schema":"https://agent-plugins.org/schemas/1.0.0/mcp.schema.json","mcpServers":{"local":{"type":"stdio","command":"./resources/server"}}}`,
				})
				target := filepath.Join(root, "real")
				if outside {
					target = t.TempDir()
				}
				skillDir := filepath.Join(target, "demo")
				if err := os.MkdirAll(skillDir, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: demo\ndescription: Demo skill\n---\nDemo"), 0644); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(target, "server"), nil, 0755); err != nil {
					t.Fatal(err)
				}
				testenv.DirLink(t, kind, target, filepath.Join(root, "skills"))
				testenv.DirLink(t, kind, target, filepath.Join(root, "resources"))
				alias := filepath.Join(t.TempDir(), "plugin")
				testenv.DirLink(t, kind, root, alias)
				p, notes, err := Load(alias, t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				if outside {
					if len(p.Skills) != 0 || len(p.Servers) != 0 || len(notes) == 0 {
						t.Fatalf("escaping components accepted: %#v, notes %v", p, notes)
					}
				} else if len(p.Skills) != 1 || len(p.Servers) != 1 || len(notes) != 0 {
					t.Fatalf("contained components rejected: %#v, notes %v", p, notes)
				}
				_, err = resolveContained("./resources/missing/output", p.Root, p.Root)
				if (err != nil) != outside {
					t.Fatalf("future output containment: outside = %v, err = %v", outside, err)
				}
			})
		}
	}
}

func TestPluginDataDirectoryLinkBoundaries(t *testing.T) {
	for _, kind := range []string{"symlink", "junction"} {
		t.Run(kind, func(t *testing.T) {
			root := writePlugin(t, map[string]string{"plugin.json": validManifest})
			data := t.TempDir()
			alias := filepath.Join(t.TempDir(), "data")
			testenv.DirLink(t, kind, data, alias)
			p, _, err := Load(root, alias)
			if err != nil {
				t.Fatal(err)
			}
			resolved, err := pathutil.Resolve(data)
			if err != nil || p.Data != filepath.Join(resolved, "demo") {
				t.Fatalf("data = %q, resolve error = %v", p.Data, err)
			}
			testenv.DirLink(t, kind, t.TempDir(), filepath.Join(data, "demo"))
			if _, _, err := Load(root, alias); err == nil {
				t.Fatal("accepted plugin data escaping the data root")
			}
		})
	}
}
