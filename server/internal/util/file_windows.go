//go:build windows

package util

import (
	"io"
	"os"

	"golang.org/x/sys/windows"
)

// OpenShared opens a file for reading while another program may replace or
// delete it: os.Open locks the file's name on Windows, so that renaming a
// new version over it (as ffmpeg does with its playlists) fails meanwhile.
func OpenShared(name string) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: name, Err: err}
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: name, Err: err}
	}
	return os.NewFile(uintptr(h), name), nil
}

// ReadFileShared reads a file another program may replace or delete
// meanwhile.
func ReadFileShared(name string) ([]byte, error) {
	f, err := OpenShared(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}
