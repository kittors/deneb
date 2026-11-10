package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"

	"github.com/kittors/deneb/engine"
	"github.com/kittors/deneb/engine/localapi/socket"
)

// serviceName is the Windows service: `sc query Deneb`.
const serviceName = "Deneb"

// isWindowsService: the service manager started this process.
func isWindowsService() bool {
	ok, err := svc.IsWindowsService()
	return err == nil && ok
}

// runService runs the engine under the service manager: the command line
// it was installed with, until the service is stopped. The service has no
// console: its log, crashes included, goes to denebd.log in its state
// directory, the previous run's to denebd.log.1 once it passes 10 MB.
func runService(args []string) error {
	if self, err := os.Executable(); err == nil {
		removeOld(filepath.Dir(self)) // what the last update replaced
	}
	state, _ := splitState(args, defaultServiceState())
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	if f, err := openServiceLog(filepath.Join(state, "denebd.log")); err == nil {
		log.SetOutput(f)
		os.Stdout, os.Stderr = f, f
		debug.SetCrashOutput(f, debug.CrashOptions{})
	}
	log.Printf("[service] process started") // with "stopped", times a restart's parts
	return svc.Run(serviceName, &service{args: args})
}

func defaultServiceState() string { return filepath.Join(os.Getenv("ProgramData"), "Deneb") }

func openServiceLog(path string) (*os.File, error) {
	if err := mkPrivateDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	if fi, err := os.Stat(path); err == nil && fi.Size() > 10<<20 {
		os.Rename(path, path+".1")
	}
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
}

// mkPrivateDir creates dir, if it isn't there, for SYSTEM and
// administrators only: it holds the device's keys, and what's under
// ProgramData is readable by every user unless said otherwise. A directory
// that's already there is left as it is.
func mkPrivateDir(dir string) error {
	if _, err := os.Stat(dir); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
	if err != nil {
		return err
	}
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return err
	}
	sa := &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	if err := windows.CreateDirectory(p, sa); err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	return nil
}

