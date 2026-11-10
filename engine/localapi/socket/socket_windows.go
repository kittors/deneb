package socket

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"unsafe"

	winio "github.com/tailscale/go-winio"
	"golang.org/x/sys/windows"
)

// On Windows the endpoint is a named pipe. Anyone may create a pipe of any
// name, and a client is served by whoever created it first, so:
//
//   - the Windows service (kernel mode) listens under
//     \\.\pipe\ProtectedPrefix\Administrators\, where only administrators and
//     SYSTEM may create pipes: no one else can take its name first and
//     answer the window in its place;
//   - a user-space engine listens on \\.\pipe\denebd, owned by its user and
//     open to no other, and a client talks to that pipe only once it has
//     checked so (checkOwnPipe).
func systemPath() string { return `\\.\pipe\ProtectedPrefix\Administrators\Deneb\denebd` }
func userPath() string   { return `\\.\pipe\denebd` }

func isPrivileged() bool { return false }

func listen(path string, opts ListenOptions) (net.Listener, error) {
	sid, err := currentUserSID()
	if err != nil {
		return nil, err
	}
	// Protected DACL: only this user and SYSTEM. Everyone else, including
	// other administrators' sessions, is denied by omission. The Windows
	// service (M5) runs as SYSTEM: there administrators may connect too,
	// so an elevated `deneb` works, and so may the desktop's user
	// (AllowUser), so the window works without elevation: an
	// administrator's window runs with the administrators group for deny
	// only.
	// The owner is set, to this user: what a client checks (checkOwnPipe).
	sddl := fmt.Sprintf("O:%sD:P(A;;GA;;;%s)(A;;GA;;;SY)", sid, sid)
	if sid == "S-1-5-18" {
		sddl += "(A;;GA;;;BA)"
	}
	if opts.AllowUser != "" {
		u, err := UserSID(opts.AllowUser)
		if err != nil {
			return nil, err
		}
		if u != sid {
			sddl += "(A;;GA;;;" + u + ")"
		}
	}
	return winio.ListenPipe(path, &winio.PipeConfig{
		SecurityDescriptor: sddl,
		InputBufferSize:    64 << 10,
		OutputBufferSize:   64 << 10,
	})
}

func dial(ctx context.Context, path string) (net.Conn, error) {
	c, err := winio.DialPipeContext(ctx, path)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(path, systemPath()) {
		if err := checkOwnPipe(c); err != nil {
			c.Close()
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}
	return c, nil
}

// checkOwnPipe refuses a pipe someone else could be serving: it must be
// owned by this user (only administrators may set an owner other than
// themselves) and open to no one but this user and SYSTEM, as creating
// another instance of a pipe takes the access its DACL gives. So another
// user who took the name first, waiting for the window, is not talked to.
func checkOwnPipe(c net.Conn) error {
	pc, ok := c.(interface{ Fd() uintptr })
	if !ok {
		return fmt.Errorf("unexpected pipe connection %T", c)
	}
	me, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return err
	}
	sd, err := windows.GetSecurityInfo(windows.Handle(pc.Fd()), windows.SE_KERNEL_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("read the pipe's security: %w", err)
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return err
	}
	if !owner.Equals(me.User.Sid) {
		return fmt.Errorf("%w: the pipe belongs to %v, not to this user", ErrNotOurs, owner)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	if dacl == nil {
		return fmt.Errorf("%w: the pipe is open to everyone", ErrNotOurs)
	}
	for i := range uint32(dacl.AceCount) {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return err
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			continue // a deny lets no one in
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !sid.Equals(me.User.Sid) && !sid.Equals(system) {
			return fmt.Errorf("%w: the pipe is open to %v too", ErrNotOurs, sid)
		}
	}
	return nil
}

var procWaitNamedPipe = windows.NewLazySystemDLL("kernel32.dll").NewProc("WaitNamedPipeW")

// exists: a pipe of that name is being served (WaitNamedPipe, which does
// not connect to it).
func exists(p string) bool {
	name, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return false
	}
	r, _, err := procWaitNamedPipe.Call(uintptr(unsafe.Pointer(name)), 1)
	return r != 0 || errors.Is(err, windows.ERROR_SEM_TIMEOUT) // served, or every instance busy
}

// UserSID is the SID, as a string, of a user given by SID or by account
// name ("PC\jie", or "jie" on this computer).
func UserSID(user string) (string, error) {
	if s, err := windows.StringToSid(user); err == nil {
		return s.String(), nil
	}
	s, _, _, err := windows.LookupSID("", user)
	if err != nil {
		return "", fmt.Errorf("no user %q: %w", user, err)
	}
	return s.String(), nil
}

func currentUserSID() (string, error) {
	tok := windows.GetCurrentProcessToken()
	u, err := tok.GetTokenUser()
	if err != nil {
		return "", fmt.Errorf("read the current user's SID: %w", err)
	}
	return u.User.Sid.String(), nil
}
