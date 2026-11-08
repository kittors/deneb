package socket

import (
	"context"
	"fmt"
	"net"

	winio "github.com/tailscale/go-winio"
	"golang.org/x/sys/windows"
)

// On Windows the endpoint is a named pipe. M1 runs the engine as an ordinary
// process (user space), so system and user paths are the same pipe; M5's
// Windows service will keep this name.
func systemPath() string { return `\\.\pipe\denebd` }
func userPath() string   { return `\\.\pipe\denebd` }

func isPrivileged() bool { return false }

func listen(path string, opts ListenOptions) (net.Listener, error) {
	sid, err := currentUserSID()
	if err != nil {
		return nil, err
	}
	// Protected DACL: only this user and SYSTEM. Everyone else, including
	// other administrators' sessions, is denied by omission.
	sddl := fmt.Sprintf("D:P(A;;GA;;;%s)(A;;GA;;;SY)", sid)
	return winio.ListenPipe(path, &winio.PipeConfig{
		SecurityDescriptor: sddl,
		InputBufferSize:    64 << 10,
		OutputBufferSize:   64 << 10,
	})
}

func dial(ctx context.Context, path string) (net.Conn, error) {
	return winio.DialPipeContext(ctx, path)
}

func currentUserSID() (string, error) {
	tok := windows.GetCurrentProcessToken()
	u, err := tok.GetTokenUser()
	if err != nil {
		return "", fmt.Errorf("read the current user's SID: %w", err)
	}
	return u.User.Sid.String(), nil
}
