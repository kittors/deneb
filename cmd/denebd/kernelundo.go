package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"os"
	"runtime"
	"time"

	"github.com/kittors/deneb/engine"
	"github.com/kittors/deneb/engine/localapi/socket"
)

// cmdKernelUndo is `sudo denebd kernel-undo [-state dir]`: it removes what a
// kernel-mode engine that did not stop cleanly left behind, from the record
// in its state directory, and a socket file nobody answers on. An engine
// still running on that state is left alone.
func cmdKernelUndo(args []string) error {
	fs := flag.NewFlagSet("kernel-undo", flag.ExitOnError)
	stateDir := fs.String("state", defaultStateDir(), "state directory the kernel-mode engine ran on")
	fs.Parse(args)
	if runtime.GOOS != "darwin" {
		return errors.New("kernel mode exists on macOS only")
	}
	if os.Geteuid() != 0 {
		return errors.New("kernel-undo needs root: sudo denebd kernel-undo -state <dir>")
	}
	log.SetFlags(0)
	if err := engine.UndoKernel(*stateDir, log.Printf); err != nil {
		return err
	}
	if p := socket.SystemPath(); socket.Exists(p) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		c, err := socket.Dial(ctx, p)
		cancel()
		if err == nil {
			c.Close()
			log.Printf("an engine still answers on %s; leaving it", p)
		} else if os.Remove(p) == nil {
			log.Printf("removed %s, which nobody answered on", p)
		}
	}
	log.Printf("kernel-undo: done for %s", *stateDir)
	return nil
}
