package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/kittors/deneb/engine"
	"github.com/kittors/deneb/engine/localapi/apitype"
)

// The system service replacing itself with the engine the window ships,
// without a password (design/plan/m7/engine-self-update.md): the new
// executable is signed with the key of the window's updates, its signature
// beside it (denebd.sig, deploy/sign-engine.go); the service reads it whole
// (readPlain), checks it in memory, and only then writes it where only it
// can write, asks it its version, and takes only a newer one. A note beside
// the service's executable
// (update.json) counts the new engine's starts: one that does not get going
// is put back by the old one.

// engineKeys are the keys a new engine must be signed with: the window's
// updates' key, cmd/deneb/mygo.json's updates.publicKey
// (TestEngineKeysHaveTheWindows). A new key comes in a release that carries
// both, before any release is signed with it (§6).
var engineKeys = []string{"oIpKlr1FzjpMDb9aZiY3pT0cIL89a02CwLA2/sDQztA="}

// engineSig is an engine's signature file: Ed25519 over the file's SHA-256,
// as MyGo signs the window's updates. Version is what the file says it is;
// it is asked of the file too, once the signature checks out.
type engineSig struct {
	Version   string `json:"version"`
	SHA256    string `json:"sha256"`
	Signature string `json:"signature"`
}

var (
	errNoSig  = errors.New("没有签名")
	errBadSig = errors.New("签名不对")
)

// sigPath is where exe's signature is.
func sigPath(exe string) string { return exe + ".sig" }

// readEngineSig reads exe's signature (readPlain): errNoSig when there is
// none. What a file that is not one says stays out of the error.
func readEngineSig(exe string) (*engineSig, []byte, error) {
	b, err := readPlain(sigPath(exe), maxSigSize)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, errNoSig
	}
	if err != nil {
		return nil, nil, fmt.Errorf("%w：%w", errBadSig, err)
	}
	var s engineSig
	if err := json.Unmarshal(b, &s); err != nil || s.Version == "" || s.SHA256 == "" || s.Signature == "" {
		return nil, nil, fmt.Errorf("%w：签名文件看不懂", errBadSig)
	}
	return &s, b, nil
}

// check reports whether file is the one s signs, with one of keys.
func (s *engineSig) check(file string, keys []string) error {
	b, err := readPlain(file, maxEngineSize)
	if err != nil {
		return err
	}
	return s.checkBody(b, keys)
}

// checkBody reports whether body is the file s signs, with one of keys.
func (s *engineSig) checkBody(body []byte, keys []string) error {
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != strings.ToLower(s.SHA256) {
		return fmt.Errorf("%w：文件和签名对不上", errBadSig)
	}
	sig, err := base64.StdEncoding.DecodeString(s.Signature)
	if err != nil {
		return fmt.Errorf("%w：签名看不懂", errBadSig)
	}
	for _, k := range keys {
		pub, err := base64.StdEncoding.DecodeString(k)
		if err == nil && len(pub) == ed25519.PublicKeySize && ed25519.Verify(ed25519.PublicKey(pub), sum[:], sig) {
			return nil
		}
	}
	return fmt.Errorf("%w：不是 Deneb 签的", errBadSig)
}

// signedEngine: exe has a signature beside it that checks out. The service
// keeps its own, so that it knows it came signed (§6).
func signedEngine(exe string) bool {
	s, _, err := readEngineSig(exe)
	return err == nil && s.check(exe, engineKeys) == nil
}

// staged is a new engine copied beside the service's, checked, its
// signature written beside it: ready to be swapped in.
type staged struct {
	exe, sig string
	version  string
}

func (s *staged) remove() {
	os.Remove(s.exe)
	os.Remove(s.sig)
}

// stageEngine reads src, the engine the window ships, whole (readPlain),
// checks it in memory, and only then writes it to stagedExe, where only the
// service can write, to ask it its version (§2 steps 1, 3 to 6): signed
// with engineKeys, a version newer than current, not the one that could
// not start here (failed). self is the service's own executable: a service
// that came signed takes nothing unsigned. A nil staged comes with the
// result to answer, and leaves nothing behind.
func stageEngine(ctx context.Context, src, stagedExe, self, current, failed string) (*staged, apitype.UpdateResult) {
	res := func(result, format string, a ...any) apitype.UpdateResult {
		return apitype.UpdateResult{Result: result, Message: fmt.Sprintf(format, a...)}
	}
	sig, sigBytes, err := readEngineSig(src)
	unsignedTest := false
	switch {
	case errors.Is(err, errNoSig) && engine.TestBuild:
		// Test builds are never signed (deploy/check-release.sh keeps them
		// out of releases): one takes another, to try the rest.
		unsignedTest = true
	case errors.Is(err, errNoSig) && signedEngine(self):
		// It came signed: an unsigned one in the window's package is not
		// ours, and nobody is asked a password for it either.
		return nil, res(apitype.UpdateBadSignature, "安装包里的系统网卡没有签名")
	case errors.Is(err, errNoSig):
		// A development build: an administrator puts it in.
		return nil, res(apitype.UpdateNeedsInstall, "没有签名的版本，要以管理员身份重新装一次")
	case err != nil:
		return nil, res(apitype.UpdateBadSignature, "%v", err)
	}
	if !unsignedTest && failed != "" && sig.Version == failed {
		return nil, apitype.UpdateResult{Result: apitype.UpdateRolledBack, Version: failed, Message: "这一版在这台电脑上起不来，已经用回原来的版本"}
	}
	body, err := readPlain(src, maxEngineSize)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil, res(apitype.UpdateFailed, "没有 %s", src)
	case err != nil:
		return nil, res(apitype.UpdateBadSignature, "%v", err)
	}
	if !unsignedTest {
		if err := sig.checkBody(body, engineKeys); err != nil {
			return nil, res(apitype.UpdateBadSignature, "%v", err)
		}
	}
	// Checked (or a test build's): only now on the disk.
	st := &staged{exe: stagedExe, sig: sigPath(stagedExe)}
	os.Remove(st.sig)
	if err := writeNew(st.exe, body, 0o755); err != nil {
		return nil, res(apitype.UpdateFailed, "写 %s：%v", st.exe, err)
	}
	keep := false
	defer func() {
		if !keep {
			st.remove()
		}
	}()
	ver, err := exeVersion(ctx, st.exe)
	if err != nil {
		return nil, res(apitype.UpdateFailed, "读新文件的版本：%v", err)
	}
	if !unsignedTest && ver != sig.Version {
		return nil, res(apitype.UpdateBadSignature, "签名上写的是 %s，程序说它是 %s", sig.Version, ver)
	}
	out := apitype.UpdateResult{Version: ver}
	switch cmp, ok := compareVersions(ver, current); {
	case ver == current, ok && cmp == 0:
		out.Result = apitype.UpdateSame
		return nil, out
	case ok && cmp < 0:
		out.Result, out.Message = apitype.UpdateOlder, fmt.Sprintf("正在跑的 %s 比它新", current)
		return nil, out
	case !ok && !engine.TestBuild && !(isVersion(ver) && !isVersion(current)):
		// A development build running takes a release: signed, ours.
		out.Result, out.Message = apitype.UpdateFailed, fmt.Sprintf("认不出版本号：%q、%q", ver, current)
		return nil, out
	}
	if sigBytes != nil {
		if err := writeFileSync(st.sig, sigBytes, 0o644); err != nil {
			return nil, res(apitype.UpdateFailed, "写签名：%v", err)
		}
	}
	keep = true
	st.version = ver
	return st, out
}

