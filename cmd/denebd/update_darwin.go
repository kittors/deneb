package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/kittors/deneb/engine/localapi/apitype"
)

// The launchd service replacing itself (design/plan/m7/engine-self-update.md
// §2): beside its executable, only root may write, the new one is copied in
// and checked (denebd.update), the old one kept to go back to (denebd.prev),
// and the note of the update kept (update.json).
var (
	macServiceDir = filepath.Dir(macServiceBin)
	macStaged     = filepath.Join(macServiceDir, "denebd.update")
	macPrev       = filepath.Join(macServiceDir, "denebd.prev")
	macNote       = filepath.Join(macServiceDir, "update.json")
)

// macPaths are the service's executable and what an update keeps beside
// it; tests use their own.
type macPaths struct{ bin, staged, prev, note string }

var macUpdate = macPaths{bin: macServiceBin, staged: macStaged, prev: macPrev, note: macNote}

// isMacService: this process is the launchd service, as root.
func isMacService() bool {
	exe, err := os.Executable()
	return err == nil && os.Geteuid() == 0 && exe == macServiceBin
}

// serviceSelfUpdate is engine.Options.SelfUpdate for the launchd service;
// nil for an engine started any other way.
func serviceSelfUpdate() func(context.Context, string) apitype.UpdateResult {
	if !isMacService() {
		return nil
	}
	return macSelfUpdate
}

var macUpdateMu sync.Mutex

// macSelfUpdate replaces the service's engine with src, the one the window
// ships, once it checked it (stageEngine), and has launchd start it.
func macSelfUpdate(ctx context.Context, src string) apitype.UpdateResult {
	macUpdateMu.Lock()
	defer macUpdateMu.Unlock()
	res := func(result, format string, a ...any) apitype.UpdateResult {
		return apitype.UpdateResult{Result: result, Message: fmt.Sprintf(format, a...)}
	}
	if !filepath.IsAbs(src) || filepath.Base(src) != "denebd" {
		return res(apitype.UpdateFailed, "要 denebd 的完整路径，收到的是 %q", src)
	}
	// Its directory still as install made it: root's, no one else's to write.
	if err := macProtectedDir(macServiceDir); err != nil {
		return res(apitype.UpdateFailed, "%v", err)
	}
	unlock, err := macInstallLock()
	if err != nil {
		return res(apitype.UpdateFailed, "%v", err)
	}
	defer unlock()
	out, ok := macUpdate.put(ctx, src)
	if !ok {
		return out
	}
	log.Printf("update: %s -> %s from %s; stopping for launchd to start it", version, out.Version, src)
	// The answer goes out first; launchd (KeepAlive) starts the new engine.
	go func() {
		time.Sleep(500 * time.Millisecond)
		stopRun()
	}()
	out.Result = apitype.UpdateDone
	return out
}

// put checks src (stageEngine), notes the update and swaps it in: ok when
// the new engine is in place, for the service to stop.
func (p macPaths) put(ctx context.Context, src string) (out apitype.UpdateResult, ok bool) {
	st, out := stageEngine(ctx, src, p.staged, p.bin, version, failedVersion(p.note))
	if st == nil {
		return out, false
	}
	// Noted before the swap: the new engine counts its starts (§2 step 7).
	note := &updateNote{From: version, To: st.version, Prev: p.prev, At: time.Now().UTC()}
	if old := readNote(p.note); old != nil {
		note.Failed = old.Failed
	}
	if err := writeNote(p.note, note); err != nil {
		st.remove()
		return apitype.UpdateResult{Result: apitype.UpdateFailed, Message: fmt.Sprintf("记下更新：%v", err)}, false
	}
	if err := p.swap(st); err != nil {
		st.remove()
		os.Remove(p.note)
		return apitype.UpdateResult{Result: apitype.UpdateFailed, Message: fmt.Sprintf("换 denebd：%v", err)}, false
	}
	return out, true
}

// swap puts the staged engine in (§2 step 8): the running one is kept as
// denebd.prev, a hard link, and the new one renamed over it, so that denebd
// is a whole executable at every moment. Their signatures go along.
func (p macPaths) swap(st *staged) error {
	for _, f := range []string{p.prev, sigPath(p.prev)} {
		if err := os.Remove(f); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.Link(p.bin, p.prev); err != nil {
		return err
	}
	if _, err := os.Lstat(sigPath(p.bin)); err == nil {
		if err := os.Link(sigPath(p.bin), sigPath(p.prev)); err != nil {
			return err
		}
	}
	if err := os.Rename(st.exe, p.bin); err != nil {
		return err
	}
	if _, err := os.Lstat(st.sig); err == nil {
		return os.Rename(st.sig, sigPath(p.bin))
	}
	os.Remove(sigPath(p.bin)) // an unsigned test build: the old one's is not its
	return nil
}

// restore puts prev back in denebd's place (§2 step 10), and its signature
// with it.
func (p macPaths) restore(prev string) error {
	if err := os.Rename(prev, p.bin); err != nil {
		return err
	}
	if _, err := os.Lstat(sigPath(prev)); err == nil {
		return os.Rename(sigPath(prev), sigPath(p.bin))
	}
	os.Remove(sigPath(p.bin))
	return nil
}

// leftovers removes what an update cut off before its swap left.
func (p macPaths) leftovers() {
	os.Remove(p.staged)
	os.Remove(sigPath(p.staged))
}

// starting is updateStartHook's work, on p (startCheck, then putIn once the
// engine ran healthyAfter).
func (p macPaths) starting(after time.Duration) bool {
	if startCheck(p.note, version, p.restore, p.leftovers, log.Printf) {
		return true
	}
	p.leftovers()
	time.AfterFunc(after, func() {
		putIn(p.note, version, func(prev string) {
			os.Remove(prev)
			os.Remove(sigPath(prev))
		})
	})
	return false
}

// macInstallLock takes the lock install and uninstall take: one at a time.
func macInstallLock() (unlock func(), err error) {
	lock, err := os.OpenFile(filepath.Join(filepath.Dir(macServiceState), "install.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, errors.New("另一次 Deneb 安装正在进行")
	}
	return func() { lock.Close() }, nil
}

// updateStartHook runs first in the service's engine (startCheck): true
// when it put the previous engine back, and the service is to stop for
// launchd to start that one. Otherwise, once the engine has run a while,
// the update is done (putIn).
func updateStartHook() bool {
	if !isMacService() {
		return false
	}
	return macUpdate.starting(healthyAfter)
}
