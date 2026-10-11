package whatsapp

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// SessionLock is a process-wide advisory lock held for the full session-store lifetime.
type SessionLock struct{ file *os.File }

func AcquireSessionLock(databasePath string) (*SessionLock, error) {
	canonical, err := canonicalDatabasePath(databasePath)
	if err != nil {
		return nil, err
	}
	lockPath := canonical + ".lock"
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, fmt.Errorf("open WhatsApp session lock: %w", err)
	}
	if err = file.Chmod(0600); err != nil {
		file.Close()
		return nil, fmt.Errorf("protect WhatsApp session lock: %w", err)
	}
	if err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, errors.New("WhatsApp session is already in use")
		}
		return nil, fmt.Errorf("acquire WhatsApp session lock: %w", err)
	}
	return &SessionLock{file: file}, nil
}

func (l *SessionLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	closeErr := l.file.Close()
	l.file = nil
	if err != nil {
		return err
	}
	return closeErr
}

func canonicalDatabasePath(path string) (string, error) {
	if path == "" {
		return "", errors.New("WhatsApp session database path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(abs)
	if err = os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	resolvedDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	resolved := filepath.Join(resolvedDir, filepath.Base(abs))
	if _, err = os.Lstat(abs); err == nil {
		resolved, err = filepath.EvalSymlinks(abs)
		if err != nil {
			return "", err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return filepath.Clean(resolved), nil
}
