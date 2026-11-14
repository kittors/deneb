package main

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/netproxy"
	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/engine/localapi/apitype"
)

// appIcon is the update window's icon, the app's own (mygo.json's), built
// in: among the resources, every platform's package would carry it as a
// file of its own beside the icon that platform already has (DESIGN §15).
//
//go:embed icon.png
var appIcon []byte

// Updates (design/plan/m6/client.md, i4). MyGo's updater checks the latest
// release in the background, a day apart, and offers what it finds in its
// window, drawn in native UI as Deneb's are. Only builds with updates in
// mygo.json check: development builds never do, nor the .deb, which a new
// .deb updates with its engine.
func useUpdater() {
	// Deneb draws its integrated update entry and dialog using mygo.Updater.
	// A release build names its download site (updateFeed); its archives are
	// still checked with the configured Ed25519 key. Without one, updates
	// come from the GitHub releases of mygo.json's "updates".
	if updateFeed != "" {
		mygo.Updater.SetSource(httpsUpdateSource(updateFeed))
	}
	if dir := os.Getenv("DENEB_UPDATE_SOURCE"); dir != "" {
		// A release before it is published, from a folder that holds what
		// `mygo build` wrote for it. Its files must be signed with the key
		// of mygo.json all the same, or they are not installed.
		mygo.Updater.SetSource(dirSource(dir))
	}
}

// dirSource serves updates from a folder: the manifest of this platform,
// and the files it names, by their names.
type dirSource string

func (d dirSource) Manifest(ctx context.Context, prereleases bool) (io.ReadCloser, error) {
	f, err := os.Open(filepath.Join(string(d), "update-"+runtime.GOOS+"-"+runtime.GOARCH+".json"))
	if errors.Is(err, os.ErrNotExist) && runtime.GOOS == "darwin" {
		// A universal app's.
		return os.Open(filepath.Join(string(d), "update-darwin-universal.json"))
	}
	return f, err
}

func (d dirSource) Open(ctx context.Context, url string) (io.ReadCloser, error) {
	return os.Open(filepath.Join(string(d), path.Base(url)))
}

// bundledEngine returns the engine this package ships, denebd among the
// app's resources (denebd.exe beside Deneb.exe on Windows), or "" where it
// ships none: development builds, which find one on their own, and the
// .deb, whose engine is a package of its own.
//
// Not in a folder of its own: an update renames each of the app's files
// and folders aside, and Windows renames a running program, as the
// engine the window started is, but not a folder that holds one.
func bundledEngine() string {
	dir, err := mygo.App.Path(mygo.PathResources)
	if err != nil {
		return ""
	}
	p := filepath.Join(dir, "denebd")
	if runtime.GOOS == "windows" {
		p += ".exe"
	}
	if fi, err := os.Stat(p); err != nil || fi.IsDir() {
		return ""
	}
	return p
}

// engineAPI is what followEngine asks of the engine (localclient.Client).
type engineAPI interface {
	Version(ctx context.Context) (*apitype.VersionInfo, error)
	UpdateEngine(ctx context.Context, path string) (*apitype.UpdateResult, error)
	Shutdown(ctx context.Context) error
}

// followEngine has the engine catch up with the window, once a launch,
// after an update or a reinstall: the engine the package ships, bundled,
// built with the window's version want, takes the place of a running one
// of another version (i4). The engine says how:
//
//   - the Windows service replaces itself (updated), or keeps itself when
//     the shipped one is not newer (same, older);
//   - one the window started (not-service) stops, and keepEngine starts the
//     shipped one in its place within 3 seconds;
//   - the service of an unsigned release (needs-install) cannot tell the
//     shipped file from one put there by another program, so it is
//     installed again as an administrator, as the first run installed it:
//     Windows asks the person once (install), and keepEngine waits for it.
//
// It returns what came of it, for the log.
func followEngine(ctx context.Context, lc engineAPI, bundled, want string, install func(context.Context, string) error) string {
	if bundled == "" {
		return "no engine shipped"
	}
	v, err := lc.Version(ctx)
	if err != nil {
		return fmt.Sprintf("engine version: %v", err)
	}
	if v.Engine == want {
		return "engine " + want + ", as shipped"
	}
	r, err := lc.UpdateEngine(ctx, bundled)
	if err != nil {
		return fmt.Sprintf("engine update (%s to %s): %v", v.Engine, want, err)
	}
	switch r.Result {
	case apitype.UpdateNotService:
		if err := lc.Shutdown(ctx); err != nil {
			return fmt.Sprintf("engine %s: stopping it for %s: %v", v.Engine, want, err)
		}
		return fmt.Sprintf("engine %s stopped, for %s", v.Engine, want)
	case apitype.UpdateNeedsInstall:
		if err := install(ctx, bundled); err != nil {
			return fmt.Sprintf("engine %s: installing the service of %s: %v", v.Engine, want, err)
		}
		return fmt.Sprintf("engine %s: the service of %s installed", v.Engine, want)
	}
	msg := fmt.Sprintf("engine %s to %s: %s", v.Engine, want, r.Result)
	if r.Message != "" {
		msg += " (" + r.Message + ")"
	}
	return msg
}

// installing is set while the service is installed again: keepEngine
// starts no engine of the window's meanwhile, which would take this
// computer's identity while the service is stopped.
var installing atomic.Bool

// followEngineOnce runs followEngine for this launch's engine.
func followEngineOnce(a *app, lc engineAPI) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	install := func(ctx context.Context, exe string) error {
		installing.Store(true)
		defer installing.Store(false)
		return installService(ctx, exe)
	}
	result := followEngine(ctx, lc, bundledEngine(), mygo.App.Version(), install)
	log.Printf("deneb: %s", result)
	v, err := lc.Version(ctx)
	if err != nil || v.Engine != mygo.App.Version() {
		a.post(func(*ui.Context) {
			a.engineErr = "系统引擎还未更新，请在系统认证窗口中允许安装，或重新打开 Deneb 重试"
		})
	}
}

// updateFeed is the folder of the download site that holds the signed
// manifests (update-<os>-<arch>.json) and archives, as
// "https://<site>/download/". Release builds set it through MYGO_LDFLAGS
// (deploy/package.sh, DENEB_UPDATE_FEED), so the site's address stays out
// of the repository.
var updateFeed string

// httpsUpdateSource serves the same signed manifests as a release. Archive
// hosts are constrained to the feed's HTTPS origin, and Go verifies TLS.
type httpsUpdateSource string

func (s httpsUpdateSource) Manifest(ctx context.Context, _ bool) (io.ReadCloser, error) {
	return s.Open(ctx, string(s)+"update-"+runtime.GOOS+"-"+runtime.GOARCH+".json")
}
func (s httpsUpdateSource) Open(ctx context.Context, raw string) (io.ReadCloser, error) {
	base, err := url.Parse(string(s))
	if err != nil {
		return nil, err
	}
	target, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if target.Scheme != "https" || target.Host != base.Host || target.User != nil || !strings.HasPrefix(target.Path, base.Path) {
		return nil, errors.New("更新文件不在 Deneb HTTPS 下载目录中")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Transport: netproxy.Transport(), CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || req.URL.Scheme != "https" || req.URL.Host != base.Host {
			return errors.New("更新下载跳转到了其他服务")
		}
		return nil
	}}
	response, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("更新服务：%s", response.Status)
	}
	return response.Body, nil
}
