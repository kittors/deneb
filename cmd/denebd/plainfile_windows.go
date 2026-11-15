//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// readPlain reads path whole, at most max bytes (plainfile.go): a path on
// one of this computer's own drives (localDrivePath; not a mapped share),
// with no link or junction anywhere on the way, which could lead to a
// share; a file on the disk, not a device, with no other name.
func readPlain(path string, max int64) ([]byte, error) {
	name := filepath.Base(path)
	if err := localDrivePath(path); err != nil {
		return nil, err
	}
	root, err := windows.UTF16PtrFromString(path[:3])
	if err != nil {
		return nil, err
	}
	switch windows.GetDriveType(root) {
	case windows.DRIVE_FIXED, windows.DRIVE_REMOVABLE, windows.DRIVE_RAMDISK:
	default:
		return nil, fmt.Errorf("%w：%s 不是本机的磁盘", errNotPlain, path[:2])
	}
	// Each name on the way, without following it.
	at := path[:2]
	var last os.FileInfo
	for _, part := range strings.Split(path[3:], `\`) {
		at += `\` + part
		fi, err := os.Lstat(at)
		if err != nil {
			return nil, err
		}
		a, ok := fi.Sys().(*syscall.Win32FileAttributeData)
		if !ok || a.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return nil, fmt.Errorf("%w：%s 是个链接", errNotPlain, at)
		}
		last = fi
	}
	if !last.Mode().IsRegular() {
		return nil, fmt.Errorf("%w：%s 不是普通文件", errNotPlain, name)
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_SEQUENTIAL_SCAN, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	f := os.NewFile(uintptr(h), path)
	defer f.Close()
	// What was opened, now: a file on the disk with no other name, still on
	// this computer (a name on the way changed for a link meanwhile).
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return nil, &os.PathError{Op: "stat", Path: path, Err: err}
	}
	if t, err := windows.GetFileType(h); err != nil || t != windows.FILE_TYPE_DISK ||
		info.FileAttributes&(windows.FILE_ATTRIBUTE_DIRECTORY|windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DEVICE) != 0 {
		return nil, fmt.Errorf("%w：%s 不是普通文件", errNotPlain, name)
	}
	if info.NumberOfLinks != 1 {
		return nil, fmt.Errorf("%w：%s 还有别的名字（硬链接）", errNotPlain, name)
	}
	if int64(info.FileSizeHigh)<<32|int64(info.FileSizeLow) > max {
		return nil, fmt.Errorf("%w：%s 太大", errNotPlain, name)
	}
	buf := make([]uint16, 32768)
	n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), 0)
	if err != nil || int(n) >= len(buf) {
		return nil, fmt.Errorf("%w：%s 找不到在哪", errNotPlain, name)
	}
	if final := windows.UTF16ToString(buf[:n]); strings.HasPrefix(strings.ToUpper(final), `\\?\UNC\`) {
		return nil, fmt.Errorf("%w：%s 在别的电脑上", errNotPlain, name)
	}
	return readUpTo(f, max, name)
}

// writePrivate writes b to path, a new file only SYSTEM and administrators
// may open: what is not checked yet is in no one else's reach.
func writePrivate(path string, b []byte) error {
	os.Remove(path)
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;SY)(A;;FA;;;BA)")
	if err != nil {
		return err
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	sa := &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	h, err := windows.CreateFile(p, windows.GENERIC_WRITE, 0, sa, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return &os.PathError{Op: "create", Path: path, Err: err}
	}
	f := os.NewFile(uintptr(h), path)
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
