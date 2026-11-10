package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// errDeclined is installService's error when the person said no to
// Windows's question.
var errDeclined = errors.New("not allowed as an administrator")

// installService installs the engine at exe as the Windows service, or
// installs it again over the one there (M6 i2, as M7 changed it):
// `denebd.exe service install` copies it, with the wintun.dll beside it, to
// %ProgramFiles%\Deneb and starts it there, stopping the one installed
// before. It runs as an administrator, so Windows asks the person once
// (UAC). The service takes this computer's identity from the engine the
// window ran (-import-state) and keeps it in %ProgramData%\Deneb from then
// on, not in the person's folders, which any program of theirs could point
// elsewhere; when the service has one already, the engine skips that. The
// engine the window ran must have stopped before a first install; on an
// update the running engine is the service, which the install stops itself.
func installService(ctx context.Context, exe string) error {
	state := filepath.Join(os.Getenv("LOCALAPPDATA"), "Deneb")
	code, err := runElevated(ctx, exe, fmt.Sprintf(`service install -import-state "%s"`, state))
	if err != nil {
		return err
	}
	if code != 0 {
		// The engine says why in the registry; the exit code is all runas gives.
		return fmt.Errorf("exit status %d: %s", code, lastInstallNote())
	}
	return nil
}

// lastInstallNote returns what the engine noted of its last `service
// install`: HKLM\SOFTWARE\Deneb, value ServiceInstall, "<RFC 3339 time> ok"
// or "<time> <why>".
func lastInstallNote() string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Deneb`, registry.QUERY_VALUE)
	if err != nil {
		return "nothing noted"
	}
	defer k.Close()
	v, _, err := k.GetStringValue("ServiceInstall")
	if err != nil {
		return "nothing noted"
	}
	return v
}

// shellExecuteInfo is SHELLEXECUTEINFOW.
type shellExecuteInfo struct {
	size        uint32
	mask        uint32
	hwnd        windows.Handle
	verb        *uint16
	file        *uint16
	parameters  *uint16
	directory   *uint16
	show        int32
	instApp     windows.Handle
	idList      uintptr
	class       *uint16
	keyClass    windows.Handle
	hotKey      uint32
	iconMonitor windows.Handle
	process     windows.Handle
}

const seeMaskNoCloseProcess = 0x40

var procShellExecuteExW = windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteExW")

// runElevated runs exe with params as an administrator (runas), hidden,
// waits for it and returns its exit code; errDeclined when the person
// said no.
func runElevated(ctx context.Context, exe, params string) (uint32, error) {
	// The shell wants COM on the thread that calls it.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED|windows.COINIT_DISABLE_OLE1DDE); err == nil {
		defer windows.CoUninitialize()
	}
	verb, err := windows.UTF16PtrFromString("runas")
	if err != nil {
		return 0, err
	}
	file, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return 0, err
	}
	args, err := windows.UTF16PtrFromString(params)
	if err != nil {
		return 0, err
	}
	info := shellExecuteInfo{mask: seeMaskNoCloseProcess, verb: verb, file: file, parameters: args, show: windows.SW_HIDE}
	info.size = uint32(unsafe.Sizeof(info))
	if ok, _, err := procShellExecuteExW.Call(uintptr(unsafe.Pointer(&info))); ok == 0 {
		if errors.Is(err, windows.ERROR_CANCELLED) {
			return 0, errDeclined
		}
		return 0, fmt.Errorf("runas %s: %w", filepath.Base(exe), err)
	}
	if info.process == 0 {
		return 0, fmt.Errorf("runas %s: no process", filepath.Base(exe))
	}
	defer windows.CloseHandle(info.process)
	for {
		ev, err := windows.WaitForSingleObject(info.process, 500)
		if ev == windows.WAIT_OBJECT_0 {
			break
		}
		if ev != uint32(windows.WAIT_TIMEOUT) {
			return 0, fmt.Errorf("waiting for %s: %v", filepath.Base(exe), err)
		}
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
	}
	var code uint32
	if err := windows.GetExitCodeProcess(info.process, &code); err != nil {
		return 0, err
	}
	return code, nil
}
