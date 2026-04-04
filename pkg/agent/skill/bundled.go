package skill

import (
	"embed"
)

//go:embed bundled/*/SKILL.md
var bundledFS embed.FS

// BundledSkills returns all built-in skills loaded from the embedded filesystem.
func BundledSkills() []Skill {
	skills, _ := LoadBundled(bundledFS, "bundled")
	return skills
}
