package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/kittors/deneb/engine"
	"github.com/kittors/deneb/engine/localapi/apitype"
	"github.com/kittors/deneb/engine/localapi/localclient"
	"github.com/kittors/deneb/engine/localapi/socket"
	"github.com/kittors/deneb/engine/tun"
	"github.com/kittors/deneb/proto"
)

const (
	macServiceLabel = "dev.deneb.engine"
	macServicePlist = "/Library/LaunchDaemons/dev.deneb.engine.plist"
	macServiceBin   = "/Library/PrivilegedHelperTools/Deneb/denebd"
	macServiceState = "/Library/Application Support/Deneb/engine"
)

func isWindowsService() bool    { return false }
func runService([]string) error { return errors.New("not a Windows service") }
func isAdmin() bool             { return os.Geteuid() == 0 }

func systemServiceRunning() bool {
	// A restart briefly has no socket. The installed root-owned job still
	// owns this identity: starting the old user's copy during that gap would
	// make two machines fight over the same keys when launchd starts it.
	if fi, err := os.Lstat(macServicePlist); err == nil && fi.Mode().IsRegular() {
		if st, ok := fi.Sys().(*syscall.Stat_t); ok && st.Uid == 0 && fi.Mode().Perm()&0o022 == 0 {
			return true
		}
	}
	c, err := net.DialTimeout("unix", socket.SystemPath(), 200*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

func serviceSelfUpdate() func(context.Context, string) apitype.UpdateResult {
	exe, err := os.Executable()
	if err != nil || os.Geteuid() != 0 || exe != macServiceBin {
		return nil
	}
	return func(context.Context, string) apitype.UpdateResult {
		return apitype.UpdateResult{Result: apitype.UpdateNeedsInstall, Version: version, Message: "更新系统引擎需要再次通过 macOS 管理员认证"}
	}
}

func preflightService() []string {
	return []string{"安装 macOS 系统服务：程序和设备身份保存在仅管理员可写的系统目录，开机启动；本机接口仅放行当前用户和 root"}
}

// macOS ships utun itself. The missing part is a root-owned launchd job,
// not a downloaded driver. Only this initial install asks for authority;
// launchd never executes code from the user's writable application bundle.
func cmdService(args []string) error {
	if len(args) == 0 {
		return errors.New("用法：denebd service install -allow-uid UID -import-state DIR | uninstall | restart | status")
	}
	if args[0] == "status" {
		b, err := exec.Command("/bin/launchctl", "print", "system/"+macServiceLabel).CombinedOutput()
		fmt.Print(string(b))
		return err
	}
	if !isAdmin() {
		return errors.New("安装或停止 Deneb 系统服务需要管理员权限")
	}
	// Privileged cleanup invokes the OS networking tools, never a binary
	// supplied through the invoking user's PATH.
	if err := os.Setenv("PATH", "/usr/bin:/bin:/usr/sbin:/sbin"); err != nil {
		return err
	}
	switch args[0] {
	case "install":
		fs := flag.NewFlagSet("service install", flag.ContinueOnError)
		uid := fs.Int("allow-uid", -1, "the desktop user's uid")
		from := fs.String("import-state", "", "stopped user engine's state directory")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 0 || *uid <= 0 || *from == "" {
			return errors.New("需要 -allow-uid 和 -import-state，不能使用 root 的设备身份")
		}
		if _, err := user.LookupId(strconv.Itoa(*uid)); err != nil {
			return fmt.Errorf("找不到桌面用户：%w", err)
		}
		return installMacService(*uid, *from)
	case "uninstall":
		if len(args) != 1 {
			return errors.New("用法：denebd service uninstall（保留设备身份）")
		}
		if err := stopMacService(); err != nil {
			return err
		}
		if err := engine.UndoKernel(macServiceState, func(f string, a ...any) { fmt.Printf(f+"\n", a...) }); err != nil {
			return err
		}
		for _, p := range []string{macServicePlist, macServiceBin} {
			if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		fmt.Println("Deneb 系统服务已卸载，设备身份保留在系统目录")
		return nil
	case "restart":
		if len(args) != 1 {
			return errors.New("用法：denebd service restart")
		}
		if err := stopMacService(); err != nil {
			return err
		}
		if err := macLaunchctl("bootstrap", "system", macServicePlist); err != nil {
			return err
		}
		return waitMacService()
	}
	return fmt.Errorf("不认识的 service 命令 %q", args[0])
}

func installMacService(uid int, from string) error {
	for _, dir := range []string{filepath.Dir(macServiceState), macServiceState, filepath.Dir(macServiceBin)} {
		if err := macProtectedDir(dir); err != nil {
			return err
		}
	}
	if err := os.Chmod(macServiceState, 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(filepath.Dir(macServiceState), "install.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.New("另一次 Deneb 安装正在进行")
	}
	if err := stopMacService(); err != nil {
		return err
	}
	if err := importMacState(from, macServiceState, uint32(uid)); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if filepath.Clean(exe) != macServiceBin {
		b, err := os.ReadFile(exe)
		if err != nil {
			return err
		}
		if err := macAtomicFile(macServiceBin, b, 0o755); err != nil {
			return err
		}
	}
	if err := macAtomicFile(macServicePlist, []byte(macServiceDefinition(uid)), 0o644); err != nil {
		return err
	}
	log := filepath.Join(macServiceState, "denebd.log")
	f, err := os.OpenFile(log, os.O_CREATE|os.O_APPEND|os.O_WRONLY|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	f.Close()
	if err := macLaunchctl("enable", "system/"+macServiceLabel); err != nil {
		return err
	}
	if err := macLaunchctl("bootstrap", "system", macServicePlist); err != nil {
		return err
	}
	if err := waitMacService(); err != nil {
		if stopErr := stopMacService(); stopErr != nil {
			return fmt.Errorf("%w；停止失败的服务：%v", err, stopErr)
		}
		return err
	}
	fmt.Println("Deneb 系统网卡已启用，浏览器和其他程序可以直接访问局域网 IP 与名称")
	return nil
}

// All ancestors must stay outside user control. A symlink or a writable
// parent would let a non-administrator replace the code launchd runs.
func macProtectedDir(dir string) error {
	if !filepath.IsAbs(dir) {
		return errors.New("系统服务路径必须是绝对路径")
	}
	p := "/"
	for _, part := range strings.Split(strings.TrimPrefix(filepath.Clean(dir), "/"), "/") {
		p = filepath.Join(p, part)
		fi, err := os.Lstat(p)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(p, 0o755); err != nil {
				return err
			}
			fi, err = os.Lstat(p)
		}
		if err != nil {
			return err
		}
		st, ok := fi.Sys().(*syscall.Stat_t)
		if !ok || !fi.IsDir() || st.Uid != 0 || fi.Mode().Perm()&0o022 != 0 {
			return fmt.Errorf("不安全的系统服务目录 %s：必须是 root 拥有且其他用户不可写的真实目录", p)
		}
	}
	return nil
}

func macAtomicFile(path string, b []byte, mode os.FileMode) error {
	if fi, err := os.Lstat(path); err == nil && !fi.Mode().IsRegular() {
		return fmt.Errorf("拒绝替换非普通文件 %s", path)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".deneb-install-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// Copy data, never a directory tree: logs, symlinks and an undo record from
// a user-writable directory must not become inputs to privileged cleanup.
func importMacState(from, to string, uid uint32) error {
	if fi, err := os.Lstat(filepath.Join(to, "state.json")); err == nil {
		if !fi.Mode().IsRegular() {
			return errors.New("系统设备身份不是普通文件")
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	dir, err := os.OpenFile(from, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer dir.Close()
	fi, err := dir.Stat()
	if err != nil {
		return err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != uid || fi.Mode().Perm()&0o077 != 0 {
		return errors.New("设备身份必须来自当前用户的私有目录")
	}
	fd, err := unix.Openat(int(dir.Fd()), "engine.lock", unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	lock := os.NewFile(uintptr(fd), "engine.lock")
	defer lock.Close()
	lockInfo, err := lock.Stat()
	if err != nil || !lockInfo.Mode().IsRegular() {
		return errors.New("设备身份锁不是普通文件")
	}
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.New("用户态引擎还没停下，不能同时接管同一台设备")
	}
	files := map[string][]byte{}
	for _, name := range []string{"state.json", "netmap.json"} {
		fd, err := unix.Openat(int(dir.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
		if errors.Is(err, syscall.ENOENT) && name == "netmap.json" {
			continue
		}
		if err != nil {
			return err
		}
		f := os.NewFile(uintptr(fd), name)
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() {
			f.Close()
			return fmt.Errorf("设备身份 %s 不是普通文件", name)
		}
		owner, ok := info.Sys().(*syscall.Stat_t)
		if !ok || owner.Uid != uid || info.Mode().Perm()&0o077 != 0 {
			f.Close()
			return fmt.Errorf("设备身份 %s 的拥有者或权限不正确", name)
		}
		b, err := io.ReadAll(io.LimitReader(f, 16<<20+1))
		f.Close()
		if err != nil || len(b) > 16<<20 || !json.Valid(b) {
			return fmt.Errorf("设备身份 %s 无效或过大", name)
		}
		files[name] = b
	}
	if b := files["netmap.json"]; b != nil {
		var nm proto.NetMap
		if err := json.Unmarshal(b, &nm); err != nil {
			return fmt.Errorf("设备网络图无效：%w", err)
		}
		if nm.Network.Domain != proto.Domain {
			return errors.New("设备网络图必须使用 deneb.internal 专用域名")
		}
		if err := tun.CheckBlock(nm.Network.IPv4); err != nil {
			return fmt.Errorf("设备网络图地址段无效：%w", err)
		}
	}
	// state.json is the commit marker: an interrupted copy is retried in full.
	for _, name := range []string{"netmap.json", "state.json"} {
		if b, ok := files[name]; ok {
			if err := macAtomicFile(filepath.Join(to, name), b, 0o600); err != nil {
				return err
			}
		}
	}
	return nil
}

func macServiceDefinition(uid int) string {
	escape := func(s string) string {
		var b strings.Builder
		xml.EscapeText(&b, []byte(s))
		return b.String()
	}
	args := []string{macServiceBin, "run", "-mode", "kernel", "-state", macServiceState, "-socket", socket.SystemPath(), "-allow-uid", strconv.Itoa(uid), "-socks", ""}
	var b strings.Builder
	b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n<plist version=\"1.0\"><dict>\n<key>Label</key><string>" + macServiceLabel + "</string>\n<key>ProgramArguments</key><array>")
	for _, a := range args {
		b.WriteString("<string>" + escape(a) + "</string>")
	}
	b.WriteString("</array>\n<key>RunAtLoad</key><true/><key>KeepAlive</key><true/><key>ThrottleInterval</key><integer>2</integer><key>Umask</key><integer>18</integer>\n<key>EnvironmentVariables</key><dict><key>PATH</key><string>/usr/bin:/bin:/usr/sbin:/sbin</string></dict>\n")
	for _, key := range []string{"StandardOutPath", "StandardErrorPath"} {
		b.WriteString("<key>" + key + "</key><string>" + escape(filepath.Join(macServiceState, "denebd.log")) + "</string>\n")
	}
	b.WriteString("</dict></plist>\n")
	return b.String()
}

func macLaunchctl(args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	b, err := exec.CommandContext(ctx, "/bin/launchctl", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl %s：%w：%s", strings.Join(args, " "), err, strings.TrimSpace(string(b)))
	}
	return nil
}

func stopMacService() error {
	if exec.Command("/bin/launchctl", "print", "system/"+macServiceLabel).Run() != nil {
		return nil
	}
	// A closed socket does not mean launchd has removed the job yet.
	// bootstrap of the same label during that gap fails with EIO. Wait for
	// launchd's completion as well as the engine, instead of a fixed delay.
	if err := macLaunchctl("bootout", "--wait", "system/"+macServiceLabel); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c := localclient.New(socket.SystemPath())
	for {
		if _, err := c.Version(ctx); errors.Is(err, localclient.ErrNotRunning) {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New("旧 Deneb 系统引擎没有退出")
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func waitMacService() error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c := localclient.New(socket.SystemPath())
	for {
		if s, err := c.State(ctx); err == nil {
			if s.Mode != apitype.ModeKernel {
				return errors.New("系统引擎没有启用网卡，请查看系统目录中的 denebd.log")
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New("Deneb 系统服务未能启动，请查看系统目录中的 denebd.log")
		case <-time.After(100 * time.Millisecond):
		}
	}
}
