package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// The files a window names for the service to take in (the engine it ships,
// its signature, the wintun.dll beside it) are read the way an administrator
// would not be fooled into (design/plan/m7/engine-self-update.md §6): a
// regular file on this computer's disk, by no link, with no other name,
// not too big, read whole into memory (readPlain). Its signature is checked
// there, and only then is anything written: a link to a file only the
// service may read, a FIFO or a device in its place is refused before it
// reaches the disk, where others could read it.

// errNotPlain: what a window named is not such a file.
var errNotPlain = errors.New("只收本机磁盘上的普通文件")

// maxEngineSize is the most the service reads of an engine the window
// ships, or of the wintun.dll beside it; maxSigSize, of a signature.
var (
	maxEngineSize int64 = 256 << 20
	maxSigSize    int64 = 64 << 10
)

// readUpTo reads r to its end, which is at most max bytes in.
func readUpTo(r io.Reader, max int64, name string) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("%w：%s 太大", errNotPlain, name)
	}
	return b, nil
}

// localDrivePath checks that p is a path on one of this computer's drives,
// as Windows writes one: a drive letter, a colon, then names between single
// backslashes. Not a share (\\server\share), where the service would sign
// in as this computer, nor a device (\\?\, \\.\, \??\), a path relative to
// a drive, one with . or .. or a stream (name:stream) in it. Checked on
// every system, used on Windows (readPlain).
func localDrivePath(p string) error {
	bad := fmt.Errorf("%w：%q 不是本机磁盘上的路径", errNotPlain, p)
	letter := len(p) > 0 && ('a' <= p[0] && p[0] <= 'z' || 'A' <= p[0] && p[0] <= 'Z')
	if len(p) < 4 || !letter || p[1] != ':' || p[2] != '\\' {
		return bad
	}
	for _, name := range strings.Split(p[3:], `\`) {
		if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/:*?"<>|`) {
			return bad
		}
		for _, r := range name {
			if r < 0x20 {
				return bad
			}
		}
	}
	return nil
}

// writeNew writes b to path, a new file, synced; nothing is left of it when
// that fails.
func writeNew(path string, b []byte, mode os.FileMode) error {
	os.Remove(path)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return err
	}
	return nil
}
