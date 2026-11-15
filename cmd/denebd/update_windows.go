//go:build windows

package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/kittors/deneb/engine/localapi/apitype"
)

// serviceSelfUpdate is engine.Options.SelfUpdate when this process is the
// Windows service; nil for an engine started any other way.
func serviceSelfUpdate() func(context.Context, string) apitype.UpdateResult {
	if !isWindowsService() {
		return nil
	}
	return selfUpdate
}

var updateMu sync.Mutex

// updateStartHook: the Windows service checks its update in runService,
// before the service manager hears from it.
func updateStartHook() bool { return false }

// selfUpdate replaces the service's engine with the denebd.exe at src, the
// one the window ships (M6 i2; the window asks at every start when the
// versions differ). The file is read whole, only a plain file on this
// computer's own drives, never a share (readPlain), checked in memory, and
// only then written beside the service's own, where only administrators can
// write, so it cannot change between the check and the swap (stageEngine):
// signed with the key of Deneb's updates, its denebd.exe.sig beside it, and
// newer
// (design/plan/m7/engine-self-update.md §2, §3). An unsigned one, a
// development build, answers UpdateNeedsInstall: the window installs the
// service again, elevated; to a service that came signed, it answers
// UpdateBadSignature. A test build, unsigned, takes another unsigned one,
// to try the rest.
//
// Then it is swapped in (a running executable can be renamed, not
// overwritten), with the wintun.dll beside src when that one differs and
// WireGuard signed it, and a short-lived `denebd service restart` stops
// the service and starts it on the new engine. The note of the update
// (update.json) has the new engine count its starts, and put the old one
// back when it does not get going (runService).
func selfUpdate(ctx context.Context, src string) apitype.UpdateResult {
	updateMu.Lock()
	defer updateMu.Unlock()
	res := func(result, format string, a ...any) apitype.UpdateResult {
		return apitype.UpdateResult{Result: result, Message: fmt.Sprintf(format, a...)}
	}
	if !filepath.IsAbs(src) || !strings.EqualFold(filepath.Base(src), "denebd.exe") {
		return res(apitype.UpdateFailed, "要 denebd.exe 的完整路径，收到的是 %q", src)
	}
	// On this computer's own drives only: reading a share, the service
	// would sign in there as this computer (plainfile.go).
	if err := localDrivePath(src); err != nil {
		return res(apitype.UpdateBadSignature, "%v", err)
	}
	self, err := os.Executable()
	if err != nil {
		return res(apitype.UpdateFailed, "%v", err)
	}
	dir := filepath.Dir(self)
	note := filepath.Join(dir, "update.json")
	// Run below to ask its version: keeps .exe.
	st, out := stageEngine(ctx, src, filepath.Join(dir, "denebd.update.exe"), self, version, failedVersion(note))
	if st == nil {
		return out
	}
	var keep bool
	var dllStaged string
	defer func() {
		if !keep {
			st.remove()
			if dllStaged != "" {
				os.Remove(dllStaged)
			}
		}
	}()
	// The wintun.dll beside src, read as src was. Windows checks its
	// signature on a file: it is written where only SYSTEM and
	// administrators may open it, and checked there.
	dll := filepath.Join(dir, "wintun.dll")
	srcDLL := filepath.Join(filepath.Dir(src), "wintun.dll")
	switch body, err := readPlain(srcDLL, maxEngineSize); {
	case errors.Is(err, os.ErrNotExist):
		// None shipped beside it: the one in place stays.
	case err != nil:
		return res(apitype.UpdateBadSignature, "wintun.dll：%v", err)
	case !sameFile(body, dll):
		dllStaged = filepath.Join(dir, "wintun.update.dll")
		if err := writePrivate(dllStaged, body); err != nil {
			return res(apitype.UpdateFailed, "写 %s：%v", dllStaged, err)
		}
		if s, err := signerOf(dllStaged); err != nil || !strings.Contains(s, "WireGuard") {
			return res(apitype.UpdateBadSignature, "wintun.dll 不是 WireGuard 签的（%q %v）", s, err)
		}
	}
	// Noted before the swap: the new engine counts its starts, and the old
	// one is where the note says, to go back to.
	old := oldName(self)
	n := &updateNote{From: version, To: st.version, Prev: old, At: time.Now().UTC()}
	if prev := readNote(note); prev != nil {
		n.Failed = prev.Failed
	}
	if err := writeNote(note, n); err != nil {
		return res(apitype.UpdateFailed, "记下更新：%v", err)
	}
	if err := swapIn(st.exe, self, old); err != nil {
		os.Remove(note)
		return res(apitype.UpdateFailed, "换 denebd.exe：%v", err)
	}
	keep = true
	// The signature goes along, the old one's with the old one.
	os.Remove(sigPath(old))
	os.Rename(sigPath(self), sigPath(old))
	if _, err := os.Lstat(st.sig); err == nil {
		os.Rename(st.sig, sigPath(self))
	}
	if dllStaged != "" {
		if err := swapIn(dllStaged, dll, oldName(dll)); err != nil {
			// The new engine runs with the old wintun.dll, which works.
			log.Printf("update: wintun.dll stays as it was: %v", err)
			os.Remove(dllStaged)
		}
	}
	// self is the new executable now; it restarts the service it is.
	cmd := exec.Command(self, "service", "restart")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS}
	if err := cmd.Start(); err != nil {
		return res(apitype.UpdateFailed, "换好了，没能重启服务（%v）：重启 Deneb 服务就用上新版", err)
	}
	go cmd.Wait()
	log.Printf("update: %s -> %s from %s; restarting the service", version, st.version, src)
	out.Result = apitype.UpdateDone
	return out
}

