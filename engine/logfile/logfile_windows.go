package logfile

import (
	"os"

	"golang.org/x/sys/windows"
)

// Open opens path to append to, creating it, so that it can be renamed
// (and the file once renamed replaced) while this handle, or one an engine
// inherits from it, is open. The new file takes the folder's permissions:
// %LOCALAPPDATA%'s are this user's, SYSTEM's and the administrators'.
func Open(path string) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	h, err := windows.CreateFile(p,
		windows.FILE_APPEND_DATA|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(h), path), nil
}
