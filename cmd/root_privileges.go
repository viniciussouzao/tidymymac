package cmd

import (
	"errors"
	"fmt"
	"os"
)

// ErrRunningAsRoot is returned by every entry point that can delete files when
// the whole program was started under sudo.
var ErrRunningAsRoot = errors.New("refusing to delete while running as root")

// refuseRootDeletion blocks execute mode when the process itself is elevated.
//
// The elevation model exists so that root is granted to one narrow job -- the
// deletion of an already-approved plan, re-bounded by a fresh privileged scan
// -- and never to the program as a whole. `sudo tidymymac clean --execute`
// bypasses that model entirely: config loading still resolves the invoking
// user's home through SUDO_USER, so every cleaner scans the real user's
// directories, and then every cleaner deletes them as root.
//
// That is worse than the elevated helper, not equivalent to it, for two
// reasons. There is no plan/fresh-scan intersection, so nothing re-checks what
// the user approved. And RequiresSudo() == false never meant "cannot run as
// root" -- it only means "the helper will not accept this category in a plan"
// -- so the cleaners that call os.RemoveAll on a whole directory tree
// (project artifacts, app orphans, iOS backups, Downloads, Trash) are exactly
// the ones the helper was designed never to run elevated, and they are the
// ones with the largest blast radius if a path component is swapped between
// scan and deletion.
//
// Dry runs are left alone: they delete nothing, and refusing them would only
// make the tool harder to inspect.
//
// euid is a parameter rather than a direct os.Geteuid() call so the guard is
// testable without actually being root.
func refuseRootDeletion(euid int) error {
	if euid != 0 {
		return nil
	}
	return fmt.Errorf(
		"%w.\n\n"+
			"Run tidymymac normally, without sudo. It asks for your password only\n"+
			"for the categories that genuinely need root, and elevates just the\n"+
			"deletion of what you already approved -- everything else keeps\n"+
			"running as you.",
		ErrRunningAsRoot,
	)
}

// geteuid is indirected so tests can exercise the actual command wiring --
// which is the part that can rot -- rather than only refuseRootDeletion.
var geteuid = os.Geteuid

// guardRootDeletion applies refuseRootDeletion to the current process.
//
// It must be called from the RunE of every entry point that can delete, never
// from the root command's PersistentPreRunE: the hidden elevated helper shares
// that hook, and being root is its entire job. The helper runs its own guards
// instead (euid, SUDO_UID, plan validation) and exits with
// HelperGuardRejectedExitCode when they reject.
func guardRootDeletion() error {
	return refuseRootDeletion(geteuid())
}
