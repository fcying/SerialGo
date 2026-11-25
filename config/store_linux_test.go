//go:build linux

package config

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestStorePinsDirectoryAcrossReplacement(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "create", true: "existing"}[existing], func(t *testing.T) {
			root := t.TempDir()
			dir, moved, outside := filepath.Join(root, "app"), filepath.Join(root, "moved"), filepath.Join(root, "outside")
			for _, path := range []string{dir, outside} {
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			original := []byte("SendText = \"outside\"\n")
			if err := os.WriteFile(filepath.Join(outside, "config.toml"), original, 0600); err != nil {
				t.Fatal(err)
			}
			if existing {
				if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("SendText = \"inside\"\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			store, err := openStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if err := os.Rename(dir, moved); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, dir); err != nil {
				t.Fatal(err)
			}
			settings, path, file, err := store.LoadWithIdentity()
			if file != nil {
				file.Close()
			}
			if err != nil || path != filepath.Join(dir, "config.toml") {
				t.Fatalf("load: path=%q err=%v", path, err)
			}
			if existing && settings.SendText != "inside" {
				t.Fatalf("read substituted directory: %q", settings.SendText)
			}
			settings.SendText = "updated"
			file, err = store.SaveWithIdentity(path, settings)
			if file != nil {
				file.Close()
			}
			if err != nil {
				t.Fatal(err)
			}
			got := defaults()
			if err := decode(filepath.Join(moved, "config.toml"), &got); err != nil || got.SendText != "updated" {
				t.Fatalf("pinned configuration: %q err=%v", got.SendText, err)
			}
			if got, err := os.ReadFile(filepath.Join(outside, "config.toml")); err != nil || !bytes.Equal(got, original) {
				t.Fatalf("outside configuration changed: %q err=%v", got, err)
			}
		})
	}
}

func TestStoreFollowsAbsoluteSameDirectorySymlinkAfterReplacement(t *testing.T) {
	root := t.TempDir()
	dir, moved, outside := filepath.Join(root, "app"), filepath.Join(root, "moved"), filepath.Join(root, "outside")
	for _, path := range []string{dir, outside} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "actual.toml"), []byte("SendText = \"inside\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "actual.toml"), filepath.Join(dir, "config.toml")); err != nil {
		t.Fatal(err)
	}
	store, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := os.Rename(dir, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, dir); err != nil {
		t.Fatal(err)
	}
	settings, path, file, err := store.LoadWithIdentity()
	if file != nil {
		file.Close()
	}
	if err != nil || settings.SendText != "inside" {
		t.Fatalf("load: %q err=%v", settings.SendText, err)
	}
	settings.SendText = "updated"
	file, err = store.SaveWithIdentity(path, settings)
	if file != nil {
		file.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	got := defaults()
	if err := decode(filepath.Join(moved, "actual.toml"), &got); err != nil || got.SendText != "updated" {
		t.Fatalf("save: %q err=%v", got.SendText, err)
	}
	if _, err := os.Lstat(filepath.Join(moved, "config.toml")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(outside, "actual.toml")); !os.IsNotExist(err) {
		t.Fatalf("outside target accessed: %v", err)
	}
}

func TestStoreOutputIdentityTracksReplacementAndMissingName(t *testing.T) {
	root := t.TempDir()
	dir, moved := filepath.Join(root, "app"), filepath.Join(root, "moved")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := os.Rename(dir, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("decoy"), 0600); err != nil {
		t.Fatal(err)
	}
	movedConfig := filepath.Join(moved, "config.toml")
	if err := os.Remove(movedConfig); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(movedConfig, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	pinnedDir, current, name, err := store.OutputIdentity()
	wantDir, dirErr := os.Stat(moved)
	wantFile, fileErr := os.Stat(movedConfig)
	if err != nil || dirErr != nil || fileErr != nil || name != "config.toml" || !os.SameFile(pinnedDir, wantDir) || !os.SameFile(current, wantFile) {
		t.Fatalf("replacement identity: dir=%v file=%v name=%q err=%v", pinnedDir, current, name, err)
	}
	if err := os.Remove(movedConfig); err != nil {
		t.Fatal(err)
	}
	pinnedDir, current, name, err = store.OutputIdentity()
	actual := filepath.Join(moved, "actual.toml")
	if err := os.WriteFile(actual, []byte("linked"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "actual.toml"), movedConfig); err != nil {
		t.Fatal(err)
	}
	_, linked, linkedName, linkErr := store.OutputIdentity()
	wantLinked, statErr := os.Stat(actual)
	if linkErr != nil || statErr != nil || linkedName != "actual.toml" || !os.SameFile(linked, wantLinked) {
		t.Fatalf("linked identity: file=%v name=%q err=%v", linked, linkedName, linkErr)
	}
	if err := os.Remove(movedConfig); err != nil {
		t.Fatal(err)
	}
	if err != nil || current != nil || name != "config.toml" || !os.SameFile(pinnedDir, wantDir) {
		t.Fatalf("missing identity: dir=%v file=%v name=%q err=%v", pinnedDir, current, name, err)
	}
}

func TestStoreLoadsInReadOnlyDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("SendText = \"read only\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0700)
	probe := filepath.Join(dir, "probe")
	if err := os.WriteFile(probe, nil, 0600); err == nil {
		os.Remove(probe)
		t.Skip("process can write despite directory permissions")
	}
	store, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	settings, loadedPath, file, err := store.LoadWithIdentity()
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
	if loadedPath != path || settings.SendText != "read only" {
		t.Fatalf("load: path=%q text=%q", loadedPath, settings.SendText)
	}
	file, err = store.SaveWithIdentity(path, settings)
	if file != nil {
		file.Close()
	}
	if err == nil {
		t.Fatal("saved in read-only directory")
	}
}
