package lsp

import "slices"

// ServerRequirement describes the interchangeable language-server commands
// for one project type in preference order.
type ServerRequirement struct {
	Project              string
	Commands             []string
	Directories          []string
	MinimumMajorVersions map[string]int
}

// DetectRequirements reports language-server needs independently of whether
// those servers are already installed. It shares the normal project marker
// index, so automatic installation and runtime discovery agree.
func DetectRequirements(workingDir string) []ServerRequirement {
	index := indexWorkspace(workingDir)
	var requirements []ServerRequirement
	for _, project := range knownProjects {
		directories := projectDirs(index, project)
		if len(directories) == 0 {
			continue
		}
		commands := make([]string, 0, len(project.Servers))
		minimums := make(map[string]int)
		for _, server := range project.Servers {
			if server.Command != "" && !slices.Contains(commands, server.Command) {
				commands = append(commands, server.Command)
			}
			if server.MinimumMajorVersion > minimums[server.Command] {
				minimums[server.Command] = server.MinimumMajorVersion
			}
		}
		if len(commands) > 0 {
			requirements = append(requirements, ServerRequirement{
				Project: project.Name, Commands: commands, Directories: directories,
				MinimumMajorVersions: minimums,
			})
		}
	}
	return requirements
}
