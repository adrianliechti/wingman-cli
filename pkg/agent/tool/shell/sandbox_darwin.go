//go:build darwin

package shell

import (
	"fmt"
	"os"
	"strings"
)

const sandboxExecPath = "/usr/bin/sandbox-exec"

func platformSandboxCommand(shell, command, _ string, writableRoots []string) (string, []string, error) {
	if info, err := os.Stat(sandboxExecPath); err != nil || info.IsDir() {
		return "", nil, fmt.Errorf("workspace shell sandbox requires %s", sandboxExecPath)
	}
	profile, definitions := seatbeltProfile(writableRoots)
	args := []string{"-p", profile}
	args = append(args, definitions...)
	args = append(args, "--", shell, "-c", command)
	return sandboxExecPath, args, nil
}

// seatbeltProfile starts closed by default, permits the minimum runtime
// services needed by ordinary CLI programs, then opens writes only beneath
// parameterized roots. In particular, it does not use the broad mach*, ipc*,
// iokit-open, or file-ioctl grants that make many ad-hoc Seatbelt profiles
// ineffective. The service allowlist follows the same shape as the hardened
// policy used by Anthropic Sandbox Runtime, narrowed to Wingman's
// workspace-write mode. Apple Events and Launch Services operations stay
// blocked because either can ask an unsandboxed GUI process to act on behalf
// of the sandboxed command. Network remains enabled in this first mode.
func seatbeltProfile(writableRoots []string) (string, []string) {
	var writeRules strings.Builder
	var definitions []string
	for i, root := range writableRoots {
		name := fmt.Sprintf("WRITABLE_ROOT_%d", i)
		fmt.Fprintf(&writeRules, " (literal (param %q)) (subpath (param %q))", name, name)
		definitions = append(definitions, "-D"+name+"="+root)
	}

	profile := `(version 1)
(deny default)
(allow process-exec)
(allow process-fork)
(allow process-info* (target same-sandbox))
(allow signal (target same-sandbox))
(allow mach-priv-task-port (target same-sandbox))
(allow user-preference-read)
(allow mach-lookup
 (global-name "com.apple.audio.systemsoundserver")
 (global-name "com.apple.distributed_notifications@Uv3")
 (global-name "com.apple.FontObjectsServer")
 (global-name "com.apple.fonts")
 (global-name "com.apple.logd")
 (global-name "com.apple.lsd.mapdb")
 (global-name "com.apple.PowerManagement.control")
 (global-name "com.apple.system.logger")
 (global-name "com.apple.system.notification_center")
 (global-name "com.apple.system.opendirectoryd.libinfo")
 (global-name "com.apple.system.opendirectoryd.membership")
 (global-name "com.apple.bsd.dirhelper")
 (global-name "com.apple.securityd.xpc")
 (global-name "com.apple.SecurityServer")
 (global-name "com.apple.coreservices.launchservicesd"))
(allow ipc-posix-shm)
(allow ipc-posix-sem)
(allow iokit-open
 (iokit-registry-entry-class "IOSurfaceRootUserClient")
 (iokit-registry-entry-class "RootDomainUserClient")
 (iokit-user-client-class "IOSurfaceSendRight"))
(allow iokit-get-properties)
(allow system-socket
 (require-all (socket-domain AF_SYSTEM) (socket-protocol 2)))
(allow sysctl-read)
(allow sysctl-write (sysctl-name "kern.tcsm_enable"))
(allow distributed-notification-post)
(allow file-read*)
(allow file-write*` + writeRules.String() + `
 (literal "/dev/null")
 (literal "/dev/zero")
 (literal "/dev/random")
 (literal "/dev/urandom")
 (literal "/dev/tty")
 (subpath "/dev/fd")
 (literal "/dev/ptmx")
 (regex #"^/dev/ttys[0-9]+"))
(allow file-ioctl
 (literal "/dev/null")
 (literal "/dev/zero")
 (literal "/dev/random")
 (literal "/dev/urandom")
 (literal "/dev/dtracehelper")
 (literal "/dev/tty")
 (literal "/dev/ptmx")
 (regex #"^/dev/ttys"))
(allow network*)
(allow pseudo-tty)
(deny appleevent-send)
(deny lsopen)`

	return profile, definitions
}
