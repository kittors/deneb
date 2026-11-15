package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"net/netip"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/kittors/deneb/proto"
)

// host is the machine install, uninstall, status and update work on.
// Every path goes through path() and every command through run(): a test
// points root at a temporary directory and records the commands, so a
// whole install can be played on any machine, touching none of its
// services, firewall or users.
type host struct {
	root string
	// run runs a command and returns what it printed, stdout and stderr.
	run func(name string, args ...string) (string, error)
	// runIn is run with input on the command's stdin (crontab -).
	runIn func(input, name string, args ...string) (string, error)
	// has: the command is installed.
	has func(name string) bool
	// spawn starts a relay that outlives install: detached, as user ("" is
	// the current one). For a machine with cron or nothing to start it.
	spawn func(user, name string, args ...string) error
	// kill asks a process to stop (SIGTERM).
	kill  func(pid int) error
	chown func(path string, uid, gid int) error

	// What the machine itself answers, which a test makes up.
	freeSpace func(dir string) (uint64, bool)
	tcpFree   func(port int) bool
	udpFree   func(port int) bool
	// addrs are the public addresses on the interfaces, and all of them
	// (findAddrs).
	addrs func() (v4, v6 netip.Addr, local []netip.Addr)
	// progVersion runs the program at path with `version`: "" when it
	// does not run.
	progVersion func(path string) string
	// answers: the relay answers on port, on this machine; waitFor is how
	// long install waits for it to.
	answers func(port int) bool
	waitFor time.Duration
	// check and leave are the control server's calls (checkFromOutside,
	// leave), which a test answers itself.
	check   func(context.Context, *controlClient) (*proto.RelayStatus, error)
	current func(context.Context, *controlClient) (*proto.RelayStatus, error)
	leave   func(context.Context, *controlClient) error

	uid      int
	userName string
	home     string
	getenv   func(string) string
	out      io.Writer
	// tty is the terminal to ask on. The program's stdin is the install
	// script coming down curl, so questions go to /dev/tty, never stdin.
	tty func() (io.ReadWriteCloser, error)
}

// realHost is this machine.
func realHost() *host {
	h := &host{
		run: func(name string, args ...string) (string, error) {
			out, err := exec.Command(name, args...).CombinedOutput()
			return string(out), err
		},
		runIn: func(input, name string, args ...string) (string, error) {
			c := exec.Command(name, args...)
			c.Stdin = strings.NewReader(input)
			out, err := c.CombinedOutput()
			return string(out), err
		},
		has: func(name string) bool {
			_, err := exec.LookPath(name)
			return err == nil
		},
		spawn:       spawnDetached,
		kill:        killProcess,
		chown:       os.Lchown,
		freeSpace:   freeSpace,
		tcpFree:     tcpFree,
		udpFree:     udpFree,
		addrs:       findAddrs,
		progVersion: func(p string) string { return installedVersion(context.Background(), p) },
		answers:     answersLocally,
		waitFor:     20 * time.Second,
		check:       checkFromOutside,
		current:     currentStatus,
		leave:       leave,
		uid:         os.Getuid(),
		getenv:      os.Getenv,
		out:         os.Stdout,
		tty: func() (io.ReadWriteCloser, error) {
			return os.OpenFile("/dev/tty", os.O_RDWR, 0)
		},
	}
	if u, err := user.Current(); err == nil {
		h.userName, h.home = u.Username, u.HomeDir
	}
	if home := os.Getenv("HOME"); home != "" && h.uid != 0 {
		h.home = home
	}
	// cron and su run with a short PATH; systemctl and friends live in
	// the sbin directories, which a user's PATH may lack.
	path := os.Getenv("PATH")
	for _, d := range []string{"/usr/local/sbin", "/usr/local/bin", "/usr/sbin", "/usr/bin", "/sbin", "/bin"} {
		if !strings.Contains(":"+path+":", ":"+d+":") {
			path += ":" + d
		}
	}
	os.Setenv("PATH", strings.TrimPrefix(path, ":"))
	return h
}

