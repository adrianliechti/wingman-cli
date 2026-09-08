package skill_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman-agent/internal/testenv"
	. "github.com/adrianliechti/wingman-agent/pkg/skill"
)

func TestDiscoverLinkedSkills(t *testing.T) {
	for _, kind := range []string{"symlink", "junction"} {
		for _, scope := range []string{"project", "personal"} {
			for _, placement := range []string{"root", "group", "skill"} {
				t.Run(kind+"/"+scope+"/"+placement, func(t *testing.T) {
					home := testenv.UserHome(t)
					testenv.WingmanHome(t)
					workspace := t.TempDir()
					configRoot := workspace
					if scope == "personal" {
						configRoot = home
					}
					source := filepath.Join(configRoot, ".agents", "skills")
					target := t.TempDir()
					writeSkill(t, filepath.Join(target, "review"), "review", "Shared review workflow")
					link, destination := source, target
					switch placement {
					case "group":
						link = filepath.Join(source, "team")
					case "skill":
						link = filepath.Join(source, "review")
						destination = filepath.Join(target, "review")
					}
					if err := os.MkdirAll(filepath.Dir(link), 0755); err != nil {
						t.Fatal(err)
					}
					testenv.DirLink(t, kind, destination, link)
					// A back edge and a second alias must not recurse forever or
					// load the same skill twice.
					testenv.DirLink(t, kind, target, filepath.Join(target, "loop"))
					testenv.DirLink(t, kind, destination, link+"-duplicate")

					discover := func() ([]Skill, error) { return Discover(workspace) }
					rediscover := func() ([]Skill, error) { return Rediscover(workspace) }
					if scope == "personal" {
						discover, rediscover = DiscoverPersonal, RediscoverPersonal
					}
					skills, err := discover()
					if err != nil || len(skills) != 1 || skills[0].Name != "review" {
						t.Fatalf("discovered skills = %#v, %v", skills, err)
					}
					if content, err := skills[0].GetContent(workspace); err != nil || !strings.Contains(content, "# review") {
						t.Fatalf("linked skill content = %q, %v", content, err)
					}

					writeSkill(t, filepath.Join(target, "review"), "review", "Updated shared workflow")
					skills, err = rediscover()
					if err != nil || len(skills) != 1 || skills[0].Description != "Updated shared workflow" {
						t.Fatalf("refreshed skills = %#v, %v", skills, err)
					}
				})
			}
		}
	}
}

func TestDiscoverPersonalExcludesLinkedSystemSkills(t *testing.T) {
	for _, kind := range []string{"symlink", "junction"} {
		t.Run(kind, func(t *testing.T) {
			home := testenv.UserHome(t)
			wingman := testenv.WingmanHome(t)
			system := filepath.Join(wingman, "skills", ".system")
			writeSkill(t, filepath.Join(system, "bundled"), "bundled", "Bundled skill")
			target := t.TempDir()
			writeSkill(t, filepath.Join(target, "review"), "review", "Personal skill")
			testenv.DirLink(t, kind, system, filepath.Join(target, "managed"))
			agents := filepath.Join(home, ".agents")
			if err := os.MkdirAll(agents, 0755); err != nil {
				t.Fatal(err)
			}
			testenv.DirLink(t, kind, target, filepath.Join(agents, "skills"))
			skills, err := DiscoverPersonal()
			if err != nil || len(skills) != 1 || skills[0].Name != "review" {
				t.Fatalf("personal skills = %#v, %v; want review only", skills, err)
			}
		})
	}
}
