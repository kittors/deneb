// Command denebd is Deneb's networking engine and its command line in one
// binary, so a server or a test machine needs one file (plan §3 M1: the
// Windows link is slow).
//
//	denebd [run] [flags]     run the engine
//	denebd login|status|lan|ping|...   talk to the running engine
//	deneb ...                same, when the binary is named or linked "deneb"
//	denebd fake              a fake engine with demo data, for the desktop client
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	args := os.Args[1:]
	name := strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe")
	var err error
	switch {
	case name == "deneb":
		err = runCLI(args)
	case len(args) == 0 || args[0] == "run" || strings.HasPrefix(args[0], "-"):
		if len(args) > 0 && args[0] == "run" {
			args = args[1:]
		}
		err = cmdRun(args)
	case args[0] == "fake":
		err = cmdFake(args[1:])
	case args[0] == "kernel-undo":
		err = cmdKernelUndo(args[1:])
	default:
		err = runCLI(args)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
