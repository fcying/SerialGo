//go:build windows

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"github.com/fcying/SerialGo/internal/i18n"

	"golang.org/x/sys/windows"
)

func TestStorePreventsDirectoryReplacement(t *testing.T) {
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
	store, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	pinnedPath, outsidePath := dir, outside
	if err := os.Rename(dir, moved); err == nil {
		pinnedPath = moved
		if err := os.Symlink(outside, dir); err != nil {
			// Exercise replacement even on systems without symlink privileges.
			if err := os.Rename(outside, dir); err != nil {
				t.Fatal(err)
			}
			outsidePath = dir
		}
	}
	directory, current, name, err := store.OutputIdentity()
	wantDirectory, dirErr := os.Stat(pinnedPath)
	if err != nil || dirErr != nil || current != nil || name != "config.toml" || !os.SameFile(directory, wantDirectory) {
		t.Fatalf("missing pinned configuration identity: dir=%v file=%v name=%q err=%v", directory, current, name, err)
	}
	settings, path, file, err := store.LoadWithIdentity()
	if file != nil {
		file.Close()
	}
	if err != nil {
		t.Fatal(err)
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
	if err := decode(filepath.Join(pinnedPath, "config.toml"), &got); err != nil || got.SendText != "updated" {
		t.Fatalf("pinned configuration: %q err=%v", got.SendText, err)
	}
	directory, current, name, err = store.OutputIdentity()
	wantFile, fileErr := os.Stat(filepath.Join(pinnedPath, "config.toml"))
	if err != nil || fileErr != nil || name != "config.toml" || !os.SameFile(directory, wantDirectory) || !os.SameFile(current, wantFile) {
		t.Fatalf("saved pinned configuration identity: dir=%v file=%v name=%q err=%v", directory, current, name, err)
	}
	if got, err := os.ReadFile(filepath.Join(outsidePath, "config.toml")); err != nil || string(got) != string(original) {
		t.Fatalf("outside configuration changed: %q err=%v", got, err)
	}
}

func TestStoreOutputIdentityTracksPinnedConfiguration(t *testing.T) {
	for _, scenario := range []string{"replacement", "missing", "relative symlink", "absolute symlink"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			logical := filepath.Join(root, "app")
			moved := filepath.Join(root, "moved")
			if err := os.Mkdir(logical, 0700); err != nil {
				t.Fatal(err)
			}
			originalPath := filepath.Join(logical, "config.toml")
			if err := os.WriteFile(originalPath, []byte("original"), 0600); err != nil {
				t.Fatal(err)
			}
			store, err := openStore(logical)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			pinned := logical
			if err := os.Rename(logical, moved); err == nil {
				pinned = moved
				if err := os.Mkdir(logical, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(originalPath, []byte("decoy"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			configPath := filepath.Join(pinned, "config.toml")
			oldFile, err := store.openTarget("config.toml")
			if err != nil {
				t.Fatal(err)
			}
			defer oldFile.Close()
			old, err := oldFile.Stat()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(configPath, filepath.Join(pinned, "old.toml")); err != nil {
				t.Fatal(err)
			}
			name := "config.toml"
			var expected os.FileInfo
			switch scenario {
			case "replacement":
				if err := os.WriteFile(configPath, []byte("replacement"), 0600); err != nil {
					t.Fatal(err)
				}
				expected, err = os.Stat(configPath)
			case "relative symlink", "absolute symlink":
				name = "actual.toml"
				actual := filepath.Join(pinned, name)
				if err := os.WriteFile(actual, []byte("linked"), 0600); err != nil {
					t.Fatal(err)
				}
				link := name
				if scenario == "absolute symlink" {
					link = filepath.Join(logical, name)
				}
				makeConfigSymlink(t, link, configPath)
				expected, err = os.Stat(actual)
			}
			if err != nil {
				t.Fatal(err)
			}
			wantDir, err := os.Stat(pinned)
			if err != nil {
				t.Fatal(err)
			}
			dir, current, gotName, err := store.OutputIdentity()
			if err != nil || dir == nil || !os.SameFile(dir, wantDir) || gotName != name {
				t.Fatalf("pinned identity: dir=%v name=%q err=%v", dir, gotName, err)
			}
			if expected == nil {
				if current != nil {
					t.Fatalf("missing configuration has identity: %v", current)
				}
			} else if current == nil || !os.SameFile(current, expected) || os.SameFile(current, old) {
				t.Fatalf("current identity = %v; want %v, not original %v", current, expected, old)
			}
		})
	}
}

func TestStoreOutputIdentityRemainsStableAfterDirectoryMove(t *testing.T) {
	root := t.TempDir()
	dir, moved := filepath.Join(root, "app"), filepath.Join(root, "moved")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	directory, current, name, err := store.OutputIdentity()
	if err != nil || directory == nil || current == nil || name != "config.toml" {
		t.Fatalf("original identity: dir=%v file=%v name=%q err=%v", directory, current, name, err)
	}
	if err := os.Rename(dir, moved); err != nil {
		t.Skipf("open directory cannot be renamed: %v", err)
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("decoy"), 0600); err != nil {
		t.Fatal(err)
	}
	pinnedDir, err := store.root.Open(".")
	if err != nil {
		t.Fatal(err)
	}
	defer pinnedDir.Close()
	wantDir, err := pinnedDir.Stat()
	if err != nil {
		t.Fatal(err)
	}
	pinnedFile, err := store.openTarget("config.toml")
	if err != nil {
		t.Fatal(err)
	}
	defer pinnedFile.Close()
	wantFile, err := pinnedFile.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(directory, wantDir) || !os.SameFile(current, wantFile) {
		t.Fatal("identities obtained before the move no longer describe the pinned directory and file")
	}
	newDir, newFile, _, err := store.OutputIdentity()
	if err != nil || newDir == nil || newFile == nil || !os.SameFile(newDir, wantDir) || !os.SameFile(newFile, wantFile) {
		t.Fatalf("identity after move: dir=%v file=%v err=%v", newDir, newFile, err)
	}
}

func TestAbsoluteSymlinkDirectoryCase(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "actual.toml")
	if err := os.WriteFile(target, []byte("SendText = \"inside\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.toml")
	makeConfigSymlink(t, filepath.Join(strings.ToUpper(dir), "actual.toml"), path)
	settings, _, err := load(path)
	if err != nil || settings.SendText != "inside" {
		t.Fatalf("load: %q err=%v", settings.SendText, err)
	}
	settings.SendText = "updated"
	if err := Save(path, settings); err != nil {
		t.Fatal(err)
	}
	got := defaults()
	if err := decode(target, &got); err != nil || got.SendText != "updated" {
		t.Fatalf("save: %q err=%v", got.SendText, err)
	}
	store, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	settings, storedPath, file, err := store.LoadWithIdentity()
	if file != nil {
		file.Close()
	}
	if err != nil || settings.SendText != "updated" {
		t.Fatalf("store load: %q err=%v", settings.SendText, err)
	}
	settings.SendText = "store updated"
	file, err = store.SaveWithIdentity(storedPath, settings)
	if file != nil {
		file.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := decode(target, &got); err != nil || got.SendText != "store updated" {
		t.Fatalf("store save: %q err=%v", got.SendText, err)
	}
}

func TestAbsoluteSymlinkRejectsCaseDistinctDirectory(t *testing.T) {
	parent := t.TempDir()
	name, err := windows.UTF16PtrFromString(parent)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(name, windows.FILE_WRITE_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		t.Skipf("case-sensitive directories unavailable: %v", err)
	}
	flags := uint32(windows.FILE_CS_FLAG_CASE_SENSITIVE_DIR)
	err = windows.SetFileInformationByHandle(h, windows.FileCaseSensitiveInfo, (*byte)(unsafe.Pointer(&flags)), uint32(unsafe.Sizeof(flags)))
	windows.CloseHandle(h)
	if err != nil {
		t.Skipf("case-sensitive directories unavailable: %v", err)
	}
	dir, outside := filepath.Join(parent, "app"), filepath.Join(parent, "APP")
	for _, path := range []string{dir, outside} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	other := filepath.Join(outside, "actual.toml")
	original := []byte("SendText = \"outside\"\n")
	if err := os.WriteFile(other, original, 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.toml")
	makeConfigSymlink(t, other, path)
	if _, _, err := load(path); err == nil {
		t.Fatal("load accepted a case-distinct outside directory")
	}
	if err := Save(path, defaults()); err == nil {
		t.Fatal("save accepted a case-distinct outside directory")
	}
	store, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_, _, file, err := store.LoadWithIdentity()
	if file != nil {
		file.Close()
	}
	if err == nil {
		t.Fatal("store load accepted a case-distinct outside directory")
	}
	file, err = store.SaveWithIdentity(path, defaults())
	if file != nil {
		file.Close()
	}
	if err == nil {
		t.Fatal("store save accepted a case-distinct outside directory")
	}
	if got, err := os.ReadFile(other); err != nil || string(got) != string(original) {
		t.Fatalf("outside configuration changed: %q err=%v", got, err)
	}
}

func TestStoreRejectsConfigurationLockAlias(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			lock := filepath.Join(dir, ".config.toml.lock")
			path := filepath.Join(dir, "config.toml")
			original := []byte("SendText = \"original\"\n")
			if err := os.WriteFile(lock, original, 0600); err != nil {
				t.Fatal(err)
			}
			if kind == "symlink" {
				makeConfigSymlink(t, ".config.toml.lock", path)
			} else if err := os.Link(lock, path); err != nil {
				t.Skipf("hard links unavailable: %v", err)
			}
			store, err := openStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			settings, loadedPath, file, err := store.LoadWithIdentity()
			if file != nil {
				file.Close()
			}
			if err != nil || settings.SendText != "original" {
				t.Fatalf("load: text=%q err=%v", settings.SendText, err)
			}
			settings.SendText = "changed"
			file, err = store.SaveWithIdentity(loadedPath, settings)
			if file != nil {
				file.Close()
			}
			if err == nil || !strings.Contains(i18n.ErrorText(err, false), "配置锁文件") || !strings.Contains(i18n.ErrorText(err, true), "lock file") {
				t.Fatalf("expected localized lock conflict, got %v", err)
			}
			got, err := os.ReadFile(lock)
			if err != nil || string(got) != string(original) {
				t.Fatalf("configuration changed: %q err=%v", got, err)
			}
		})
	}
}
