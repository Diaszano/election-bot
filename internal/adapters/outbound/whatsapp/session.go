package whatsapp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	waStore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	_ "modernc.org/sqlite"
)

// SessionStore owns the protected whatsmeow database and its cross-process lock.
type SessionStore struct {
	path      string
	lock      *SessionLock
	db        *sql.DB
	container *sqlstore.Container
	device    *waStore.Device
}

func OpenSession(ctx context.Context, path string) (*SessionStore, error) {
	lock, err := AcquireSessionLock(path)
	if err != nil {
		return nil, err
	}
	cleanup := func(e error) (*SessionStore, error) { _ = lock.Close(); return nil, e }
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return cleanup(fmt.Errorf("create WhatsApp session directory: %w", err))
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return cleanup(fmt.Errorf("open WhatsApp session database: %w", err))
	}
	if err = file.Chmod(0600); err != nil {
		file.Close()
		return cleanup(fmt.Errorf("protect WhatsApp session database: %w", err))
	}
	if err = file.Close(); err != nil {
		return cleanup(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return cleanup(fmt.Errorf("open WhatsApp session database: %w", err))
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	for _, pragma := range []string{"PRAGMA foreign_keys=ON", "PRAGMA journal_mode=WAL", "PRAGMA busy_timeout=5000", "PRAGMA synchronous=FULL"} {
		if _, err = db.ExecContext(ctx, pragma); err != nil {
			db.Close()
			return cleanup(fmt.Errorf("configure WhatsApp session database: %w", err))
		}
	}
	container := sqlstore.NewWithDB(db, "sqlite", nil)
	if err = container.Upgrade(ctx); err != nil {
		container.Close()
		return cleanup(fmt.Errorf("upgrade WhatsApp session database: %w", err))
	}
	devices, err := container.GetAllDevices(ctx)
	if err != nil {
		container.Close()
		return cleanup(errors.New("enumerate WhatsApp devices"))
	}
	device, pair, err := selectDevice(devices)
	if err != nil {
		container.Close()
		return cleanup(err)
	}
	if pair {
		device = container.NewDevice()
	}
	store := &SessionStore{path: path, lock: lock, db: db, container: container, device: device}
	if err = store.protectFiles(); err != nil {
		container.Close()
		lock.Close()
		return nil, err
	}
	return store, nil
}

func selectDevice(devices []*waStore.Device) (*waStore.Device, bool, error) {
	switch len(devices) {
	case 0:
		return nil, true, nil
	case 1:
		if devices[0] == nil {
			return nil, false, errors.New("WhatsApp device store contains an invalid entry")
		}
		return devices[0], false, nil
	default:
		return nil, false, errors.New("WhatsApp session database contains multiple devices; select and remove devices explicitly")
	}
}
func (s *SessionStore) Device() *waStore.Device        { return s.device }
func (s *SessionStore) Container() *sqlstore.Container { return s.container }
func (s *SessionStore) protectFiles() error {
	for _, path := range []string{s.path, s.path + "-wal", s.path + "-shm"} {
		if _, err := os.Stat(path); err == nil {
			if err = os.Chmod(path, 0600); err != nil {
				return fmt.Errorf("protect WhatsApp session file: %w", err)
			}
		}
	}
	return nil
}
func (s *SessionStore) Close() error {
	if s == nil {
		return nil
	}
	var err error
	if s.container != nil {
		err = s.container.Close()
		s.container = nil
		s.db = nil
	}
	if protectErr := s.protectFiles(); err == nil {
		err = protectErr
	}
	if s.lock != nil {
		if lockErr := s.lock.Close(); err == nil {
			err = lockErr
		}
		s.lock = nil
	}
	return err
}