// sessionUser is the user logged in to the session this process runs in,
// who ran the installer even when an administrator's password elevated
// it, else the user at the console: PC\name and SID. An ssh session has
// none (it runs in session 0, with the services).
func sessionUser() (name, sid string, err error) {
	var own uint32
	if err := windows.ProcessIdToSessionId(windows.GetCurrentProcessId(), &own); err != nil {
		return "", "", err
	}
	for _, sess := range []uint32{own, windows.WTSGetActiveConsoleSessionId()} {
		if sess == 0 || sess == 0xFFFFFFFF { // services' session; no console session
			continue
		}
		user, err := wtsString(sess, wtsUserName)
		if err != nil || user == "" {
			continue
		}
		if domain, err := wtsString(sess, wtsDomainName); err == nil && domain != "" {
			user = domain + `\` + user
		}
		if sid, err := socket.UserSID(user); err == nil {
			return user, sid, nil
		}
	}
	return "", "", errors.New("no one is logged in to this computer's desktop")
}

var procWTSQuerySessionInformation = windows.NewLazySystemDLL("wtsapi32.dll").NewProc("WTSQuerySessionInformationW")

// WTS_INFO_CLASS values.
const (
	wtsUserName   = 5
	wtsDomainName = 7
)

func wtsString(session, class uint32) (string, error) {
	var buf *uint16
	var n uint32
	r, _, err := procWTSQuerySessionInformation.Call(0, uintptr(session), uintptr(class), uintptr(unsafe.Pointer(&buf)), uintptr(unsafe.Pointer(&n)))
	if r == 0 {
		return "", err
	}
	defer windows.WTSFreeMemory(uintptr(unsafe.Pointer(buf)))
	return windows.UTF16PtrToString(buf), nil
}

type service struct{ args []string }

func (s *service) Execute(_ []string, req <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	done := make(chan error, 1)
	go func() { done <- cmdRun(s.args) }()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case err := <-done: // stopped on its own: an error, or asked through the local API
			if err != nil {
				log.Printf("denebd: %v", err)
				return false, 1 // the recovery actions restart it
			}
			return false, 0
		case c := <-req:
			switch c.Cmd {
			case svc.Interrogate:
				status <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				stopRun()
				<-done
				return false, 0
			}
		}
	}
}

// cmdService is `denebd service install [run flags]`, `denebd service
// uninstall [-purge]` and `denebd service restart`, as administrator.
//
// install copies denebd.exe and wintun.dll from where it runs (the app's
// bin folder) into %ProgramFiles%\Deneb, which only administrators can
// write, and runs the engine from there as a service: kernel mode, as
// LocalSystem, started with Windows, restarted 2 s after a crash. Its
// state is %ProgramData%\Deneb unless -state says otherwise (another
// engine's state directory keeps that device's identity). Run again, it
// updates what it installed. The window runs it, elevated, in the first
// run's "install the adapter" step (M6 i2).
//
// uninstall stops and deletes the service (the engine takes its adapter,
// routes and NRPT rule down as it stops; whatever a crash left is undone
// from the state directory's record) and removes %ProgramFiles%\Deneb.
// The state, the device's identity, stays unless -purge. With no service
// installed it does nothing: the app's uninstaller runs it either way.
//
// restart stops the service and starts it again: an update (selfUpdate)
// runs it, from the new executable.
func cmdService(args []string) error {
	if len(args) == 0 || args[0] != "install" && args[0] != "uninstall" && args[0] != "restart" {
		return errors.New("用法：denebd service install [denebd run 的参数] | uninstall [-purge] | restart")
	}
	if !isAdmin() {
		return errors.New("要以管理员身份运行")
	}
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	switch args[0] {
	case "install":
		// The window runs this elevated (runas) and sees only the exit
		// code: the outcome also goes to the state directory's log, which
		// the window can read.
		state, err := installService(m, args[1:])
		noteInstall(err)
		if state != "" { // the service's own: only administrators write there
			if err != nil {
				noteServiceLog(state, "service install failed: %v", err)
			} else {
				noteServiceLog(state, "service install: done")
			}
		}
		return err
	case "uninstall":
		return uninstallService(m, len(args) > 1 && args[1] == "-purge")
	case "restart":
		time.Sleep(300 * time.Millisecond) // the update's answer goes out first
		s, err := m.OpenService(serviceName)
		if err != nil {
			return err
		}
		defer s.Close()
		stopService(s)
		return s.Start()
	}
	return nil
}

// installService is `service install`, args being run's flags; it says
// the state directory it chose once it knows it.
func installService(m *mgr.Mgr, args []string) (state string, err error) {
	state, rest := splitState(args, defaultServiceState())
	if state, err = filepath.Abs(state); err != nil { // the service starts in System32
		return "", err
	}
	// The service runs as SYSTEM: its state must be where only
	// administrators can write. In a user's folder, any program of that
	// user could point the files SYSTEM writes elsewhere (a junction) and
	// have it write theirs (M7 review). The identity comes over instead.
	if under, err := underUserProfiles(state); err != nil || under {
		if err == nil {
			err = fmt.Errorf("状态目录 %s 在用户的文件夹里，系统服务不能用：用默认的 %s，设备身份用 -import-state 带过来", state, defaultServiceState())
		}
		return "", err
	}
	imp, rest := splitFlag(rest, "import-state", "")
	if !hasFlag(rest, "allow-user") {
		// The window runs as the desktop's user, without elevation.
		if name, sid, err := sessionUser(); err == nil {
			rest = append(rest, "-allow-user", sid)
			fmt.Printf("本机接口放行 %s：装服务时登录这个桌面的用户\n", name)
		} else {
			fmt.Printf("没找到登录桌面的用户（%v）：本机接口只放行管理员，放行谁用 -allow-user 指定\n", err)
		}
	}
	run := append([]string{"-mode", "kernel", "-state", state}, rest...)
	if err := checkRunFlags(run); err != nil {
		return state, err
	}
	if err := mkPrivateDir(state); err != nil {
		return state, err
	}
	if imp != "" {
		if err := importState(imp, state); err != nil {
			return state, err
		}
	}
	src, err := os.Executable()
	if err != nil {
		return state, err
	}
	dir := installDir()
	exe := filepath.Join(dir, "denebd.exe")
	// Copied in beside the running engine first, and swapped in once it
	// stopped: the copying, and Defender's look at a new executable, happen
	// while the old engine still serves (the network is down from the stop
	// to the start).
	staged := map[string]string{} // in place -> staged
	if !strings.EqualFold(filepath.Dir(src), dir) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return state, err
		}
		for _, f := range []string{"denebd.exe", "wintun.dll"} {
			tmp := filepath.Join(dir, strings.TrimSuffix(f, filepath.Ext(f))+".update"+filepath.Ext(f))
			if err := copyFile(filepath.Join(filepath.Dir(src), f), tmp); err != nil {
				os.Remove(tmp)
				return state, fmt.Errorf("复制 %s 到 %s：%w", f, dir, err)
			}
			staged[filepath.Join(dir, f)] = tmp
		}
	}
	s, err := m.OpenService(serviceName)
	if err == nil { // installed before: stopped, so its files can be replaced
		defer s.Close()
		stopService(s)
	}
	for final, tmp := range staged {
		if err := swapIn(tmp, final); err != nil {
			return state, fmt.Errorf("换上 %s：%w", final, err)
		}
	}
	removeOld(dir)
	run = append([]string{"run"}, run...)
	cfg := mgr.Config{
		DisplayName: "Deneb",
		Description: "Deneb 组网引擎：Deneb 网卡、到各对端的路由、deneb.internal 的 NRPT 规则",
		StartType:   mgr.StartAutomatic,
	}
	if s == nil {
		if s, err = m.CreateService(serviceName, exe, cfg, run...); err != nil {
			return state, err
		}
		defer s.Close()
	} else {
		old, err := s.Config()
		if err != nil {
			return state, err
		}
		old.DisplayName, old.Description, old.StartType = cfg.DisplayName, cfg.Description, cfg.StartType
		old.BinaryPathName = windows.ComposeCommandLine(append([]string{exe}, run...))
		if err := s.UpdateConfig(old); err != nil {
			return state, err
		}
	}
	s.SetRecoveryActions([]mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 2 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 2 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 10 * time.Second},
	}, 600)
	// Also when the engine stops with an error rather than crashing.
	s.SetRecoveryActionsOnNonCrashFailures(true)
	if err := s.Start(); err != nil {
		return state, fmt.Errorf("装好了，起不来：%w", err)
	}
	for i := 0; i < 50; i++ { // up to 10 s: it fails at once if the adapter can't be made
		time.Sleep(200 * time.Millisecond)
		st, err := s.Query()
		if err != nil {
			return state, err
		}
		switch st.State {
		case svc.Running:
			fmt.Printf("已装好、启动 %s 服务：%s %v\n", serviceName, exe, run)
			return state, nil
		case svc.Stopped:
			return state, fmt.Errorf("装好了，起来就停了：看 %s", filepath.Join(state, "denebd.log"))
		}
	}
	return state, fmt.Errorf("装好了，10 s 还没起来：看 %s", filepath.Join(state, "denebd.log"))
}

