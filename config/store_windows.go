//go:build windows

package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"github.com/fcying/SerialGo/internal/i18n"

	"github.com/BurntSushi/toml"
	"golang.org/x/sys/windows"
)

// Resolve each link relative to the pinned directory, never through its old path.
func (s *Store) target() (string, error) {
	name := "config.toml"
	seen := make(map[string]bool)
	for {
		if seen[name] {
			return "", fmt.Errorf("configuration symlink cycle: %s", name)
		}
		seen[name] = true
		info, err := s.root.Lstat(name)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) && len(seen) == 1 {
				return name, nil
			}
			return "", fmt.Errorf("inspect configuration target %s: %w", name, err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			return name, nil
		}
		link, err := s.root.Readlink(name)
		if err != nil {
			return "", err
		}
		if filepath.IsAbs(link) {
			linkDir, logicalDir := filepath.Clean(filepath.Dir(link)), filepath.Clean(filepath.Dir(s.path))
			if linkDir != logicalDir {
				if !strings.EqualFold(linkDir, logicalDir) {
					return "", fmt.Errorf("configuration symlink points outside its directory: %s", name)
				}
				candidate, candidateErr := os.Stat(linkDir)
				pinned, pinnedErr := s.root.Stat(".")
				if candidateErr != nil || pinnedErr != nil || !os.SameFile(candidate, pinned) {
					return "", fmt.Errorf("configuration symlink points outside its directory: %s", name)
				}
			}
			name = filepath.Base(link)
		} else {
			if filepath.Dir(link) != "." || filepath.Clean(link) == "." || filepath.Clean(link) == ".." {
				return "", fmt.Errorf("configuration symlink points outside its directory: %s", name)
			}
			name = link
		}
	}
}

func storeOutputIdentity(s *Store) (os.FileInfo, os.FileInfo, string, error) {
	dir, err := s.root.Stat(".")
	if err != nil {
		return nil, nil, "", err
	}
	name, err := s.target()
	if err != nil {
		return nil, nil, "", err
	}
	info, err := s.root.Stat(name)
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	return dir, info, name, err
}

// Open the final component itself, so a link substituted after target() cannot
// redirect the read, even to another file within the directory.
func (s *Store) openTarget(name string) (*os.File, error) {
	dir, err := s.root.Open(".")
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return nil, err
	}
	oa := windows.OBJECT_ATTRIBUTES{RootDirectory: windows.Handle(dir.Fd()), ObjectName: objectName, Attributes: windows.OBJ_CASE_INSENSITIVE}
	oa.Length = uint32(unsafe.Sizeof(oa))
	var h windows.Handle
	var iosb windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(&h, windows.FILE_GENERIC_READ, &oa, &iosb, nil, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		windows.FILE_OPEN, windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT, 0, 0)
	if err != nil {
		if status, ok := err.(windows.NTStatus); ok {
			err = status.Errno()
		}
		return nil, &os.PathError{Op: "open", Path: name, Err: err}
	}
	var info windows.ByHandleFileInformation
	err = windows.GetFileInformationByHandle(h, &info)
	if err == nil && (info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY)) != 0 {
		err = errors.New("configuration target is not a regular file")
	}
	if err != nil {
		windows.CloseHandle(h)
		return nil, &os.PathError{Op: "open", Path: name, Err: err}
	}
	return os.NewFile(uintptr(h), name), nil
}

func loadStoreFile(s *Store, opened **os.File) (Settings, string, error) {
	settings := defaults()
	if defaultSettingsErr != nil {
		return settings, "", defaultSettingsErr
	}
	name, err := s.target()
	if err != nil {
		return settings, "", err
	}
	file, err := s.openTarget(name)
	if errors.Is(err, os.ErrNotExist) {
		if err := saveRootSettings(s, settings, true, opened); err != nil {
			return defaults(), "", err
		}
		return settings, s.path, nil
	}
	if err != nil {
		return defaults(), "", err
	}
	*opened = file
	if err := decodeSettings(file, &settings); err != nil {
		return defaults(), "", err
	}
	return settings, s.path, nil
}

func saveStoreFile(s *Store, settings Settings, opened **os.File) error {
	return saveRootSettings(s, settings, false, opened)
}

func openStoreTarget(s *Store) (*os.File, error) {
	name, err := s.target()
	if err != nil {
		return nil, err
	}
	return s.openTarget(name)
}

