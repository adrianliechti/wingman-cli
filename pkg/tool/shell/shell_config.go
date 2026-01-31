package shell

// safeCommands is a list of read-only commands that don't require user confirmation
var safeCommands = []string{
	// Unix: Search & find
	"rg",
	"grep",
	"egrep",
	"fgrep",
	"find",
	"fd",
	"locate",
	"which",
	"whereis",
	"type",

	// Unix: List & view
	"ls",
	"cat",
	"head",
	"tail",
	"less",
	"more",
	"bat",
	"tree",
	"file",
	"stat",
	"wc",

	// Unix: Text processing (read-only, piped usage)
	"awk",
	"sed",
	"cut",
	"sort",
	"uniq",
	"tr",
	"diff",
	"comm",
	"join",
	"column",
	"jq",
	"yq",
	"xq",

	// Unix: Path utilities
	"pwd",
	"realpath",
	"dirname",
	"basename",
	"readlink",

	// Unix: System info
	"echo",
	"env",
	"printenv",
	"whoami",
	"hostname",
	"uname",
	"date",
	"uptime",
	"df",
	"du",
	"free",
	"ps",
	"top",
	"htop",
	"id",
	"groups",

	// Unix: Help & documentation
	"man",
	"help",

	// Version info commands
	"gofmt",
	"rustc",
	"javac",
	"ruby",
	"php",

	// Windows cmd.exe: Search & find
	"findstr",
	"where",

	// Windows cmd.exe: List & view
	"dir",

	// Windows cmd.exe: Text processing
	"fc",
	"comp",

	// Windows cmd.exe: System info
	"cd",
	"set",
	"time",
	"ver",
	"systeminfo",
	"tasklist",

	// PowerShell: Common read-only cmdlets (case-insensitive matching in isSafeCommand)
	"Get-Content",
	"Get-ChildItem",
	"Get-Location",
	"Get-Item",
	"Get-ItemProperty",
	"Get-Process",
	"Get-Service",
	"Get-Command",
	"Get-Help",
	"Get-Alias",
	"Get-Variable",
	"Get-Date",
	"Get-Host",
	"Get-History",
	"Get-FileHash",
	"Get-Acl",
	"Select-String",
	"Select-Object",
	"Where-Object",
	"ForEach-Object",
	"Format-List",
	"Format-Table",
	"Format-Wide",
	"Out-String",
	"Test-Path",
	"Test-Connection",
	"Measure-Object",
	"Measure-Command",
	"Compare-Object",
	"Sort-Object",
	"Group-Object",
	"Resolve-Path",
	"Split-Path",
	"Join-Path",
	"ConvertTo-Json",
	"ConvertFrom-Json",

	// PowerShell aliases
	"gc",  // Get-Content
	"gci", // Get-ChildItem
	"gl",  // Get-Location
	"gi",  // Get-Item
	"gps", // Get-Process
	"gsv", // Get-Service
	"gcm", // Get-Command
	"gal", // Get-Alias
	"gv",  // Get-Variable
	"sls", // Select-String
	"ft",  // Format-Table
	"fl",  // Format-List
	"fw",  // Format-Wide
	"oh",  // Out-Host
	"pwd", // Get-Location (also Unix)
	"cat", // Get-Content (also Unix)
	"ls",  // Get-ChildItem (also Unix)
	"dir", // Get-ChildItem (also Windows)
}

