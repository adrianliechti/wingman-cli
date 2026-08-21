package shell_test

import (
	"fmt"
	"strings"
	"testing"

	. "github.com/adrianliechti/wingman-agent/pkg/agent/tool/shell"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

// These tests are a security corpus, not an inventory of commands we expect
// models to emit. None of the strings are executed. The cases exercise shell
// expansions and parser differentials that have caused permission bypasses in
// agentic command runners.

func TestClassifierSecurityObfuscationRequiresApproval(t *testing.T) {
	tests := []struct {
		name    string
		command string
	}{
		// Quote removal, special quoting, and line joining all occur before the
		// shell resolves the command name or its flags.
		{"empty single quotes in command", `r''m -rf /tmp/wingman-marker`},
		{"empty double quotes in command", `r""m -rf /tmp/wingman-marker`},
		{"empty quotes in recursive flag", `rm -r''f /tmp/wingman-marker`},
		{"quoted recursive flag", `rm "-rf" /tmp/wingman-marker`},
		{"escaped recursive flag", `rm -r\f /tmp/wingman-marker`},
		{"continued recursive flag", "rm -r\\\nf /tmp/wingman-marker"},
		{"ansi c quoted command", `$'\x72\x6d' -rf /tmp/wingman-marker`},
		{"ansi c quoted flag", `rm $'\x2d\x72\x66' /tmp/wingman-marker`},
		{"locale quoted command", `$"rm" -rf /tmp/wingman-marker`},

		// Brace and pathname expansion happen after tokenization. A parser that
		// treats these as literal words can miss the command or option Bash runs.
		{"brace expanded command", `r{m,} -rf /tmp/wingman-marker`},
		{"brace expanded flag", `rm -{rf,} /tmp/wingman-marker`},
		{"quoted brace parser differential", `git diff {@'{'0},--output=/tmp/wingman-marker}`},
		{"glob expanded command", `r? -rf /tmp/wingman-marker`},

		// Expansion can construct a command name without ever spelling it as one
		// contiguous token in the source.
		{"ifs constructed command", `rm${IFS}-rf${IFS}/tmp/wingman-marker`},
		{"unset variable in command", `r${EMPTY}m -rf /tmp/wingman-marker`},
		{"positional expansion in command", `r$@m -rf /tmp/wingman-marker`},
		{"backtick constructed command", "r`printf m` -rf /tmp/wingman-marker"},
		{"variable recursive remove flag", `FLAGS=-rf; rm "$FLAGS" /tmp/wingman-marker`},
		{"variable git push flag", `FLAGS=--force; git push "$FLAGS" origin main`},

		// Bash recursively evaluates variables in arithmetic contexts. The
		// command substitution is literal during assignment, then executes when
		// x is interpreted by $((x)).
		{"recursive arithmetic expansion", `x='arr[$(touch /tmp/wingman-marker)0]'; echo $((x))`},
		{"legacy recursive arithmetic expansion", `x='arr[$(touch /tmp/wingman-marker)0]'; echo $[x]`},
		{"arithmetic command recursive expansion", `x='arr[$(touch /tmp/wingman-marker)0]'; ((x))`},
		{"let recursive expansion", `let 'x=arr[$(touch /tmp/wingman-marker)0]'`},
		{"substring offset recursive expansion", `x='arr[$(touch /tmp/wingman-marker)0]'; value=abcdef; echo "${value:x}"`},
		{"array assignment recursive expansion", `x='arr[$(touch /tmp/wingman-marker)0]'; values[x]=1`},
		{"array unset recursive expansion", `x='arr[$(touch /tmp/wingman-marker)0]'; unset 'values[x]'`},
		{"double bracket recursive arithmetic", `x='arr[$(touch /tmp/wingman-marker)0]'; [[ x -eq 0 ]]`},
		{"conditional double bracket recursive arithmetic", `x='arr[$(touch /tmp/wingman-marker)0]'; if [[ x -eq 0 ]]; then :; fi`},
		{"nested substring recursive expansion", `x='arr[$(touch /tmp/wingman-marker)0]'; value=abcdef; echo "${missing:-${value:x}}"`},
		{"indirect parameter recursive expansion", `x='arr[$(touch /tmp/wingman-marker)0]'; echo "${!x}"`},
		{"integer attribute recursive expansion", `x='arr[$(touch /tmp/wingman-marker)0]'; declare -i value=x`},
		{"later integer attribute assignment", `declare -i value; x='arr[$(touch /tmp/wingman-marker)0]'; value=x`},

		// @P parses the value as prompt text; prompt expansion performs command
		// substitution. Include array and quoted forms, not only the original
		// proof of concept.
		{"prompt transformation", `a='$'; b="$a(touch /tmp/wingman-marker)"; echo ${b@P}`},
		{"prompt transformation in quotes", `a='$'; b="$a(touch /tmp/wingman-marker)"; echo "${b@P}"`},
		{"prompt transformation array element", `a[0]='$(touch /tmp/wingman-marker)'; echo ${a[0]@P}`},
		{"nested prompt transformation", `b='$(touch /tmp/wingman-marker)'; echo "${missing:-${b@P}}"`},

		// Zsh has additional evaluation, command lookup, and module constructs.
		{"zsh evaluate parameter", `payload='rm -rf /tmp/wingman-marker'; print -r -- ${(e)payload}`},
		{"zsh module behind precommand modifier", `noglob zmodload zsh/system`},
		{"zsh history editor execution", `fc -e /tmp/wingman-editor`},

		// A quoted heredoc body is data, but command substitution in command-name
		// position executes the body's output.
		{"heredoc output as command name", "$(cat <<'EOF'\nrm\nEOF\n) -rf /tmp/wingman-marker"},
		{"recursive arithmetic in heredoc", "x='arr[$(touch /tmp/wingman-marker)0]'; cat <<EOF\n$((x))\nEOF"},
		{"legacy recursive arithmetic in heredoc", "x='arr[$(touch /tmp/wingman-marker)0]'; cat <<EOF\n$[x]\nEOF"},
		{"arithmetic shift before recursive remove", "echo $((1 << 2))\nrm -rf /tmp/wingman-marker"},
		{"single quote in comment before command", "echo ok # '\nrm -rf /tmp/wingman-marker"},
		{"double quote in comment before command", "echo ok # \"\nrm -rf /tmp/wingman-marker"},
		{"env split string retokenization", `env -S 'rm -rf /tmp/wingman-marker'`},
		{"env split string attached", `env --split-string='rm -rf /tmp/wingman-marker'`},
		{"env alternate path argument", `env -P /usr/bin rm -rf /tmp/wingman-marker`},
		{"env argv zero argument", `env --argv0 harmless rm -rf /tmp/wingman-marker`},
		{"exec argv zero argument", `exec -a harmless rm -rf /tmp/wingman-marker`},
		{"time output argument", `time -o /tmp/timing rm -rf /tmp/wingman-marker`},
		{"timeout option terminator", `timeout -- 1 rm -rf /tmp/wingman-marker`},
		{"taskset option terminator", `taskset -- 0 rm -rf /tmp/wingman-marker`},
		{"fd attached exec", `fd pattern --exec='rm' -rf /tmp/wingman-marker`},

		// Ambiguous input must fail closed instead of being partially tokenized.
		{"unterminated single quote", `rm -rf /tmp/wingman-marker '`},
		{"unterminated double quote", `rm -rf /tmp/wingman-marker "`},
		{"dangling escape", `rm -rf /tmp/wingman-marker\`},
		{"unterminated parameter expansion", `echo ${value`},

		// Non-ASCII spacing and format controls create display/parser
		// differentials. The first separator below is NBSP, the second ideographic
		// space.
		{"non breaking space", "rm\u00a0-rf /tmp/wingman-marker"},
		{"ideographic space", "rm\u3000-rf /tmp/wingman-marker"},

		// Backslash-escaped whitespace can turn an apparent allowlisted command
		// into a path whose basename is a mutating executable.
		{"escaped whitespace executable path", `echo\ test/../../../usr/bin/touch /tmp/wingman-marker`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requireEffect(t, tt.command, tool.EffectDangerous)
		})
	}
}

func TestClassifierSecurityEquivalentRecursiveRemoveSpellings(t *testing.T) {
	wrappers := []string{
		"",
		"env -- ",
		"command -- ",
		"nice -n 5 ",
		"timeout --signal KILL 1 ",
		"stdbuf -o0 ",
		"taskset -c 0 ",
		"setarch x86_64 ",
		"ionice -c 3 ",
		"nohup ",
	}
	commands := []string{
		"rm",
		`r''m`,
		`r""m`,
		`r\m`,
		"r\\\nm",
		`$'\x72\x6d'`,
		`$"rm"`,
		`r${EMPTY}m`,
		`r{m,}`,
		`r?`,
	}
	flags := []string{
		"-rf",
		"-fr",
		"-r -f",
		`-r''f`,
		`-r\f`,
		"-r\\\nf",
		`$'\x2d\x72\x66'`,
		`-{rf,}`,
		"--recursive --force",
	}

	for wi, wrapper := range wrappers {
		for ci, command := range commands {
			for fi, flag := range flags {
				full := wrapper + command + " " + flag + " /tmp/wingman-marker"
				t.Run(fmt.Sprintf("wrapper_%02d/command_%02d/flags_%02d", wi, ci, fi), func(t *testing.T) {
					requireEffect(t, full, tool.EffectDangerous)
				})
			}
		}
	}
}

func TestClassifierSecurityDeepNestedParameters(t *testing.T) {
	command := "echo " + strings.Repeat("${missing:-", 750) + "${value@P}" + strings.Repeat("}", 750)
	requireEffect(t, command, tool.EffectDangerous)
}

func TestClassifierSecuritySessionPoisoningRequiresApproval(t *testing.T) {
	tests := []struct {
		name    string
		command string
	}{
		// These values execute later in an interactive shell, often when the next
		// otherwise read-only command or prompt is processed.
		{"bash prompt command", `PROMPT_COMMAND='touch /tmp/wingman-marker'`},
		{"bash xtrace prompt", `PS4='$(touch /tmp/wingman-marker)'; set -x; :`},
		{"bash startup hook", `BASH_ENV=/tmp/wingman-hook bash -c :`},
		{"bash startup hook through env", `env BASH_ENV=/tmp/wingman-hook bash -c :`},
		{"posix startup hook", `ENV=/tmp/wingman-hook sh -c :`},
		{"zsh startup directory", `ZDOTDIR=/tmp/wingman-zdot zsh -c :`},
		{"dynamic loader hook", `LD_PRELOAD=/tmp/wingman-hook.so ls`},
		{"node startup hook", `NODE_OPTIONS=--require=/tmp/wingman-hook.js node --version`},
		{"git pager hook", `GIT_PAGER=/tmp/wingman-helper git log -1`},
		{"less preprocessor hook", `LESSOPEN='|/tmp/wingman-helper %s' less input.txt`},
		{"export prompt command", `export PROMPT_COMMAND='touch /tmp/wingman-marker'`},
		{"declare xtrace prompt", `declare PS4='$(touch /tmp/wingman-marker)'`},
		{"printf prompt command", `printf -v PROMPT_COMMAND %s 'touch /tmp/wingman-marker'`},
		{"alias poisoning", `alias ls='touch /tmp/wingman-marker'`},
		{"later alias poisoning", `alias harmless=value ls='touch /tmp/wingman-marker'`},
		{"command hash poisoning", `hash -p /tmp/wingman-ls ls`},
		{"load bash builtin", `enable -f /tmp/wingman-builtin.so ls`},
		{"bash function shorthand", `ls() { touch /tmp/wingman-marker; }`},
		{"bash compact function shorthand", `ls(){ touch /tmp/wingman-marker; }`},
		{"bash key binding command", `bind -x '"\C-x":touch /tmp/wingman-marker'`},
		{"bash completion command", `complete -C /tmp/wingman-helper ls`},

		// PowerShell state is shared across commands in a session too.
		{"powershell function poisoning", `Set-Item Function:\Get-Content { Invoke-Expression $args }`},
		{"powershell function declaration", `function Get-Content { Invoke-Expression $args }`},
		{"powershell engine event", `Register-EngineEvent PowerShell.Exiting -Action { Invoke-Expression $env:PAYLOAD }`},
		{"powershell object event", `Register-ObjectEvent $timer Elapsed -Action { Invoke-Expression $env:PAYLOAD }`},
		{"powershell key handler", `Set-PSReadLineKeyHandler -Chord Ctrl+x -ScriptBlock { Invoke-Expression $env:PAYLOAD }`},
		{"powershell abbreviated recursive delete", `Remove-Item -Re -Fo /tmp/wingman-marker`},
		{"powershell shortest recursive abbreviation", `Remove-Item -r /tmp/wingman-marker`},
		{"powershell del alias recursive delete", `del -Recurse -Force /tmp/wingman-marker`},
		{"powershell rd alias recursive delete", `rd -r /tmp/wingman-marker`},
		{"powershell abbreviated forced process stop", `Stop-Process -Name wingman-marker -f`},
		{"powershell backtick command obfuscation", "Rem`ove-It`em -Recurse -Force /tmp/wingman-marker"},
		{"powershell backtick parameter obfuscation", "Remove-Item -Re`cur`se -Fo`rc`e /tmp/wingman-marker"},
		{"nested powershell backtick obfuscation", "pwsh -Command \"Rem`ove-It`em -Re`cur`se -Fo`rc`e /tmp/wingman-marker\""},
		{"powershell variable remove flag", `$flags='-Recurse'; Remove-Item $flags /tmp/wingman-marker`},
		{"cmd delayed variable command", `cmd /v:on /c "set x=rd /s /q C:\\temp & !x!"`},
		{"nested powershell abbreviated delete", `pwsh -Command "Remove-Item -Re -Fo /tmp/wingman-marker"`},
		{"nested powershell shortest recursive abbreviation", `pwsh -Command "Remove-Item -r /tmp/wingman-marker"`},
		{"cmd caret escaped command", `cmd /c "r^mdir /s /q C:\wingman-marker"`},
		{"cmd caret escaped flag", `cmd /c "rmdir /^s /q C:\wingman-marker"`},
		{"powershell abbreviated encoded command", `pwsh -Enc SQBFAFgA`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requireEffect(t, tt.command, tool.EffectDangerous)
		})
	}
}

func TestClassifierSecurityReadOnlyBoundary(t *testing.T) {
	tests := []struct {
		name    string
		command string
	}{
		// Dynamic or obfuscated flags can become write/exec flags after shell or
		// application option parsing.
		{"find globbed exec flag", `find . -?xec touch /tmp/wingman-marker \;`},
		{"fd globbed exec flag", `fd pattern -? /tmp/wingman-marker`},
		{"git output abbreviated", `git diff --out=/tmp/wingman-marker`},
		{"git upload pack abbreviated", `git ls-remote --up=/tmp/wingman-helper local-repo`},
		{"git upload pack full", `git ls-remote --upload-pack=/tmp/wingman-helper local-repo`},
		{"git brace expanded output", `git diff {--output=/tmp/wingman-marker,HEAD}`},
		{"rg variable preprocessor", `rg . "$Z--pre=/tmp/wingman-helper" file`},
		{"ps variable environment flag", `ps ax"$Z"e`},

		// Commands in the read-only allowlist with modes that write, execute code,
		// or mutate interpreter state.
		{"jq program file", `jq -f /tmp/wingman-program.jq input.json`},
		{"jq raw file", `jq --rawfile secret /tmp/wingman-secret '.' input.json`},
		{"jq library path", `jq -L /tmp/wingman-lib '.' input.json`},
		{"less output log", `less -o /tmp/wingman-marker input.txt`},
		{"tree output file", `tree -o /tmp/wingman-marker .`},
		{"tree html output", `tree -H https://example.invalid -o /tmp/wingman-marker .`},
		{"printf variable assignment", `printf -v PATH %s /tmp`},
		{"set shell option", `set -x`},
		{"source script", `source /tmp/wingman-script.sh`},
		{"dot source script", `. /tmp/wingman-script.sh`},
		{"xargs process slot variable", `xargs --process-slot-var echo touch /tmp/wingman-marker`},

		// Script blocks make nominally Get/Select/ForEach-style PowerShell cmdlets
		// arbitrary-code runners.
		{"powershell foreach script block", `Get-ChildItem | ForEach-Object { Set-Content /tmp/wingman-marker x }`},
		{"powershell where script block", `Get-ChildItem | Where-Object { Invoke-Expression $env:PAYLOAD }`},
		{"powershell select calculated property", `Get-ChildItem | Select-Object @{n='x';e={Set-Content /tmp/wingman-marker x}}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClassifyEffect(map[string]any{"command": tt.command}); got == tool.EffectReadOnly {
				t.Fatalf("ClassifyEffect(%q) = %q; executable or mutating input must not cross the read-only boundary", tt.command, got)
			}
		})
	}
}

func TestClassifierSecuritySensitiveEnvironmentReadsRequireApproval(t *testing.T) {
	for _, command := range []string{
		`cat /proc/self/environ`,
		`cat /proc/1/environ`,
		`grep -a TOKEN /proc/self/environ`,
		`tr '\0' '\n' < /proc/self/environ`,
		`cat safe.txt \; echo ~/.ssh/id_rsa`,
		`Get-Content C:\Windows\System32\config\SAM`,
		`pwsh -Command "Get-Content C:\Windows\System32\config\SECURITY"`,
	} {
		t.Run(command, func(t *testing.T) {
			requireEffect(t, command, tool.EffectDangerous)
		})
	}
}

// A global git -c/--config-env/--exec-path runs arbitrary code (aliases,
// core.pager, core.sshCommand, ...) behind an otherwise benign-looking git
// subcommand, so it must cross the approval boundary. Subcommand-local -c
// (git branch -c, git commit -c) is unrelated and must stay quiet.
func TestClassifierSecurityGitConfigInjectionRequiresApproval(t *testing.T) {
	dangerous := []string{
		`git -c alias.pwn='!rm -rf /tmp/wingman-marker' pwn`,
		`git -c core.pager='rm -rf /tmp/wingman-marker' log`,
		`git -c core.sshCommand='touch /tmp/wingman-marker' fetch origin`,
		`git -C /repo -c core.pager='touch /tmp/wingman-marker' status`,
		`git --config-env=core.pager=EVIL log`,
		`git --exec-path=/tmp/wingman-bin status`,
	}
	for _, command := range dangerous {
		t.Run("dangerous/"+command, func(t *testing.T) {
			requireEffect(t, command, tool.EffectDangerous)
		})
	}

	quiet := []string{
		`git branch -c new-branch`,
		`git commit -c HEAD`,
		`git -C /repo status`,
	}
	for _, command := range quiet {
		t.Run("quiet/"+command, func(t *testing.T) {
			if got := ClassifyEffect(map[string]any{"command": command}); got == tool.EffectDangerous {
				t.Fatalf("ClassifyEffect(%q) = dangerous; subcommand-local option must not prompt", command)
			}
		})
	}
}

// Git subcommands whose defining purpose is to run a caller-supplied command
// over history or submodules are code-execution boundaries, exactly like
// find -exec. Their benign siblings must stay quiet.
func TestClassifierSecurityGitCommandExecutionRequiresApproval(t *testing.T) {
	dangerous := []string{
		`git rebase -x 'rm -rf /tmp/wingman-marker' main`,
		`git rebase --exec='touch /tmp/wingman-marker' main`,
		`git bisect run rm -rf /tmp/wingman-marker`,
		`git filter-branch --tree-filter 'rm -rf /tmp/wingman-marker' HEAD`,
		`git submodule foreach 'rm -rf /tmp/wingman-marker'`,
	}
	for _, command := range dangerous {
		t.Run("dangerous/"+command, func(t *testing.T) {
			requireEffect(t, command, tool.EffectDangerous)
		})
	}

	quiet := []string{
		`git rebase main`,
		`git rebase --continue`,
		`git submodule update --init`,
		`git submodule status`,
		`git bisect start`,
		`git bisect good`,
	}
	for _, command := range quiet {
		t.Run("quiet/"+command, func(t *testing.T) {
			if got := ClassifyEffect(map[string]any{"command": command}); got == tool.EffectDangerous {
				t.Fatalf("ClassifyEffect(%q) = dangerous; benign git subcommand must not prompt", command)
			}
		})
	}
}

// Privilege-escalation launchers beyond sudo/su/doas must cross the approval
// boundary as well.
func TestClassifierSecurityPrivilegeEscalationRequiresApproval(t *testing.T) {
	for _, command := range []string{
		`pkexec rm -rf /tmp/wingman-marker`,
		`run0 rm -rf /tmp/wingman-marker`,
		`gosu root rm -rf /tmp/wingman-marker`,
	} {
		t.Run(command, func(t *testing.T) {
			requireEffect(t, command, tool.EffectDangerous)
		})
	}
}

func TestClassifierSecurityMalformedWrappersFailClosed(t *testing.T) {
	for _, command := range []string{
		"exec -a", "env -u", "env -P", "nice -n", "ionice -c",
		"timeout -s", "stdbuf -o", "time -o",
	} {
		t.Run(command, func(t *testing.T) {
			requireEffect(t, command, tool.EffectDangerous)
		})
	}
}

func TestClassifierSecurityCredentialStoreReadsRequireApproval(t *testing.T) {
	for _, command := range []string{
		`cat ~/.ssh/id_ed25519`,
		`head -1 "$HOME/.aws/credentials"`,
		`grep token ~/.config/gh/hosts.yml`,
		`base64 ~/.docker/config.json`,
		`sed -n '1,10p' ~/.kube/config`,
		`cat ~/.s''sh/id_rsa`,
		`Get-Content $HOME\.azure\accessTokens.json`,
		`pwsh -NoProfile -Command "Get-Content $HOME/.config/gcloud/application_default_credentials.json"`,
		`type C:\Users\alice\.aws\credentials`,
		`cat /etc/shadow`,
	} {
		t.Run(command, func(t *testing.T) {
			requireEffect(t, command, tool.EffectDangerous)
		})
	}
}

func TestClassifierSecurityBenignCounterexamplesStayQuiet(t *testing.T) {
	tests := []struct {
		name    string
		command string
	}{
		{"literal brace text", `echo '{rm,}'`},
		{"literal prompt transformation", `printf '%s\n' '${value@P}'`},
		{"numeric arithmetic", `echo $((1 + 2))`},
		{"numeric shift arithmetic", `echo $((1 << 2))`},
		{"numeric double bracket arithmetic", `[[ 1 -lt 2 ]]`},
		{"prompt command documentation", `printf '%s\n' 'PROMPT_COMMAND=echo'`},
		{"zsh eval syntax as data", `printf '%s\n' '${(e)payload}'`},
		{"zsh history listing", `fc -l`},
		{"ordinary shell variable", `D=/tmp; echo "$D"`},
		{"ordinary exported value", `export LABEL="$D"`},
		{"ordinary powershell output variable", `Get-ChildItem -OutVariable results`},
		{"nonrecursive remove", `rm -f /tmp/wingman-marker`},
		{"quoted unicode spacing", "printf '%s\\n' 'rm\u00a0-rf'"},
		{"public ssh key", `cat ~/.ssh/id_ed25519.pub`},
		{"credential path as documentation", `printf '%s\n' '~/.ssh/id_ed25519'`},
		{"credential-looking project file", `cat fixtures/aws/credentials`},
		{"hash joined to word by continuation", "echo foo\\\n#bar"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClassifyEffect(map[string]any{"command": tt.command}); got == tool.EffectDangerous {
				t.Fatalf("ClassifyEffect(%q) = dangerous; inert counterexample must not prompt", tt.command)
			}
		})
	}
}

func FuzzClassifierSecurityNeverPanics(f *testing.F) {
	for _, command := range []string{
		`echo ${a="$"}${b="$a(touch /tmp/wingman-marker)"}${b@P}`,
		`x='arr[$(touch /tmp/wingman-marker)0]'; echo $((x))`,
		"cat <<E\"O\"F\nliteral\nEOF\nrm -rf /tmp/wingman-marker",
		"rm\x00-rf /tmp/wingman-marker",
		"printf '%s\\n' '${(e)literal}'",
		"timeout -s",
		"nice -n",
		"stdbuf -o",
		"",
	} {
		f.Add(command)
	}

	f.Fuzz(func(t *testing.T, command string) {
		effect := ClassifyEffect(map[string]any{"command": command})
		switch effect {
		case tool.EffectReadOnly, tool.EffectMutates, tool.EffectDangerous:
		default:
			t.Fatalf("unexpected effect %q", effect)
		}
		_ = IsDangerousCommand(command)
		_ = IsReadOnlyCommand(command)
	})
}

func requireEffect(t *testing.T, command string, want tool.Effect) {
	t.Helper()
	if got := ClassifyEffect(map[string]any{"command": command}); got != want {
		t.Fatalf("ClassifyEffect(%q) = %q, want %q", command, got, want)
	}
}