func isVersion(s string) bool {
	_, ok := parseVersion(s)
	return ok
}

// updateNote is the service's record of an update, update.json beside its
// executable. Written before the swap: To is the version just put in, and
// Tries how often it started (§2 step 10); Prev is the executable it
// replaced, to put back. Failed is a version that could not start here and
// was gone back from: it is not taken again.
type updateNote struct {
	From   string    `json:"from"`
	To     string    `json:"to,omitempty"`
	Prev   string    `json:"prev,omitempty"`
	Tries  int       `json:"tries"`
	Failed string    `json:"failed,omitempty"`
	At     time.Time `json:"at"`
}

// maxStarts is how often a new engine may start without getting going
// before the old one is put back.
const maxStarts = 3

// healthyAfter is how long a new engine runs before it counts as put in.
const healthyAfter = 15 * time.Second

func readNote(path string) *updateNote {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var n updateNote
	if json.Unmarshal(b, &n) != nil {
		return nil
	}
	return &n
}

func writeNote(path string, n *updateNote) error {
	b, err := json.Marshal(n)
	if err != nil {
		return err
	}
	return writeFileSync(path, b, 0o600)
}

// failedVersion is the version that could not start here, if one.
func failedVersion(notePath string) string {
	if n := readNote(notePath); n != nil {
		return n.Failed
	}
	return ""
}

// startCheck runs first thing in the service's engine, before it touches
// the network (§2 step 10). An engine just put in counts its start; one that
// started maxStarts times without getting going puts the previous one back
// (restore), notes it, and reports goBack: the service then stops, for its
// manager to start the one put back. An update that never got swapped in
// (cut off between the note and the swap) is cleared away (leftovers).
func startCheck(notePath, own string, restore func(prev string) error, leftovers func(), logf func(string, ...any)) (goBack bool) {
	n := readNote(notePath)
	if n == nil || n.To == "" {
		return false
	}
	if n.To != own {
		logf("update: %s was not put in (this is %s): clearing it", n.To, own)
		leftovers()
		n.To, n.Prev, n.Tries = "", "", 0
		keepOrRemoveNote(notePath, n)
		return false
	}
	n.Tries++
	if n.Tries <= maxStarts || n.Prev == "" {
		if err := writeNote(notePath, n); err != nil {
			logf("update: noting the start: %v", err)
		}
		return false
	}
	if err := restore(n.Prev); err != nil {
		logf("update: %s did not get going %d times, and %s could not be put back: %v", own, n.Tries-1, n.From, err)
		return false
	}
	logf("update: %s did not get going %d times: %s put back", own, n.Tries-1, n.From)
	writeNote(notePath, &updateNote{From: n.From, Failed: own, At: time.Now().UTC()})
	return true
}

// putIn runs once the engine has run healthyAfter (§2 step 11): the update
// is done, and the previous executable goes (cleanup).
func putIn(notePath, own string, cleanup func(prev string)) {
	n := readNote(notePath)
	if n == nil || n.To != own {
		return
	}
	if n.Prev != "" {
		cleanup(n.Prev)
	}
	n.To, n.Prev, n.Tries = "", "", 0
	keepOrRemoveNote(notePath, n)
}

// keepOrRemoveNote keeps a note only for the version that failed.
func keepOrRemoveNote(path string, n *updateNote) {
	if n.Failed == "" {
		os.Remove(path)
		return
	}
	writeNote(path, n)
}

// exeVersion asks an executable, once its signature passed, what version it
// is: `denebd version`.
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

// copyFile copies src to dst, a new file, synced.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	os.Remove(dst)
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
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

// writeFileSync writes a file whole through a temporary one beside it, and
// syncs it: a cut leaves the old one or the new one.
func writeFileSync(path string, b []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	os.Remove(tmp)
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