// installDir is where the service's engine is: only administrators can
// write there, as an executable a SYSTEM service runs must be. The app's
// own folder (%LOCALAPPDATA%\Programs\Deneb) is the user's.
func installDir() string {
	pf := os.Getenv("ProgramFiles")
	if pf == "" {
		pf = `C:\Program Files`
	}
	return filepath.Join(pf, "Deneb")
}

// stopService stops s and waits up to 10 s for it, looking often: the
// network is down from the stop to the next start.
func stopService(s *mgr.Service) {
	st, err := s.Control(svc.Stop)
	if err != nil {
		return // not running
	}
	for i := 0; i < 200 && st.State != svc.Stopped; i++ {
		time.Sleep(50 * time.Millisecond)
		if st, err = s.Query(); err != nil {
			return
		}
	}
}

// underUserProfiles: dir is in C:\Users (the users' profiles folder).
func underUserProfiles(dir string) (bool, error) {
	users, err := windows.KnownFolderPath(windows.FOLDERID_UserProfiles, 0)
	if err != nil {
		return false, err
	}
	rel, err := filepath.Rel(strings.ToLower(users), strings.ToLower(dir))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, `..\`), nil
}

// importState brings a device's identity, its keys and last network map,
// from the user-space engine's state directory into the service's, when
// the service has none yet: the service is then the same device. The
// user-space engine must have stopped (denebd shutdown): two engines of
// one identity would fight over it.
func importState(from, to string) error {
	if _, err := os.Stat(filepath.Join(to, "state.json")); err == nil {
		return nil // the service's own already
	}
	if stateInUse(from) {
		return fmt.Errorf("用户态引擎还在用 %s：先停下它（denebd shutdown）", from)
	}
	for _, f := range []string{"state.json", "netmap.json"} {
		b, err := os.ReadFile(filepath.Join(from, f))
		if f == "netmap.json" && errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("带过设备身份：%w", err)
		}
		if err := os.WriteFile(filepath.Join(to, f), b, 0o600); err != nil {
			return err
		}
	}
	fmt.Printf("设备身份从 %s 带了过来\n", from)
	return nil
}

// stateInUse: an engine holds the state directory's lock (engine.lock,
// opened with no sharing while it runs).
func stateInUse(dir string) bool {
	p, err := windows.UTF16PtrFromString(filepath.Join(dir, "engine.lock"))
	if err != nil {
		return false
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return errors.Is(err, windows.ERROR_SHARING_VIOLATION)
	}
	windows.CloseHandle(h)
	return false
}

// noteInstall records how the last `service install` went where the
// window, which ran it elevated and sees only its exit code, can read it:
// HKLM\SOFTWARE\Deneb, value ServiceInstall, "<time> ok" or "<time> <why>".
func noteInstall(err error) {
	k, _, e := registry.CreateKey(registry.LOCAL_MACHINE, `SOFTWARE\Deneb`, registry.SET_VALUE)
	if e != nil {
		return
	}
	defer k.Close()
	v := "ok"
	if err != nil {
		v = err.Error()
	}
	k.SetStringValue("ServiceInstall", time.Now().Format(time.RFC3339)+" "+v)
}

// noteServiceLog adds a line to the log in the state directory.
func noteServiceLog(state, format string, a ...any) {
	f, err := os.OpenFile(filepath.Join(state, "denebd.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s [service] %s\n", time.Now().Format("2006/01/02 15:04:05.000000"), fmt.Sprintf(format, a...))
}

func uninstallService(m *mgr.Mgr, purge bool) error {
	state := ""
	if s, err := m.OpenService(serviceName); err == nil {
		if cfg, err := s.Config(); err == nil {
			if args, err := windows.DecomposeCommandLine(cfg.BinaryPathName); err == nil && len(args) > 1 {
				state, _ = splitState(args[1:], defaultServiceState())
			}
		}
		stopService(s)
		err := s.Delete()
		s.Close()
		if err != nil {
			return err
		}
		fmt.Printf("已删除 %s 服务\n", serviceName)
		if state != "" {
			noteServiceLog(state, "service uninstall: the service is deleted")
		}
	}
	if state != "" {
		// What a crash left, if anything: the engine undoes its own on stop.
		if err := engine.UndoKernel(state, log.Printf); err != nil {
			fmt.Printf("撤销残留：%v\n", err)
		}
	}
	dir := installDir()
	self, _ := os.Executable()
	for _, f := range []string{"denebd.exe", "wintun.dll"} {
		removeFile(filepath.Join(dir, f), self)
	}
	removeOld(dir)
	os.Remove(dir) // only if empty
	registry.DeleteKey(registry.LOCAL_MACHINE, `SOFTWARE\Deneb`)
	if purge && state != "" {
		if err := os.RemoveAll(state); err != nil {
			return err
		}
		fmt.Printf("已删除状态 %s\n", state)
	}
	return nil
}

// removeFile removes path, or, when it is this running executable, has
// Windows remove it at the next restart.
func removeFile(path, self string) {
	if err := os.Remove(path); err == nil || errors.Is(err, os.ErrNotExist) {
		return
	}
	if strings.EqualFold(path, self) {
		if p, err := windows.UTF16PtrFromString(path); err == nil {
			windows.MoveFileEx(p, nil, windows.MOVEFILE_DELAY_UNTIL_REBOOT)
		}
	}
}

func isAdmin() bool { return windows.GetCurrentProcessToken().IsElevated() }

// systemServiceRunning: the Deneb service is serving its local API.
func systemServiceRunning() bool { return socket.Exists(socket.SystemPath()) }
