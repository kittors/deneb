package engine

import "github.com/kittors/deneb/engine/tun"

// UndoKernel removes what a kernel-mode engine that was killed left behind
// (the resolver file, the route), from the record in its state directory.
// It refuses while an engine runs on that state: that one still needs them.
func UndoKernel(stateDir string, logf func(string, ...any)) error {
	unlock, err := lockState(stateDir, 0)
	if err != nil {
		return err
	}
	defer unlock()
	tun.CleanupStale(stateDir, logf)
	return nil
}
