//go:build linux

package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestLoadRejectsFIFOWithoutBlocking(t *testing.T) {
	for _, symlink := range []bool{false, true} {
		name := "direct"
		if symlink {
			name = "symlink"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.toml")
			fifo := path
			if symlink {
				fifo = filepath.Join(dir, "target.toml")
			}
			if err := unix.Mkfifo(fifo, 0600); err != nil {
				t.Fatal(err)
			}
			if symlink {
				makeConfigSymlink(t, "target.toml", path)
			}
			type result struct {
				path string
				err  error
			}
			done := make(chan result, 1)
			go func() {
				_, loadedPath, err := load(path)
				done <- result{loadedPath, err}
			}()
			select {
			case got := <-done:
				if got.err == nil || got.path != "" {
					t.Fatalf("load FIFO: path=%q err=%v", got.path, got.err)
				}
			case <-time.After(2 * time.Second):
				// Release a reader blocked by a regression before failing the test.
				fd, err := unix.Open(fifo, unix.O_WRONLY|unix.O_NONBLOCK, 0)
				if err == nil {
					unix.Close(fd)
				}
				t.Fatal("load blocked opening a FIFO")
			}
			info, err := os.Lstat(fifo)
			if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
				t.Fatalf("FIFO changed: info=%v err=%v", info, err)
			}
		})
	}
}

func TestDecodeRejectsTargetReplacedByFIFO(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("SendText = \"original\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	resolved, err := settingsTarget(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		settings := defaults()
		done <- decode(resolved, &settings)
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("decode accepted substituted FIFO")
		}
	case <-time.After(2 * time.Second):
		fd, err := unix.Open(path, unix.O_WRONLY|unix.O_NONBLOCK, 0)
		if err == nil {
			unix.Close(fd)
		}
		t.Fatal("decode blocked opening a substituted FIFO")
	}
}

func TestInstallSettingsFileFallback(t *testing.T) {
	for _, unsupported := range []error{unix.ENOSYS, unix.EINVAL, unix.EOPNOTSUPP} {
		t.Run(unsupported.Error(), func(t *testing.T) {
			dir := t.TempDir()
			source := filepath.Join(dir, "temporary")
			target := filepath.Join(dir, "settings")
			if err := os.WriteFile(source, []byte("new settings"), 0600); err != nil {
				t.Fatal(err)
			}
			rename := func(olddir int, old string, newdir int, new string, flags uint) error {
				if olddir != unix.AT_FDCWD || old != source || newdir != unix.AT_FDCWD || new != target || flags != unix.RENAME_NOREPLACE {
					t.Fatalf("unexpected rename arguments: %d %q %d %q %d", olddir, old, newdir, new, flags)
				}
				return unsupported
			}
			if err := installSettingsFileWith(source, target, rename, os.Link); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(target)
			if err != nil || string(data) != "new settings" {
				t.Fatalf("installed contents = %q, err = %v", data, err)
			}
			if err := os.Remove(source); err != nil {
				t.Fatal(err)
			}
			data, err = os.ReadFile(target)
			if err != nil || string(data) != "new settings" {
				t.Fatalf("installed contents after temporary cleanup = %q, err = %v", data, err)
			}
		})
	}
}

func TestInstallSettingsFileDoesNotReplaceConcurrentTarget(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "temporary")
	target := filepath.Join(dir, "settings")
	if err := os.WriteFile(source, []byte("new settings"), 0600); err != nil {
		t.Fatal(err)
	}
	rename := func(_ int, _ string, _ int, _ string, _ uint) error {
		if err := os.WriteFile(target, []byte("concurrent settings"), 0600); err != nil {
			t.Fatal(err)
		}
		return unix.ENOSYS
	}
	err := installSettingsFileWith(source, target, rename, os.Link)
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("install error = %v, want EEXIST", err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "concurrent settings" {
		t.Fatalf("target contents = %q, err = %v", data, err)
	}
}

func TestInstallSettingsFileDoesNotFallbackOnExistingTarget(t *testing.T) {
	rename := func(_ int, _ string, _ int, _ string, _ uint) error { return unix.EEXIST }
	link := func(_, _ string) error {
		t.Fatal("unexpected fallback after EEXIST")
		return nil
	}
	if err := installSettingsFileWith("source", "target", rename, link); !errors.Is(err, os.ErrExist) {
		t.Fatalf("install error = %v, want EEXIST", err)
	}
}

func TestInstallSettingsFileReportsUnsupportedFallback(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "temporary")
	target := filepath.Join(dir, "settings")
	if err := os.WriteFile(source, []byte("new settings"), 0600); err != nil {
		t.Fatal(err)
	}
	rename := func(_ int, _ string, _ int, _ string, _ uint) error { return unix.ENOSYS }
	link := func(old, new string) error { return &os.LinkError{Op: "link", Old: old, New: new, Err: unix.EOPNOTSUPP} }
	if err := installSettingsFileWith(source, target, rename, link); !errors.Is(err, unix.EOPNOTSUPP) {
		t.Fatalf("install error = %v, want EOPNOTSUPP", err)
	}
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target should not exist: %v", err)
	}
	data, err := os.ReadFile(source)
	if err != nil || string(data) != "new settings" {
		t.Fatalf("temporary contents = %q, err = %v", data, err)
	}
}
