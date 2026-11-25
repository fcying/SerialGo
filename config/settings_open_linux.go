//go:build linux

package config

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"golang.org/x/sys/unix"
)

func pinSettingsDirectory(dir string) (string, *os.Root, func() error, error) {
	fd, err := unix.Open(dir, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return "", nil, nil, &os.PathError{Op: "open", Path: dir, Err: err}
	}
	file := os.NewFile(uintptr(fd), dir)
	return "/proc/self/fd/" + strconv.Itoa(fd), nil, file.Close, nil
}

func verifyExecutableDirectory(store *Store, exe string) error {
	running, err := os.Stat("/proc/self/exe")
	if err != nil {
		return err
	}
	candidate, err := os.Stat(filepath.Join(store.dir, filepath.Base(exe)))
	if err != nil {
		return err
	}
	if !os.SameFile(running, candidate) {
		return errors.New("executable directory changed while opening settings")
	}
	return nil
}

func loadStoreFile(store *Store, opened **os.File) (Settings, string, error) {
	return loadFileInDir(filepath.Join(store.dir, "config.toml"), filepath.Dir(store.path), opened)
}

func saveStoreFile(store *Store, settings Settings, opened **os.File) error {
	return saveFileInDir(filepath.Join(store.dir, "config.toml"), filepath.Dir(store.path), settings, false, opened)
}

func openStoreTarget(s *Store) (*os.File, error) {
	target, err := settingsTargetInDir(filepath.Join(s.dir, "config.toml"), filepath.Dir(s.path))
	if err != nil {
		return nil, err
	}
	return openSettingsFile(target)
}

// The persistent sidecar serializes cooperating saves. Initial creation uses
// a no-replace install instead. Directory writers can replace either name.
func lockStore(s *Store) (func(), error) {
	path := filepath.Join(s.dir, ".config.toml.lock")
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	info, err := file.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = errors.New("configuration lock is not a regular file")
	}
	if err == nil {
		deadline := time.Now().Add(time.Second)
		for {
			err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
			if !errors.Is(err, unix.EWOULDBLOCK) || time.Now().After(deadline) {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if errors.Is(err, unix.EWOULDBLOCK) {
			err = configurationLockTimeout(err)
		}
	}
	if err != nil {
		file.Close()
		return nil, err
	}
	return func() {
		unix.Flock(fd, unix.LOCK_UN)
		file.Close()
	}, nil
}

func storeOutputIdentity(s *Store) (os.FileInfo, os.FileInfo, string, error) {
	dir, err := os.Stat(s.dir)
	if err != nil {
		return nil, nil, "", err
	}
	target, err := settingsTargetInDir(filepath.Join(s.dir, "config.toml"), filepath.Dir(s.path))
	if err != nil {
		return nil, nil, "", err
	}
	info, err := os.Stat(target)
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	return dir, info, filepath.Base(target), err
}

func sameSettingsDirectory(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}

// openSettingsFile opens the resolved final component without following a link
// substituted after settingsTarget inspected it.
func openSettingsFile(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	file := os.NewFile(uintptr(fd), path)
	info, err := file.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = errors.New("configuration target is not a regular file")
	}
	if err != nil {
		file.Close()
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return file, nil
}

// Rename without replacement publishes the complete file without requiring hard links.
func installSettingsFile(source, target string) error {
	return installSettingsFileWith(source, target, unix.Renameat2, os.Link)
}

// A hard link also publishes atomically without replacing an existing target.
// The caller removes the temporary source after this function returns.
func installSettingsFileWith(source, target string, rename func(int, string, int, string, uint) error, link func(string, string) error) error {
	err := rename(unix.AT_FDCWD, source, unix.AT_FDCWD, target, unix.RENAME_NOREPLACE)
	if err == nil {
		return nil
	}
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.EOPNOTSUPP) {
		return link(source, target)
	}
	return &os.LinkError{Op: "rename", Old: source, New: target, Err: err}
}

func duplicateSettingsFile(file *os.File) (*os.File, error) {
	fd, err := unix.FcntlInt(file.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), file.Name()), nil
}

func createSettingsTemp(dir string) (*os.File, error) {
	return os.CreateTemp(dir, ".settings-*")
}
