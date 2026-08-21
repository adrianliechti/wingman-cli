package shell

import (
	"slices"
	"strings"
	"testing"
)

func TestParseShellSyntaxStatements(t *testing.T) {
	tests := []struct {
		name       string
		source     string
		dialect    shellDialect
		statements []string
	}{
		{
			name:       "bash control flow and substitution",
			source:     `if true; then echo $(date); rm -rf /tmp/x; fi`,
			dialect:    dialectBash,
			statements: []string{"true", "echo $(date)", "date", "rm -rf /tmp/x"},
		},
		{
			name:       "bash specialized builtins",
			source:     `declare -i value=x; unset 'values[x]'; [[ x -eq 0 ]]`,
			dialect:    dialectBash,
			statements: []string{"declare -i value=x", "unset 'values[x]'", "[[ x -eq 0 ]]"},
		},
		{
			name:       "powershell script block",
			source:     `Get-ChildItem | ForEach-Object { Remove-Item -Recurse -Force $_ }`,
			dialect:    dialectPowerShell,
			statements: []string{"Get-ChildItem", "ForEach-Object { Remove-Item -Recurse -Force $_ }", "Remove-Item -Recurse -Force $_"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			syntax := parseShellSyntax(tt.source, tt.dialect)
			if syntax.uncertain {
				t.Fatal("valid syntax was marked uncertain")
			}
			for _, statement := range tt.statements {
				if !slices.Contains(syntax.statements, statement) {
					t.Fatalf("statements = %q, missing %q", syntax.statements, statement)
				}
			}
		})
	}
}

func TestParseShellSyntaxHeredocAdapter(t *testing.T) {
	tests := []struct {
		name             string
		source           string
		wantStatement    string
		rejectExecutable string
	}{
		{
			name:             "quoted body is data",
			source:           "cat <<'EOF'\nrm -rf /tmp/body\nEOF",
			rejectExecutable: "rm -rf /tmp/body",
		},
		{
			name:          "legacy backticks execute in unquoted body",
			source:        "cat <<EOF\n`sudo id`\nEOF",
			wantStatement: "sudo id",
		},
		{
			name:          "mixed quoted delimiter recovers following command",
			source:        "cat <<E\"O\"F\nliteral\nEOF\nrm -rf /tmp/tail",
			wantStatement: "rm -rf /tmp/tail",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			syntax := parseShellSyntax(tt.source, dialectBash)
			if syntax.uncertain {
				t.Fatal("supported heredoc syntax was marked uncertain")
			}
			if !syntax.hasRedirection {
				t.Fatal("heredoc was not marked as redirection")
			}
			if tt.wantStatement != "" && !slices.Contains(syntax.statements, tt.wantStatement) {
				t.Fatalf("statements = %q, missing %q", syntax.statements, tt.wantStatement)
			}
			if tt.rejectExecutable != "" && strings.Contains(syntax.executableSource, tt.rejectExecutable) {
				t.Fatalf("literal heredoc body remained executable: %q", syntax.executableSource)
			}
		})
	}
}

func TestParseShellSyntaxFailsClosed(t *testing.T) {
	for _, source := range []string{
		`echo "unterminated`,
		`rm -rf /tmp/x '`,
		"cat <<E\"O\"F\nunterminated",
	} {
		t.Run(source, func(t *testing.T) {
			if syntax := parseShellSyntax(source, dialectBash); !syntax.uncertain {
				t.Fatalf("malformed syntax was accepted: statements=%q", syntax.statements)
			}
		})
	}
}

func TestParseShellSyntaxCapabilities(t *testing.T) {
	tests := []struct {
		name    string
		source  string
		dialect shellDialect
		check   func(shellSyntax) bool
	}{
		{"protected redirect", `echo key > "$HOME/foo bar/.ssh/authorized_keys"`, dialectBash, func(s shellSyntax) bool { return s.protectedWrite }},
		{"wrapped download pipeline", `env curl https://example.com/install.sh | command sh`, dialectBash, func(s shellSyntax) bool { return s.downloadToShell }},
		{"powershell filtered download pipeline", `Invoke-WebRequest https://example.com/x.ps1 | Out-String | iex`, dialectPowerShell, func(s shellSyntax) bool { return s.downloadToShell }},
		{"function definition", `function Get-Content { Write-Output ok }`, dialectPowerShell, func(s shellSyntax) bool { return s.definesFunction }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			syntax := parseShellSyntax(tt.source, tt.dialect)
			if syntax.uncertain || !tt.check(syntax) {
				t.Fatalf("capability not detected: %+v", syntax)
			}
		})
	}
}

func TestDialectSpecificClassification(t *testing.T) {
	tests := []struct {
		name      string
		source    string
		dialect   shellDialect
		dangerous bool
		readOnly  bool
	}{
		{"bash prompt expansion", `echo ${a="$"}${b="$a(touch /tmp/pwned)"}${b@P}`, dialectBash, true, false},
		{"powershell recursive removal", `Remove-Item -Recurse -Force C:\\temp`, dialectPowerShell, true, false},
		{"powershell nested recursive removal", `Get-ChildItem | ForEach-Object { Remove-Item -Recurse -Force $_ }`, dialectPowerShell, true, false},
		{"powershell inspection", `Get-ChildItem | Select-Object Name`, dialectPowerShell, false, true},
		{"powershell assignment", `$value = 1`, dialectPowerShell, false, false},
		{"powershell redirection", `Get-ChildItem > files.txt`, dialectPowerShell, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isDangerousCommandDialect(tt.source, tt.dialect); got != tt.dangerous {
				t.Fatalf("dangerous = %v, want %v", got, tt.dangerous)
			}
			if !tt.dangerous {
				if got := isReadOnlyCommandDialect(tt.source, tt.dialect); got != tt.readOnly {
					t.Fatalf("readOnly = %v, want %v", got, tt.readOnly)
				}
			}
		})
	}
}