// A persistent sidecar serializes cooperating instances, not directory writers
// that replace the lock or target while an operation is in progress.
func lockStore(s *Store) (func(), error) {
	dir, err := s.root.Open(".")
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	name, err := windows.NewNTUnicodeString(".config.toml.lock")
	if err != nil {
		return nil, err
	}
	oa := windows.OBJECT_ATTRIBUTES{RootDirectory: windows.Handle(dir.Fd()), ObjectName: name, Attributes: windows.OBJ_CASE_INSENSITIVE}
	oa.Length = uint32(unsafe.Sizeof(oa))
	var h windows.Handle
	var iosb windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(&h, windows.FILE_GENERIC_READ|windows.FILE_GENERIC_WRITE, &oa, &iosb, nil, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		windows.FILE_OPEN_IF, windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT, 0, 0)
	if err != nil {
		return nil, err
	}
	var info windows.ByHandleFileInformation
	err = windows.GetFileInformationByHandle(h, &info)
	if err == nil && info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		err = errors.New("configuration lock is not a regular file")
	}
	if err == nil {
		var target *os.File
		target, err = openStoreTarget(s)
		if err == nil {
			var targetInfo windows.ByHandleFileInformation
			err = windows.GetFileInformationByHandle(windows.Handle(target.Fd()), &targetInfo)
			if err == nil && info.VolumeSerialNumber == targetInfo.VolumeSerialNumber &&
				info.FileIndexHigh == targetInfo.FileIndexHigh && info.FileIndexLow == targetInfo.FileIndexLow {
				err = i18n.NewError("配置文件不能与配置锁文件相同, 请将配置移至其他文件", "The configuration must not be the lock file; move the configuration to another file")
			}
			target.Close()
		} else if errors.Is(err, os.ErrNotExist) {
			err = nil
		}
	}
	var overlapped windows.Overlapped
	if err == nil {
		deadline := time.Now().Add(time.Second)
		for {
			err = windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped)
			if !errors.Is(err, windows.ERROR_LOCK_VIOLATION) || time.Now().After(deadline) {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			err = configurationLockTimeout(err)
		}
	}
	if err != nil {
		windows.CloseHandle(h)
		return nil, err
	}
	return func() {
		windows.UnlockFileEx(h, 0, 1, 0, &overlapped)
		windows.CloseHandle(h)
	}, nil
}

func saveRootSettings(s *Store, settings Settings, createOnly bool, opened **os.File) error {
	if err := prepareSettings(&settings); err != nil {
		return err
	}
	name := "config.toml"
	if !createOnly {
		var err error
		name, err = s.target()
		if err != nil {
			return err
		}
	}
	var file *os.File
	var temp string
	for range 10000 {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return err
		}
		temp = ".settings-" + hex.EncodeToString(random[:])
		var err error
		file, err = s.root.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return err
		}
		break
	}
	if file == nil {
		return &os.PathError{Op: "createtemp", Path: s.path, Err: os.ErrExist}
	}
	defer s.root.Remove(temp)
	presets := settings.Presets
	if presets == nil {
		presets = []Preset{}
	}
	writeErr := toml.NewEncoder(file).Encode(fileSettings{Settings: &settings, Presets: presets})
	var duplicate *os.File
	var dupErr error
	if writeErr == nil && opened != nil {
		duplicate, dupErr = duplicateSettingsFile(file)
	}
	closeErr := file.Close()
	if err := errors.Join(writeErr, dupErr, closeErr); err != nil {
		if duplicate != nil {
			duplicate.Close()
		}
		return err
	}
	var err error
	if createOnly {
		err = renameRootNoReplace(s.root, temp, name)
	} else {
		err = s.root.Rename(temp, name)
	}
	if err != nil {
		if duplicate != nil {
			duplicate.Close()
		}
		return err
	}
	if opened != nil {
		*opened = duplicate
	}
	return nil
}

// FileRenameInformation performs an exclusive same-directory rename without
// requiring hard-link support. Both names are resolved by the pinned handle.
func renameRootNoReplace(root *os.Root, oldName, newName string) error {
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	objectName, err := windows.NewNTUnicodeString(oldName)
	if err != nil {
		return err
	}
	oa := windows.OBJECT_ATTRIBUTES{RootDirectory: windows.Handle(dir.Fd()), ObjectName: objectName, Attributes: windows.OBJ_CASE_INSENSITIVE}
	oa.Length = uint32(unsafe.Sizeof(oa))
	var h windows.Handle
	var iosb windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(&h, windows.DELETE|windows.SYNCHRONIZE, &oa, &iosb, nil, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		windows.FILE_OPEN, windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT, 0, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	type renameInfo struct {
		ReplaceIfExists byte
		RootDirectory windows.Handle
		FileNameLength uint32
		FileName [1]uint16
	}
	utf16Name, err := windows.UTF16FromString(newName)
	if err != nil {
		return err
	}
	var layout renameInfo
	nameBytes := (len(utf16Name) - 1) * 2
	buffer := make([]byte, int(unsafe.Offsetof(layout.FileName))+nameBytes)
	info := (*renameInfo)(unsafe.Pointer(&buffer[0]))
	info.RootDirectory = windows.Handle(dir.Fd())
	info.FileNameLength = uint32(nameBytes)
	copy(unsafe.Slice(&info.FileName[0], len(utf16Name)-1), utf16Name)
	err = windows.NtSetInformationFile(h, &iosb, &buffer[0], uint32(len(buffer)), windows.FileRenameInformation)
	if status, ok := err.(windows.NTStatus); ok {
		return status.Errno()
	}
	return err
}
