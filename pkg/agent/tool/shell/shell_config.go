package shell

import "strings"

// readOnlyCommands maps a command base name to the safe ways to invoke it.
//
//   - nil  = the entire command is read-only with any args
//   - []   = same as nil (no subcommand restrictions)
//   - non-empty = only invocations that start with one of these subcommand
//     prefixes are read-only (e.g. `git status`, `git log`)
//
// Lookup is case-insensitive (see normalizedReadOnlyCommands).
var readOnlyCommands = map[string][]string{
	// Unix: search & find
	"rg":      nil,
	"grep":    nil,
	"egrep":   nil,
	"fgrep":   nil,
	"find":    nil,
	"fd":      nil,
	"locate":  nil,
	"which":   nil,
	"whereis": nil,
	"type":    nil,

	// Unix: list & view
	"ls":   nil,
	"cat":  nil,
	"head": nil,
	"tail": nil,
	"less": nil,
	"more": nil,
	"bat":  nil,
	"tree": nil,
	"file": nil,
	"stat": nil,
	"wc":   nil,

	// Unix: text processing
	"cut":    nil,
	"sort":   nil,
	"uniq":   nil,
	"tr":     nil,
	"diff":   nil,
	"comm":   nil,
	"join":   nil,
	"column": nil,
	"jq":     nil,
	"yq":     nil,
	"xq":     nil,

	// Unix: path utilities
	"pwd":      nil,
	"realpath": nil,
	"dirname":  nil,
	"basename": nil,
	"readlink": nil,

	// Unix: system info
	"echo":     nil,
	"printenv": nil,
	"whoami":   nil,
	"uname":    nil,
	"uptime":   nil,
	"df":       nil,
	"du":       nil,
	"free":     nil,
	"ps":       nil,
	"top":      nil,
	"htop":     nil,
	"id":       nil,
	"groups":   nil,

	// Unix: help & docs
	"man":  nil,
	"help": nil,

	// Windows cmd.exe: list & view
	"dir":     nil,
	"findstr": nil,
	"where":   nil,
	"cd":      nil,
	"set":     nil,

	// PowerShell: common read-only cmdlets
	"get-content":      nil,
	"get-childitem":    nil,
	"get-location":     nil,
	"get-item":         nil,
	"get-itemproperty": nil,
	"get-process":      nil,
	"get-service":      nil,
	"get-command":      nil,
	"get-help":         nil,
	"get-alias":        nil,
	"get-variable":     nil,
	"get-date":         nil,
	"get-host":         nil,
	"get-history":      nil,
	"get-filehash":     nil,
	"get-acl":          nil,
	"select-string":    nil,
	"select-object":    nil,
	"where-object":     nil,
	"foreach-object":   nil,
	"format-list":      nil,
	"format-table":     nil,
	"test-path":        nil,
	"test-connection":  nil,
	"measure-object":   nil,
	"compare-object":   nil,
	"sort-object":      nil,
	"group-object":     nil,
	"resolve-path":     nil,
	"split-path":       nil,
	"join-path":        nil,

	// PowerShell aliases (not duplicating ones already covered by Unix names above)
	"gc":  nil, // Get-Content
	"gci": nil, // Get-ChildItem
	"gl":  nil, // Get-Location
	"gi":  nil, // Get-Item
	"gps": nil, // Get-Process
	"gsv": nil, // Get-Service
	"gcm": nil, // Get-Command
	"gal": nil, // Get-Alias
	"gv":  nil, // Get-Variable
	"sls": nil, // Select-String
	"ft":  nil, // Format-Table
	"fl":  nil, // Format-List
	"oh":  nil, // Out-Host

	// Subcommand-restricted tools

	"go": {"doc", "list", "version", "help"},

	"git": {"status", "log", "diff", "show", "branch", "tag", "remote", "ls-files", "ls-tree", "rev-parse", "describe", "shortlog", "blame", "grep", "reflog", "stash list", "help", "version"},

	"gh": {"status", "repo view", "pr list", "pr view", "pr status", "pr diff", "issue list", "issue view", "issue status", "gist list", "gist view", "release list", "release view", "run list", "run view", "workflow list", "workflow view", "help", "version"},

	"npm":  {"list", "ls", "ll", "la", "view", "info", "show", "outdated", "search", "help", "config list", "config get", "version", "explain", "why", "fund", "audit"},
	"yarn": {"list", "info", "why", "outdated", "licenses", "config list", "config get", "help", "version", "audit"},
	"pnpm": {"list", "ls", "ll", "why", "outdated", "licenses", "config list", "config get", "help", "version", "audit"},
	"bun":  {"pm ls", "pm cache", "help", "version"},
	"deno": {"info", "doc", "types", "help", "version"},

	"python":  {"-V", "--version", "-h", "--help"},
	"python3": {"-V", "--version", "-h", "--help"},
	"node":    {"-v", "--version", "-h", "--help"},
	"npx":     {"which", "--version", "-v"},

	"pip":  {"list", "show", "freeze", "check", "config list", "config get", "help", "version", "--version"},
	"pip3": {"list", "show", "freeze", "check", "config list", "config get", "help", "version", "--version"},
	"uv":   {"pip list", "pip show", "pip freeze", "pip check", "version", "help"},

	"poetry": {"show", "version", "env info", "env list", "config list", "config get", "help", "about", "check"},
	"pdm":    {"list", "show", "info", "config", "venv list", "help", "version"},

	"cargo":  {"tree", "metadata", "version", "search", "help", "pkgid", "verify-project", "read-manifest", "locate-project", "--version", "-V"},
	"rustup": {"show", "check", "component list", "target list", "toolchain list", "help", "version", "--version"},

	"gem":    {"list", "search", "info", "specification", "dependency", "environment", "help", "version", "--version"},
	"bundle": {"list", "show", "info", "outdated", "check", "version", "config list", "config get", "help", "viz"},

	"java":   {"-version", "--version", "-help", "--help"},
	"mvn":    {"dependency:tree", "dependency:list", "dependency:analyze", "help:describe", "help:effective-pom", "help:effective-settings", "versions:display-dependency-updates", "versions:display-plugin-updates", "-v", "-version", "--version", "help"},
	"gradle": {"dependencies", "projects", "tasks", "properties", "help", "-v", "-version", "--version"},

	"dotnet":   {"list", "nuget list", "tool list", "workload list", "sdk check", "help", "--version", "--info", "--list-sdks", "--list-runtimes"},
	"composer": {"show", "info", "search", "outdated", "licenses", "depends", "why", "prohibits", "why-not", "config list", "diagnose", "help", "list", "about", "--version", "-V"},

	"docker":         {"version", "--version", "-v", "info", "ps", "container ls", "container list", "images", "image ls", "image list", "volume ls", "volume list", "volume inspect", "network ls", "network list", "network inspect", "logs", "inspect", "top", "stats", "diff", "history", "port", "events", "config ls", "config inspect", "secret ls", "secret inspect", "system df", "system info", "context ls", "context show", "manifest inspect", "search", "help"},
	"docker-compose": {"ps", "config", "images", "logs", "top", "events", "version", "--version", "help"},

	"kubectl":   {"version", "--version", "get", "describe", "logs", "explain", "api-resources", "api-versions", "cluster-info", "top", "config view", "config get-contexts", "config current-context", "auth can-i", "auth whoami", "diff", "events", "help"},
	"helm":      {"version", "--version", "list", "ls", "status", "get", "get all", "get hooks", "get manifest", "get notes", "get values", "history", "show", "show all", "show chart", "show crds", "show readme", "show values", "search hub", "search repo", "repo list", "env", "help"},
	"kustomize": {"build", "cfg", "version", "help"},

	"terraform": {"version", "providers", "state list", "state show", "output", "graph", "show", "validate", "help", "-version", "-help"},
	"tofu":      {"version", "providers", "state list", "state show", "output", "graph", "show", "validate", "help", "-version", "-help"},
}

// normalizedReadOnlyCommands holds the same data lower-cased once at startup,
// so per-command lookup is a single map access.
var normalizedReadOnlyCommands = map[string][]string{}

func init() {
	for cmd, subs := range readOnlyCommands {
		key := strings.ToLower(cmd)
		if len(subs) == 0 {
			normalizedReadOnlyCommands[key] = nil
			continue
		}
		lowered := make([]string, len(subs))
		for i, s := range subs {
			lowered[i] = strings.ToLower(s)
		}
		normalizedReadOnlyCommands[key] = lowered
	}
}
