// Package logfile opens a log file the same way for the window that starts
// an engine and for the engine that rolls it over (cmd/denebd): appended
// to, for its user only, and on Windows such that it can be renamed while
// it is open, as rolling it over needs. Go's os.OpenFile does not allow
// that there: it leaves FILE_SHARE_DELETE out.
package logfile
