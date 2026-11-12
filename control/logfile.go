package control

import (
	"bufio"
	"bytes"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The server's log, in production, goes into files of its own, one a day,
// kept LogKeepDays (PRIVACY.md): what a log says (emails, addresses) is
// gone after that. Docker's own log of the container keeps by size, not by
// age, so at a quiet server it would keep lines for months; it gets only
// what is said before the files open, and crashes.

// LogKeepDays is how many days of log files stay: today's and the days
// before it, this many in all.
const LogKeepDays = 30

// LogFiles writes the log into dir/deneb-control-YYYYMMDD.log (UTC), a new
// file each day, and drops the files older than LogKeepDays.
type LogFiles struct {
	dir string
	now func() time.Time // tests set it

	mu  sync.Mutex
	day string
	f   *os.File
}

// OpenLogFiles opens today's file in dir, making dir (0700) if it is not
// there, and drops the old files.
func OpenLogFiles(dir string) (*LogFiles, error) { return openLogFiles(dir, time.Now) }

func openLogFiles(dir string, now func() time.Time) (*LogFiles, error) {
	l := &LogFiles{dir: dir, now: now}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	// Only the server reads its log: it has emails and addresses.
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.open(); err != nil {
		return nil, err
	}
	return l, nil
}

func logFileName(day string) string { return "deneb-control-" + day + ".log" }

// open opens today's file, if it is not open yet, and drops the files of
// the days past keeping; l.mu is held.
func (l *LogFiles) open() error {
	day := l.now().UTC().Format("20060102")
	if day == l.day && l.f != nil {
		return nil
	}
	f, err := os.OpenFile(filepath.Join(l.dir, logFileName(day)), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if l.f != nil {
		l.f.Close()
	}
	l.f, l.day = f, day
	l.prune()
	return nil
}

// prune drops the files of days before the last LogKeepDays.
func (l *LogFiles) prune() {
	first := l.now().UTC().AddDate(0, 0, -(LogKeepDays - 1)).Format("20060102")
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		day, ok := logFileDay(e.Name())
		if ok && day < first {
			os.Remove(filepath.Join(l.dir, e.Name()))
		}
	}
}

// logFileDay is the day a log file's name says, if it is one.
func logFileDay(name string) (string, bool) {
	day, ok := strings.CutPrefix(name, "deneb-control-")
	day, ok2 := strings.CutSuffix(day, ".log")
	if !ok || !ok2 || len(day) != 8 {
		return "", false
	}
	if _, err := strconv.Atoi(day); err != nil {
		return "", false
	}
	return day, true
}

// Write writes p into today's file, moving to a new one when the day
// changed.
func (l *LogFiles) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.open(); err != nil && l.f == nil {
		return 0, err
	}
	return l.f.Write(p)
}

// Close closes today's file.
func (l *LogFiles) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}

// maxLogRead bounds what deneb-control logs reads back.
const maxLogRead = 64 << 20

// lastLogLines are the last n lines of the log files in dir that contain
// grep (all lines if grep is empty), oldest first.
func lastLogLines(dir string, n int, grep string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var days []string
	for _, e := range entries {
		if day, ok := logFileDay(e.Name()); ok {
			days = append(days, day)
		}
	}
	slices.Sort(days)
	var out []string
	read := 0
	// Newest file first, until n lines are found.
	for i := len(days) - 1; i >= 0 && len(out) < n && read < maxLogRead; i-- {
		b, err := os.ReadFile(filepath.Join(dir, logFileName(days[i])))
		if err != nil {
			continue
		}
		read += len(b)
		var lines []string
		sc := bufio.NewScanner(bytes.NewReader(b))
		sc.Buffer(nil, 1<<20)
		for sc.Scan() {
			if grep == "" || strings.Contains(sc.Text(), grep) {
				lines = append(lines, sc.Text())
			}
		}
		take := min(len(lines), n-len(out))
		out = append(lines[len(lines)-take:], out...)
	}
	return out, nil
}

// handleAdminLogs serves the end of the log: ?n= lines (default 200),
// ?grep= only the lines with that in them.
func (s *Server) handleAdminLogs(w http.ResponseWriter, r *http.Request) {
	if s.cfg.LogDir == "" {
		s.writeErr(w, r, errInvalid("这个服务器的日志不在文件里（开发模式，或者 -log-dir off）：看它的标准输出"))
		return
	}
	n := 200
	if v, err := strconv.Atoi(r.URL.Query().Get("n")); err == nil && v > 0 {
		n = min(v, 100000)
	}
	lines, err := lastLogLines(s.cfg.LogDir, n, r.URL.Query().Get("grep"))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if lines == nil {
		lines = []string{}
	}
	writeJSON(w, http.StatusOK, lines)
}

// AdminLogs is the end of the log, through the admin socket at path.
func AdminLogs(path string, n int, grep string) ([]string, error) {
	q := "/logs?n=" + strconv.Itoa(n)
	if grep != "" {
		q += "&grep=" + url.QueryEscape(grep)
	}
	var out []string
	return out, adminCall(path, "GET", q, nil, &out)
}
