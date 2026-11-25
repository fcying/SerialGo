//go:build windows

package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// Root operations remain bound to the directory even if its path is replaced.
func pinSettingsDirectory(dir string) (string, *os.Root, func() error, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", nil, nil, err
	}
	return dir, root, root.Close, nil
}

func verifyExecutableDirectory(store *Store, exe string) error {
	original, err := os.Stat(exe)
	if err != nil {
		return err
	}
	candidate, err := store.root.Stat(filepath.Base(exe))
	if err != nil {
		return err
	}
	if !os.SameFile(original, candidate) {
		return errors.New("executable directory changed while opening settings")
	}
	return nil
}

func sameSettingsDirectory(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if a == b {
		return true
	}
	if !strings.EqualFold(a, b) {
		return false
	}
	first, err := os.Stat(a)
	if err != nil {
		return false
	}
	second, err := os.Stat(b)
	return err == nil && os.SameFile(first, second)
}

// Open the reparse point itself, then reject it before reading. This prevents
// a final component replaced after settingsTarget from redirecting the read.
func openSettingsFile(path string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	h, err := windows.CreateFile(name, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	var info windows.ByHandleFileInformation
	err = windows.GetFileInformationByHandle(h, &info)
	if err == nil && info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		err = errors.New("configuration target is a reparse point")
	}
	if err != nil {
		windows.CloseHandle(h)
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(h), path), nil
}

// MoveFile fails if the destination already exists, unlike a replacing rename.
func installSettingsFile(source, target string) error {
	from, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return &os.LinkError{Op: "rename", Old: source, New: target, Err: err}
	}
	to, err := windows.UTF16PtrFromString(target)
	if err == nil {
		err = windows.MoveFile(from, to)
	}
	if err != nil {
		return &os.LinkError{Op: "rename", Old: source, New: target, Err: err}
	}
	return nil
}

func duplicateSettingsFile(file *os.File) (*os.File, error) {
	process, err := windows.GetCurrentProcess()
	if err != nil {
		return nil, err
	}
	var handle windows.Handle
	if err := windows.DuplicateHandle(process, windows.Handle(file.Fd()), process, &handle, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(handle), file.Name()), nil
}

// The published file may remain open for identity tracking, so its original
// handle must allow deletion/renaming. os.CreateTemp does not grant that share.
func createSettingsTemp(dir string) (*os.File, error) {
	for range 10000 {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return nil, err
		}
		path := filepath.Join(dir, ".settings-"+hex.EncodeToString(random[:]))
		name, err := windows.UTF16PtrFromString(path)
		if err != nil {
			return nil, &os.PathError{Op: "createtemp", Path: path, Err: err}
		}
		h, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
			nil, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
		if errors.Is(err, windows.ERROR_FILE_EXISTS) || errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			continue
		}
		if err != nil {
			return nil, &os.PathError{Op: "createtemp", Path: path, Err: err}
		}
		return os.NewFile(uintptr(h), path), nil
	}
	return nil, &os.PathError{Op: "createtemp", Path: dir, Err: windows.ERROR_FILE_EXISTS}
}
