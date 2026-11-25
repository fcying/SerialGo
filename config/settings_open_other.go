//go:build !linux && !windows

package config

import (
	"errors"
	"os"
)

func pinSettingsDirectory(dir string) (string, *os.Root, func() error, error) {
	return "", nil, nil, &os.PathError{Op: "open", Path: dir, Err: errors.New("safe configuration directory pinning is unsupported on this platform")}
}

func verifyExecutableDirectory(store *Store, exe string) error {
	return errors.New("safe executable verification is unsupported on this platform")
}

func loadStoreFile(store *Store, opened **os.File) (Settings, string, error) {
	return defaults(), "", errors.New("safe configuration loading is unsupported on this platform")
}

func saveStoreFile(store *Store, settings Settings, opened **os.File) error {
	return errors.New("safe configuration saving is unsupported on this platform")
}

func openStoreTarget(store *Store) (*os.File, error) {
	return nil, errors.New("safe configuration access is unsupported on this platform")
}

func lockStore(store *Store) (func(), error) {
	return nil, errors.New("safe configuration locking is unsupported on this platform")
}

func storeOutputIdentity(store *Store) (os.FileInfo, os.FileInfo, string, error) {
	return nil, nil, "", errors.New("safe configuration inspection is unsupported on this platform")
}

func sameSettingsDirectory(a, b string) bool {
	return a == b
}

func openSettingsFile(path string) (*os.File, error) {
	return nil, &os.PathError{Op: "open", Path: path, Err: errors.New("safe configuration open is unsupported on this platform")}
}

func installSettingsFile(source, target string) error {
	return os.Link(source, target)
}

func duplicateSettingsFile(file *os.File) (*os.File, error) {
	return nil, errors.New("safe configuration file duplication is unsupported on this platform")
}

func createSettingsTemp(dir string) (*os.File, error) {
	return os.CreateTemp(dir, ".settings-*")
}