// oldName is where swapIn moves target aside: target.old, or a name of its
// own when that one is still in use.
func oldName(target string) string {
	old := target + ".old"
	if err := os.Remove(old); err != nil && !errors.Is(err, os.ErrNotExist) {
		old = fmt.Sprintf("%s.old%d", target, time.Now().UnixNano()) // still in use
	}
	return old
}

// swapIn puts staged in target's place: target is renamed to old first,
// which Windows allows for a running executable or a loaded DLL. The
// service removes .old files once an update is put in (removeOld).
func swapIn(staged, target, old string) error {
	if err := os.Rename(target, old); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(staged, target); err != nil {
		os.Rename(old, target)
		return err
	}
	return nil
}

// removeOld removes what updates left beside the executable.
func removeOld(dir string) {
	for _, pat := range []string{"denebd.exe.old*", "denebd.exe.failed*", "wintun.dll.old*", "*.update.exe", "*.update.exe.sig", "*.update.dll"} {
		m, _ := filepath.Glob(filepath.Join(dir, pat))
		for _, f := range m {
			os.Remove(f)
		}
	}
}

// restoreEngine puts prev back in the running engine's place, which did not
// get going (startCheck), and its signature with it.
func restoreEngine(self, prev string) error {
	failed := fmt.Sprintf("%s.failed%d", self, time.Now().UnixNano())
	if err := os.Rename(self, failed); err != nil {
		return err
	}
	if err := os.Rename(prev, self); err != nil {
		os.Rename(failed, self)
		return err
	}
	os.Remove(sigPath(self))
	os.Rename(sigPath(prev), sigPath(self))
	return nil
}

// sameFile: the file at path, the service's own, has content b.
func sameFile(b []byte, path string) bool {
	h, err := fileHash(path)
	return err == nil && h == sha256.Sum256(b)
}

func fileHash(path string) ([sha256.Size]byte, error) {
	var h [sha256.Size]byte
	f, err := os.Open(path)
	if err != nil {
		return h, err
	}
	defer f.Close()
	d := sha256.New()
	if _, err := io.Copy(d, f); err != nil {
		return h, err
	}
	copy(h[:], d.Sum(nil))
	return h, nil
}

