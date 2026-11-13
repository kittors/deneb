package control

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// The server's log on the console (DESIGN §14.15): today's and
// yesterday's files (UTC days), line by line, each with a kind; what a
// line carries of the addresses and ports devices report, tokens and
// codes is masked before it goes out. The console shows the last 200,
// pages back 200 at a time to 2000 at most (older: deneb-control logs on
// the server), and asks every 2 seconds for what came after.
//
// A line's seq is its day (days since 1970-01-01, UTC) times opsLogDay,
// plus where it starts in its file: it stays the same as the files grow
// and the days turn, so the console can ask for what came after or
// before one, and a request for what came after reads only the end of a
// file.

const (
	opsLogPage = 200
	opsLogMost = 2000
	opsLogDay  = 10_000_000_000 // 10 GB: no day's file is longer
	// opsLogRead is how much of a file is read at most, its end.
	opsLogRead = 64 << 20
)

// OpsLine is a line of the log, as the console shows it.
type OpsLine struct {
	Seq  int64     `json:"seq"`
	At   time.Time `json:"at"`
	Kind string    `json:"kind"` // login, device, op, alert, error, other
	Text string    `json:"text"`
}

// OpsLog is an answer of the log's: the lines, oldest first; whether the
// console may page back further; the newest line's seq, to ask after.
type OpsLog struct {
	Lines []OpsLine `json:"lines"`
	More  bool      `json:"more"`
	Last  int64     `json:"last"`
	// Note says why there is nothing: a server whose log is not in files.
	Note string `json:"note,omitempty"`
}

// consoleLog answers a request for the log: kind ("" for all); with after,
// the lines after it; with before, the opsLogPage before it; with
// neither, the last opsLogPage. Only the newest opsLogMost of the kind are
// ever served.
func (s *Server) consoleLog(kind string, after, before int64) (OpsLog, error) {
	res := OpsLog{Lines: []OpsLine{}}
	if s.cfg.LogDir == "" {
		res.Note = "这个服务器的日志不在文件里（开发模式，或者 -log-dir off）"
		return res, nil
	}
	today := s.now().UTC().Truncate(24 * time.Hour)
	days := []time.Time{today.AddDate(0, 0, -1), today}
	var all []OpsLine
	for _, day := range days {
		from, skip := int64(-1), false // the whole file
		if after > 0 {
			switch d := dayNumber(day); {
			case d < after/opsLogDay:
				continue // a day before the one asked after
			case d == after/opsLogDay:
				from, skip = after%opsLogDay, true // past the line asked after
			default:
				from = 0
			}
		}
		lines, err := readLogDay(filepath.Join(s.cfg.LogDir, logFileName(day.Format("20060102"))), day, from, skip)
		if err != nil {
			return res, err
		}
		all = append(all, lines...)
	}
	res.Last = after
	if len(all) > 0 {
		res.Last = max(res.Last, all[len(all)-1].Seq)
	}
	var lines []OpsLine
	for _, l := range all {
		if (kind == "" || l.Kind == kind) && l.Seq > after {
			lines = append(lines, l)
		}
	}
	if len(lines) > opsLogMost {
		lines = lines[len(lines)-opsLogMost:]
	}
	switch {
	case after > 0:
		res.Lines = lines
	case before > 0:
		i := 0
		for i < len(lines) && lines[i].Seq < before {
			i++
		}
		lines = lines[:i]
		fallthrough
	default:
		if len(lines) > opsLogPage {
			lines, res.More = lines[len(lines)-opsLogPage:], true
		}
		res.Lines = lines
	}
	if res.Lines == nil {
		res.Lines = []OpsLine{}
	}
	return res, nil
}

// dayNumber is a day's number, days since 1970-01-01 (UTC).
func dayNumber(day time.Time) int64 { return day.Unix() / 86400 }

// readLogDay reads the lines of one day's log file from the one that
// starts at from (-1: the whole file, or its last opsLogRead), with skip
// leaving that first one out (the one asked after). No file is no lines.
func readLogDay(path string, day time.Time, from int64, skip bool) ([]OpsLine, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	skipFirst, start := skip, from
	if from < 0 {
		start = max(fi.Size()-opsLogRead, 0)
		skipFirst = start > 0 // a line cut in two
	}
	if start >= fi.Size() {
		return nil, nil
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return nil, err
	}
	base := dayNumber(day) * opsLogDay
	at := day
	var out []OpsLine
	r := bufio.NewReaderSize(f, 64<<10)
	offset := start
	for {
		line, err := r.ReadString('\n')
		if len(line) == 0 && err != nil {
			break
		}
		pos := offset
		offset += int64(len(line))
		if skipFirst {
			skipFirst = false
			continue
		}
		if !strings.HasSuffix(line, "\n") {
			break // still being written: the next request has it whole
		}
		line = strings.TrimRight(line, "\r\n")
		text := line
		if t, err := time.Parse("2006/01/02 15:04:05", prefix(line, 19)); err == nil {
			at, text = t.UTC(), strings.TrimPrefix(line[19:], " ")
		}
		out = append(out, OpsLine{Seq: base + pos, At: at, Kind: logKind(text), Text: redactLog(text)})
		if err != nil {
			break
		}
	}
	return out, nil
}

func prefix(s string, n int) string {
	if len(s) < n {
		return s
	}
	return s[:n]
}

// logKind is what a line of the server's log is about, by how it starts.
func logKind(text string) string {
	has := func(ps ...string) bool {
		for _, p := range ps {
			if strings.HasPrefix(text, p) {
				return true
			}
		}
		return false
	}
	low := strings.ToLower(text)
	switch {
	case has("alert "):
		return "alert"
	case strings.Contains(low, "panic") || strings.Contains(low, "error") || strings.Contains(low, "failed") ||
		strings.Contains(low, "did not go out") || strings.Contains(low, "cannot ") || strings.Contains(low, "could not"):
		return "error"
	case has("login", "web login", "web logout", "sign-up", "renew login", "mail", "sweep web sessions"):
		return "login"
	case has("device ", "publish "):
		return "device"
	case has("op ", "operator ", "admin:", "account "):
		return "op"
	}
	return "other"
}

// What a line may carry that the console never shows.
var (
	// An address with its port: what devices report of where they are
	// (and connections seen from one), as 203.0.113.7:41641 or
	// [2001:db8::7]:41641. A source address alone stays: the log's
	// whom-from, which the privacy statement names.
	logAddrPort = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}:\d{1,5}\b|\[[0-9A-Fa-f:.%]+\]:\d{1,5}`)
	// A token, a key or a hash: a long run of base64url or hex.
	logToken = regexp.MustCompile(`[A-Za-z0-9_-]{32,}`)
	// A link's token, after the # of its URL (a login link).
	logFragment = regexp.MustCompile(`(https?://[^\s#]*)#[A-Za-z0-9_-]{8,}`)
	// A code: "code 123456", "the login code for x is 123456" (a
	// development server's log).
	logCode = regexp.MustCompile(`(?i)(\bcode\s+|\bcode\b[^0-9\n]{0,60}?\bis\s+)\d{6}\b`)
)

// redactLog masks what the console never shows.
func redactLog(s string) string {
	s = logFragment.ReplaceAllString(s, "${1}#‹…›")
	s = logAddrPort.ReplaceAllString(s, "‹地址›")
	s = logCode.ReplaceAllString(s, "${1}‹码›")
	return logToken.ReplaceAllString(s, "‹…›")
}
