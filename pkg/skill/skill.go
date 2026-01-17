package skill

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"gopkg.in/yaml.v3"
)

type Skill struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`

	Location string `yaml:"-"`
}

var skillDirs = []string{
	".skills",
	".github",
	".claude",
	".opencode",
}

func Discover(root string) ([]Skill, error) {
	var skills []Skill

	for _, dir := range skillDirs {
		skillDir := filepath.Join(root, dir)
		matches, err := doublestar.Glob(os.DirFS(skillDir), "**/SKILL.md")

		if err != nil {
			continue
		}

		for _, match := range matches {
			skillFile := filepath.Join(skillDir, match)
			skill, err := parseSkillMetadata(skillFile)

			if err != nil {
				continue
			}

			location := filepath.Dir(skillFile)

			if rel, err := filepath.Rel(root, location); err == nil {
				location = rel
			}

			skill.Location = location
			skills = append(skills, skill)
		}
	}

	return skills, nil
}

func parseSkillMetadata(path string) (Skill, error) {
	f, err := os.Open(path)

	if err != nil {
		return Skill{}, err
	}

	defer f.Close()

	scanner := bufio.NewScanner(f)

	var inFrontmatter bool
	var frontmatter strings.Builder

	for scanner.Scan() {
		line := scanner.Text()

		if line == "---" {
			if !inFrontmatter {
				inFrontmatter = true

				continue
			}

			break
		}

		if inFrontmatter {
			frontmatter.WriteString(line)
			frontmatter.WriteString("\n")
		}
	}

	if err := scanner.Err(); err != nil {
		return Skill{}, err
	}

	var skill Skill

	if err := yaml.Unmarshal([]byte(frontmatter.String()), &skill); err != nil {
		return Skill{}, fmt.Errorf("failed to parse frontmatter: %w", err)
	}

	if skill.Name == "" || skill.Description == "" {
		return Skill{}, fmt.Errorf("skill missing required fields")
	}

	return skill, nil
}

func FormatForPrompt(skills []Skill) string {
	if len(skills) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("<available_skills>\n")

	for _, s := range skills {
		sb.WriteString("  <skill>\n")
		sb.WriteString(fmt.Sprintf("    <name>%s</name>\n", s.Name))
		sb.WriteString(fmt.Sprintf("    <description>%s</description>\n", s.Description))
		sb.WriteString(fmt.Sprintf("    <location>%s/SKILL.md</location>\n", s.Location))
		sb.WriteString("  </skill>\n")
	}

	sb.WriteString("</available_skills>")

	return sb.String()
}