var (
	crypt32              = windows.NewLazySystemDLL("crypt32.dll")
	procCryptMsgGetParam = crypt32.NewProc("CryptMsgGetParam")
	procCryptMsgClose    = crypt32.NewProc("CryptMsgClose")
)

const cmsgSignerCertInfoParam = 7 // CMSG_SIGNER_CERT_INFO_PARAM

// signerOf is the name on the certificate that signed path, once Windows
// verified the signature and its chain (WinVerifyTrust, without revocation
// checks, which need the network); "" for a file with no signature, an
// error for a bad one.
func signerOf(path string) (string, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	data := &windows.WinTrustData{
		Size:             uint32(unsafe.Sizeof(windows.WinTrustData{})),
		UIChoice:         windows.WTD_UI_NONE,
		RevocationChecks: windows.WTD_REVOKE_NONE,
		UnionChoice:      windows.WTD_CHOICE_FILE,
		StateAction:      windows.WTD_STATEACTION_VERIFY,
		FileOrCatalogOrBlobOrSgnrOrCert: unsafe.Pointer(&windows.WinTrustFileInfo{
			Size:     uint32(unsafe.Sizeof(windows.WinTrustFileInfo{})),
			FilePath: p,
		}),
	}
	verr := windows.WinVerifyTrustEx(windows.InvalidHWND, &windows.WINTRUST_ACTION_GENERIC_VERIFY_V2, data)
	data.StateAction = windows.WTD_STATEACTION_CLOSE
	windows.WinVerifyTrustEx(windows.InvalidHWND, &windows.WINTRUST_ACTION_GENERIC_VERIFY_V2, data)
	if verr == windows.Errno(windows.TRUST_E_NOSIGNATURE) {
		return "", nil
	}
	if verr != nil {
		return "", verr
	}
	var enc, contentType, formatType uint32
	var store, msg windows.Handle
	if err := windows.CryptQueryObject(windows.CERT_QUERY_OBJECT_FILE, unsafe.Pointer(p),
		windows.CERT_QUERY_CONTENT_FLAG_PKCS7_SIGNED_EMBED, windows.CERT_QUERY_FORMAT_FLAG_BINARY, 0,
		&enc, &contentType, &formatType, &store, &msg, nil); err != nil {
		return "", fmt.Errorf("read the signature: %w", err)
	}
	defer windows.CertCloseStore(store, 0)
	defer procCryptMsgClose.Call(uintptr(msg))
	// The first signer's issuer and serial number (a CERT_INFO), which
	// find its certificate among those the signature carries.
	var n uint32
	if r, _, err := procCryptMsgGetParam.Call(uintptr(msg), cmsgSignerCertInfoParam, 0, 0, uintptr(unsafe.Pointer(&n))); r == 0 {
		return "", fmt.Errorf("the signer: %w", err)
	}
	info := make([]byte, n)
	if r, _, err := procCryptMsgGetParam.Call(uintptr(msg), cmsgSignerCertInfoParam, 0, uintptr(unsafe.Pointer(&info[0])), uintptr(unsafe.Pointer(&n))); r == 0 {
		return "", fmt.Errorf("the signer: %w", err)
	}
	cert, err := windows.CertFindCertificateInStore(store, windows.X509_ASN_ENCODING|windows.PKCS_7_ASN_ENCODING, 0,
		windows.CERT_FIND_SUBJECT_CERT, unsafe.Pointer(&info[0]), nil)
	if err != nil {
		return "", fmt.Errorf("the signer's certificate: %w", err)
	}
	defer windows.CertFreeCertificateContext(cert)
	name := make([]uint16, 512)
	got := windows.CertGetNameString(cert, windows.CERT_NAME_SIMPLE_DISPLAY_TYPE, 0, nil, &name[0], uint32(len(name)))
	if got <= 1 {
		return "", errors.New("the signer's certificate has no name")
	}
	return windows.UTF16ToString(name[:got]), nil
}