func (h *host) isRoot() bool { return h.uid == 0 }

// path is p on this host: under root in a test.
func (h *host) path(p string) string {
	if h.root == "" {
		return p
	}
	return filepath.Join(h.root, p)
}

func (h *host) exists(p string) bool {
	_, err := os.Lstat(h.path(p))
	return err == nil
}

// read is the file's text, or "" if it cannot be read.
func (h *host) read(p string) string {
	b, err := os.ReadFile(h.path(p))
	if err != nil {
		return ""
	}
	return string(b)
}

// write writes a file whole, its directories made as needed.
func (h *host) write(p string, b []byte, mode fs.FileMode) error {
	full := h.path(p)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(full), "."+filepath.Base(full)+".tmp")
	if err := os.WriteFile(tmp, b, mode); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil { // past the umask
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, full)
}

func (h *host) remove(p string) error {
	err := os.RemoveAll(h.path(p))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// chownTree gives p, and everything in it, to uid:gid.
func (h *host) chownTree(p string, uid, gid int) error {
	return filepath.WalkDir(h.path(p), func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return h.chown(path, uid, gid)
	})
}

// account is a line of /etc/passwd.
type account struct {
	name     string
	uid, gid int
	home     string
}

// lookupUser finds a user in /etc/passwd: install reads the file rather
// than ask the C library, which a static program does not have (and a
// test's root has its own).
func (h *host) lookupUser(name string) (account, bool) {
	sc := bufio.NewScanner(strings.NewReader(h.read("/etc/passwd")))
	for sc.Scan() {
		f := strings.Split(sc.Text(), ":")
		if len(f) < 7 || f[0] != name {
			continue
		}
		uid, err1 := strconv.Atoi(f[2])
		gid, err2 := strconv.Atoi(f[3])
		if err1 != nil || err2 != nil {
			return account{}, false
		}
		return account{name: f[0], uid: uid, gid: gid, home: f[5]}, true
	}
	return account{}, false
}

// procComms are the names of the running processes (/proc/<pid>/comm).
func (h *host) procComms() map[string]bool {
	out := map[string]bool{}
	ents, err := os.ReadDir(h.path("/proc"))
	if err != nil {
		return out
	}
	for _, e := range ents {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		if b, err := os.ReadFile(filepath.Join(h.path("/proc"), e.Name(), "comm")); err == nil {
			out[strings.TrimSpace(string(b))] = true
		}
	}
	return out
}

// osName is the system as /etc/os-release names it: "Debian GNU/Linux 12
// (bookworm)", "Alpine Linux v3.20", "OpenWrt 23.05.3".
func (h *host) osName() string {
	for _, p := range []string{"/etc/os-release", "/usr/lib/os-release"} {
		sc := bufio.NewScanner(strings.NewReader(h.read(p)))
		for sc.Scan() {
			if v, ok := strings.CutPrefix(sc.Text(), "PRETTY_NAME="); ok {
				return strings.Trim(v, `"'`)
			}
		}
	}
	return ""
}

// arch is the CPU this program is built for, as the downloads name it:
// amd64, arm64, armv7.
func arch() string {
	if runtime.GOARCH == "arm" {
		if v := buildSetting("GOARM"); v != "" {
			return "armv" + strings.TrimSuffix(v, ",softfloat")
		}
		return "armv7"
	}
	return runtime.GOARCH
}

// platform is the downloads' name for this build: linux-arm64.
func platform() string { return runtime.GOOS + "-" + arch() }

// shellQuote quotes s for sh, if it needs it.
func shellQuote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./:=,@%+", r))
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// shellJoin is a command line for sh.
func shellJoin(args []string) string {
	var b bytes.Buffer
	for i, a := range args {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(shellQuote(a))
	}
	return b.String()
}