// safeSubcommands is a map of commands to their safe subcommands
// These require checking both the command and its first argument
var safeSubcommands = map[string][]string{
	// Go - read-only subcommands
	"go": {"doc", "env", "fmt", "list", "version", "vet", "help"},

	// Git - read-only subcommands
	"git": {"status", "log", "diff", "show", "branch", "tag", "remote", "config", "ls-files", "ls-tree", "rev-parse", "describe", "shortlog", "blame", "grep", "reflog", "stash list", "help", "version"},

	// GitHub CLI - read-only subcommands
	"gh": {"status", "repo view", "pr list", "pr view", "pr status", "pr diff", "issue list", "issue view", "issue status", "gist list", "gist view", "release list", "release view", "run list", "run view", "workflow list", "workflow view", "api", "help", "version"},

	// npm - read-only subcommands
	"npm": {"list", "ls", "ll", "la", "view", "info", "show", "outdated", "search", "help", "config list", "config get", "version", "explain", "why", "fund", "audit"},

	// yarn - read-only subcommands
	"yarn": {"list", "info", "why", "outdated", "licenses", "config list", "config get", "help", "version", "audit"},

	// pnpm - read-only subcommands
	"pnpm": {"list", "ls", "ll", "why", "outdated", "licenses", "config list", "config get", "help", "version", "audit"},

	// bun - read-only subcommands
	"bun": {"pm ls", "pm cache", "help", "version"},

	// deno - read-only subcommands
	"deno": {"info", "doc", "types", "help", "version", "eval"},

	// Python - read-only subcommands (version, help, etc.)
	"python":  {"-V", "--version", "-h", "--help", "-m py_compile", "-c"},
	"python3": {"-V", "--version", "-h", "--help", "-m py_compile", "-c"},

	// pip - read-only subcommands
	"pip":  {"list", "show", "freeze", "check", "config list", "config get", "help", "version", "--version"},
	"pip3": {"list", "show", "freeze", "check", "config list", "config get", "help", "version", "--version"},

	// uv - read-only subcommands
	"uv": {"pip list", "pip show", "pip freeze", "pip check", "version", "help"},

	// poetry - read-only subcommands
	"poetry": {"show", "version", "env info", "env list", "config list", "config get", "help", "about", "check"},

	// pdm - read-only subcommands
	"pdm": {"list", "show", "info", "config", "venv list", "help", "version"},

	// Cargo - read-only subcommands
	"cargo": {"tree", "metadata", "version", "search", "help", "pkgid", "verify-project", "read-manifest", "locate-project", "--version", "-V"},

	// rustup - read-only subcommands
	"rustup": {"show", "check", "component list", "target list", "toolchain list", "help", "version", "--version"},

	// Ruby gem - read-only subcommands
	"gem": {"list", "search", "info", "specification", "dependency", "environment", "help", "version", "--version"},

	// Bundler - read-only subcommands
	"bundle": {"list", "show", "info", "outdated", "check", "version", "config list", "config get", "help", "viz"},

	// Java/Maven - read-only subcommands
	"java":   {"-version", "--version", "-help", "--help"},
	"mvn":    {"dependency:tree", "dependency:list", "dependency:analyze", "help:describe", "help:effective-pom", "help:effective-settings", "versions:display-dependency-updates", "versions:display-plugin-updates", "-v", "-version", "--version", "help"},
	"gradle": {"dependencies", "projects", "tasks", "properties", "help", "-v", "-version", "--version"},

	// .NET - read-only subcommands
	"dotnet": {"list", "nuget list", "tool list", "workload list", "sdk check", "help", "--version", "--info", "--list-sdks", "--list-runtimes"},

	// Composer - read-only subcommands
	"composer": {"show", "info", "search", "outdated", "licenses", "depends", "why", "prohibits", "why-not", "config list", "diagnose", "help", "list", "about", "--version", "-V"},

	// Node - read-only
	"node": {"-v", "--version", "-h", "--help", "-e", "--eval", "-p", "--print"},

	// npx - generally safe for running local binaries
	"npx": {"which", "--version", "-v"},

	// Docker - read-only subcommands
	"docker": {"version", "--version", "-v", "info", "ps", "container ls", "container list", "images", "image ls", "image list", "volume ls", "volume list", "volume inspect", "network ls", "network list", "network inspect", "logs", "inspect", "top", "stats", "diff", "history", "port", "events", "config ls", "config inspect", "secret ls", "secret inspect", "system df", "system info", "context ls", "context show", "manifest inspect", "search", "help"},

	// Docker Compose - read-only subcommands
	"docker-compose": {"ps", "config", "images", "logs", "top", "events", "version", "--version", "help"},

	// Kubernetes kubectl - read-only subcommands
	"kubectl": {"version", "--version", "get", "describe", "logs", "explain", "api-resources", "api-versions", "cluster-info", "top", "config view", "config get-contexts", "config current-context", "auth can-i", "auth whoami", "diff", "events", "help"},

	// Helm - read-only subcommands
	"helm": {"version", "--version", "list", "ls", "status", "get", "get all", "get hooks", "get manifest", "get notes", "get values", "history", "show", "show all", "show chart", "show crds", "show readme", "show values", "search hub", "search repo", "repo list", "env", "help"},

	// Kustomize - read-only subcommands
	"kustomize": {"build", "cfg", "version", "help"},

	// Terraform/OpenTofu - read-only subcommands
	"terraform": {"version", "providers", "state list", "state show", "output", "graph", "show", "validate", "fmt", "help", "-version", "-help"},
	"tofu":      {"version", "providers", "state list", "state show", "output", "graph", "show", "validate", "fmt", "help", "-version", "-help"},
}
