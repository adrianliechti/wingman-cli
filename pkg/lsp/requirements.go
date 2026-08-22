package lsp

import "slices"

// ServerRequirement describes the interchangeable language-server commands
// for one project type in preference order.
type ServerRequirement struct {
	Project  string
	Commands []string
}

// DetectRequirements reports language-server needs independently of whether
// those servers are already installed. It shares the normal project marker
// index, so automatic installation and runtime discovery agree.
func DetectRequirements(workingDir string) []ServerRequirement {
	index := indexWorkspace(workingDir)
	var requirements []ServerRequirement
	for _, project := range knownProjects {
		if len(projectDirs(index, project)) == 0 {
			continue
		}
		commands := make([]string, 0, len(project.Servers))
		for _, server := range project.Servers {
			if server.Command != "" && !slices.Contains(commands, server.Command) {
				commands = append(commands, server.Command)
			}
		}
		if len(commands) > 0 {
			requirements = append(requirements, ServerRequirement{Project: project.Name, Commands: commands})
		}
	}
	return requirements
}
