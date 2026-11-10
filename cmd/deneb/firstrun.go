package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/kittors/deneb/engine/localapi/localclient"
)

// The first time's steps in the real window (M6, DESIGN §7.3): whether they
// were done on this computer, and the network adapter where the window puts
// it in.

// onboardedState reports whether the first time's steps were done on this
// computer (or put off), and the way to keep that they were: a file beside
// the canvas's places.
func onboardedState() (done bool, save func()) {
	places := placesFile()
	if places == "" {
		return false, nil
	}
	path := filepath.Join(filepath.Dir(places), "onboarded")
	_, err := os.Stat(path)
	return err == nil, func() { os.WriteFile(path, []byte(time.Now().Format(time.RFC3339)+"\n"), 0o600) }
}

// adapterInstaller puts the network adapter in where the window does
// (Windows): the engine the window started stops, so that it lets go of
// this computer's identity, and the engine the package ships becomes the
// system service, as an administrator: Windows asks the person once
// (design/plan/m7/engine-security.md, the window's part). keepEngine
// starts no engine of the window's meanwhile; answered no, or failed, it
// starts it again. nil where there is no such step: macOS (waits for a
// developer account), Linux (the package puts it in), a development build.
func adapterInstaller(lc *localclient.Client) func(context.Context) error {
	if runtime.GOOS != "windows" {
		return nil
	}
	bin := bundledEngine()
	if bin == "" {
		return nil
	}
	return func(ctx context.Context) error {
		installing.Store(true)
		defer installing.Store(false)
		if err := lc.Shutdown(ctx); err != nil && !errors.Is(err, localclient.ErrNotRunning) {
			return err
		}
		// It lets go of the identity as it goes: wait for that, 10 s at most.
		for i := 0; i < 50; i++ {
			if _, err := lc.Version(ctx); errors.Is(err, localclient.ErrNotRunning) {
				break
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(200 * time.Millisecond):
			}
		}
		return installService(ctx, bin)
	}
}
