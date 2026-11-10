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

	"github.com/kittors/deneb/engine"
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

// selfUpdate replaces the service's engine with the denebd.exe at src, the
// one the window ships (M6 i2; the window asks at every start when the
// versions differ). The file is first copied beside the service's own,
// where only administrators can write, and checked there, so it cannot
// change between the check and the swap:
//
//   - signed, by the same publisher as the running engine. Releases are
//     not signed for now (an open-source project, 11-10): they answer
//     UpdateNeedsInstall before copying anything, as nothing would tell
//     our executable from one the user's malware put there, and the
//     window installs the service again, elevated. A test build, unsigned,
//     takes another unsigned build, to try the rest;
//   - newer; a test build whose versions are names, not semantic versions,
//     takes any other.
//
// Then it is swapped in (a running executable can be renamed, not
// overwritten), with the wintun.dll beside src when that one differs and
// WireGuard signed it, and a short-lived `denebd service restart` stops
// the service and starts it on the new engine.
func selfUpdate(ctx context.Context, src string) apitype.UpdateResult {
	updateMu.Lock()
	defer updateMu.Unlock()
	res := func(result, format string, a ...any) apitype.UpdateResult {
		return apitype.UpdateResult{Result: result, Message: fmt.Sprintf(format, a...)}
	}
	if !filepath.IsAbs(src) || !strings.EqualFold(filepath.Base(src), "denebd.exe") {
		return res(apitype.UpdateFailed, "要 denebd.exe 的完整路径，收到的是 %q", src)
	}
	self, err := os.Executable()
	if err != nil {
		return res(apitype.UpdateFailed, "%v", err)
	}
	if mine, _ := signerOf(self); mine == "" && !engine.TestBuild {
		// Nothing tells our executable from anyone's: only an administrator
		// may put it in, by installing the service again.
		return res(apitype.UpdateNeedsInstall, "没有签名的版本，要重新装一次服务才能换（会弹一次 UAC）")
	}
	dir := filepath.Dir(self)
	staged := filepath.Join(dir, "denebd.update.exe") // run below to ask its version: keeps .exe
	if err := copyFile(src, staged); err != nil {
		return res(apitype.UpdateFailed, "复制 %s：%v", src, err)
	}
	var keep bool
	var dllStaged string
	defer func() {
		if !keep {
			os.Remove(staged)
			if dllStaged != "" {
				os.Remove(dllStaged)
			}
		}
	}()
	if why := sameSigner(staged, self); why != "" {
		return res(apitype.UpdateBadSignature, "%s", why)
	}
	ver, err := exeVersion(ctx, staged)
	if err != nil {
		return res(apitype.UpdateFailed, "读新文件的版本：%v", err)
	}
	out := apitype.UpdateResult{Version: ver}
	switch cmp, ok := compareVersions(ver, version); {
	case ver == version, ok && cmp == 0:
		out.Result = apitype.UpdateSame
		return out
	case ok && cmp < 0:
		out.Result, out.Message = apitype.UpdateOlder, fmt.Sprintf("正在跑的 %s 比它新", version)
		return out
	case !ok && !engine.TestBuild:
		out.Result, out.Message = apitype.UpdateFailed, fmt.Sprintf("认不出版本号：%q、%q", ver, version)
		return out
	}
	dll := filepath.Join(dir, "wintun.dll")
	if srcDLL := filepath.Join(filepath.Dir(src), "wintun.dll"); fileDiffers(srcDLL, dll) {
		dllStaged = filepath.Join(dir, "wintun.update.dll")
		if err := copyFile(srcDLL, dllStaged); err != nil {
			return res(apitype.UpdateFailed, "复制 %s：%v", srcDLL, err)
		}
		if s, err := signerOf(dllStaged); err != nil || !strings.Contains(s, "WireGuard") {
			return res(apitype.UpdateBadSignature, "wintun.dll 不是 WireGuard 签的（%q %v）", s, err)
		}
	}
	if err := swapIn(staged, self); err != nil {
		return res(apitype.UpdateFailed, "换 denebd.exe：%v", err)
	}
	keep = true
	if dllStaged != "" {
		if err := swapIn(dllStaged, dll); err != nil {
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
	log.Printf("update: %s -> %s from %s; restarting the service", version, ver, src)
	out.Result = apitype.UpdateDone
	return out
}

// swapIn puts staged in target's place: target is renamed to .old first,
// which Windows allows for a running executable or a loaded DLL. The next
// start of the service removes .old files (removeOld).
func swapIn(staged, target string) error {
	old := target + ".old"
	if err := os.Remove(old); err != nil && !errors.Is(err, os.ErrNotExist) {
		old = fmt.Sprintf("%s.old%d", target, time.Now().UnixNano()) // still in use
	}
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
	for _, pat := range []string{"denebd.exe.old*", "wintun.dll.old*", "*.update.exe", "*.update.dll"} {
		m, _ := filepath.Glob(filepath.Join(dir, pat))
		for _, f := range m {
			os.Remove(f)
		}
	}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// fileDiffers: a exists, and b is missing or has other content.
func fileDiffers(a, b string) bool {
	ha, err := fileHash(a)
	if err != nil {
		return false
	}
	hb, err := fileHash(b)
	return err != nil || ha != hb
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

// exeVersion asks an executable, once its signature passed, what version
// it is: `denebd version`.
func exeVersion(ctx context.Context, exe string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	b, err := exec.CommandContext(ctx, exe, "version").Output()
	if err != nil {
		return "", err
	}
	v := strings.TrimSpace(string(b))
	if v == "" || strings.ContainsAny(v, " \t\r\n") {
		return "", fmt.Errorf("unexpected answer %q", v)
	}
	return v, nil
}

// sameSigner says why newFile may not replace self, or "" when it may.
func sameSigner(newFile, self string) string {
	mine, err := signerOf(self)
	if err != nil {
		return fmt.Sprintf("正在跑的引擎签名有问题：%v", err)
	}
	theirs, err := signerOf(newFile)
	if err != nil {
		return fmt.Sprintf("新文件的签名不对：%v", err)
	}
	switch {
	case mine == "" && theirs == "" && engine.TestBuild:
		return ""
	case mine == "":
		return "正在跑的引擎没有签名，新文件却签了名"
	case theirs == "":
		return "新文件没有签名"
	case theirs != mine:
		return fmt.Sprintf("新文件是 %q 签的，正在跑的是 %q 签的", theirs, mine)
	}
	return ""
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
