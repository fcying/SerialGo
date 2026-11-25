package config

import (
	"crypto/sha256"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"sync"

	"github.com/fcying/SerialGo/internal/i18n"
)

// errConfigurationChanged marks a target replaced while a snapshot is read.
var errConfigurationChanged = i18n.NewError("读取配置时文件被替换", "Configuration changed during access")

// Store binds settings I/O to the executable directory for its lifetime.
// Close it only after all settings operations have completed.
type Store struct {
	path     string
	dir      string
	root     *os.Root
	close    func() error
	mu       sync.Mutex
	baseline *storeSnapshot
}

// DefaultSettings returns independent defaults without accessing the filesystem.
func DefaultSettings() Settings {
	return defaults()
}

// OpenExecutableStore pins the resolved executable directory before loading settings.
func OpenExecutableStore() (*Store, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return nil, err
	}
	store, err := openStore(filepath.Dir(exe))
	if err != nil {
		return nil, err
	}
	if err := verifyExecutableDirectory(store, exe); err != nil {
		store.Close()
		return nil, err
	}
	return store, nil
}

func openStore(dir string) (*Store, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	pinned, root, closeDir, err := pinSettingsDirectory(dir)
	if err != nil {
		return nil, err
	}
	return &Store{path: filepath.Join(dir, "config.toml"), dir: pinned, root: root, close: closeDir}, nil
}

func (s *Store) LoadWithIdentity() (Settings, string, *os.File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.baseline != nil {
		s.baseline.file.Close()
		s.baseline = nil
	}
	for attempt := 0; attempt < 8; attempt++ {
		var file *os.File
		settings, path, err := loadStoreFile(s, &file)
		if err != nil {
			if file == nil && errors.Is(err, os.ErrExist) {
				continue
			}
			return settings, path, file, err
		}
		h := sha256.New()
		settings = defaults()
		reader := io.NewSectionReader(file, 0, math.MaxInt64)
		if err = decodeSettings(io.TeeReader(reader, h), &settings); err != nil {
			return defaults(), "", file, err
		}
		var digest [sha256.Size]byte
		copy(digest[:], h.Sum(nil))
		snapshot, err := s.snapshot(file, digest)
		if err != nil {
			if errors.Is(err, errConfigurationChanged) || errors.Is(err, os.ErrNotExist) {
				file.Close()
				continue
			}
			return defaults(), "", file, err
		}
		s.baseline = snapshot
		return settings, s.path, file, nil
	}
	return defaults(), "", nil, configurationConflict(errConfigurationChanged)
}

func (s *Store) SaveWithIdentity(path string, settings Settings) (*os.File, error) {
	if path == "" {
		return nil, nil
	}
	if path != s.path {
		return nil, errors.New("configuration path does not belong to this store")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := lockStore(s)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if s.baseline == nil {
		return nil, errors.New("configuration was not loaded")
	}
	current, err := openStoreTarget(s)
	if err != nil {
		return nil, configurationConflict(err)
	}
	defer current.Close()
	digest, err := hashStoreFile(current)
	if err != nil {
		return nil, err
	}
	snapshot, err := s.snapshot(current, digest)
	if err != nil {
		if errors.Is(err, errConfigurationChanged) {
			return nil, configurationConflict(err)
		}
		return nil, err
	}
	defer snapshot.file.Close()
	if snapshot.name != s.baseline.name || !os.SameFile(snapshot.info, s.baseline.info) || snapshot.hash != s.baseline.hash {
		return nil, configurationConflict(nil)
	}
	var file *os.File
	if err := saveStoreFile(s, settings, &file); err != nil {
		return file, err
	}
	digest, err = hashStoreFile(file)
	if err == nil {
		var next *storeSnapshot
		next, err = s.snapshot(file, digest)
		if err == nil {
			s.baseline.file.Close()
			s.baseline = next
		}
	}
	if err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}

type storeSnapshot struct {
	name string
	file *os.File
	info os.FileInfo
	hash [sha256.Size]byte
}

func hashStoreFile(file *os.File) ([sha256.Size]byte, error) {
	h := sha256.New()
	_, err := io.Copy(h, io.NewSectionReader(file, 0, math.MaxInt64))
	var digest [sha256.Size]byte
	copy(digest[:], h.Sum(nil))
	return digest, err
}

func (s *Store) snapshot(file *os.File, digest [sha256.Size]byte) (*storeSnapshot, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	_, current, name, err := storeOutputIdentity(s)
	if err != nil {
		return nil, err
	}
	if current == nil || !os.SameFile(info, current) {
		return nil, errConfigurationChanged
	}
	pinned, err := openStoreTarget(s)
	if err != nil {
		return nil, err
	}
	pinnedInfo, err := pinned.Stat()
	if err != nil || !os.SameFile(info, pinnedInfo) {
		pinned.Close()
		if err != nil {
			return nil, err
		}
		return nil, errConfigurationChanged
	}
	return &storeSnapshot{name: name, info: info, file: pinned, hash: digest}, nil
}

func configurationConflict(cause error) error {
	const zh = "配置文件已更改，参数未保存。请先复制未保存的修改，重启以重新加载配置，再重新应用修改"
	const en = "Configuration changed; settings were not saved. Copy unsaved changes, restart to reload the configuration, then reapply them"
	if cause != nil {
		return i18n.WrapError(zh, en, cause)
	}
	return i18n.NewError(zh, en)
}

func configurationLockTimeout(cause error) error {
	return i18n.WrapError("等待配置文件锁超时，参数未保存，请稍后重试", "Timed out waiting for the configuration lock; settings were not saved, please retry", cause)
}

// OutputIdentity describes the current configuration in the pinned directory.
// A missing configuration has a nil file identity but still reserves its name.
func (s *Store) OutputIdentity() (os.FileInfo, os.FileInfo, string, error) {
	return storeOutputIdentity(s)
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var err error
	if s.baseline != nil {
		err = s.baseline.file.Close()
		s.baseline = nil
	}
	return errors.Join(err, s.close())
}
