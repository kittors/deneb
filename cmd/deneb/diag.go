package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
)

// diagnoser puts this computer's diagnostics package together
// (localclient.Client.Diagnostics): the engine's state, paths and log,
// without keys or what went through (DESIGN §6.14, M6 i7).
type diagnoser interface {
	Diagnostics(ctx context.Context, w io.Writer) error
}

// saveDiagnostics has the engine put the diagnostics package together into
// the person's downloads, and says where it went.
func saveDiagnostics(lc diagnoser) (string, error) {
	dir, err := mygo.App.Path(mygo.PathDownloads)
	if err != nil {
		return "", err
	}
	return saveDiagnosticsIn(dir, lc, time.Now())
}

// saveDiagnosticsIn is saveDiagnostics into dir, at now: "Deneb 诊断包
// 2026-11-09 23-41.zip", or with " (2)" and on when one of that name is
// there already.
func saveDiagnosticsIn(dir string, lc diagnoser, now time.Time) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := diagName(now)
	var f *os.File
	var path string
	for n := 1; ; n++ {
		path = filepath.Join(dir, name)
		if n > 1 {
			path = filepath.Join(dir, fmt.Sprintf("%s (%d).zip", strings.TrimSuffix(name, ".zip"), n))
		}
		var err error
		f, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			break
		}
		if !os.IsExist(err) || n >= 99 {
			return "", err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := lc.Diagnostics(ctx, f); err != nil {
		f.Close()
		os.Remove(path)
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}
