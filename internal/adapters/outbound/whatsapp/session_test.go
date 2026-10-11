package whatsapp

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"go.mau.fi/whatsmeow/proto/waAdv"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
)

func TestSelectAllStoredDevices(t *testing.T) {
	one := &store.Device{}
	got, pair, err := selectDevice(nil)
	if err != nil || got != nil || !pair {
		t.Fatalf("empty selection: device=%p pair=%t err=%v", got, pair, err)
	}
	got, pair, err = selectDevice([]*store.Device{one})
	if err != nil || got != one || pair {
		t.Fatalf("single selection: device=%p pair=%t err=%v", got, pair, err)
	}
	if _, _, err = selectDevice([]*store.Device{one, {}}); err == nil {
		t.Fatal("ambiguous multi-device store was accepted")
	}
}

func TestSessionSQLitePersistence(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "session.db")
	session, err := OpenSession(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if session.Device() == nil {
		t.Fatal("empty store did not create a device")
	}
	jid := types.NewJID("15551234567", "s.whatsapp.net")
	session.Device().ID = &jid
	session.Device().Account = &waAdv.ADVSignedDeviceIdentity{Details: []byte{1}, AccountSignature: make([]byte, 64), AccountSignatureKey: make([]byte, 32), DeviceSignature: make([]byte, 64)}
	if err = session.Device().Save(ctx); err != nil {
		t.Fatal(err)
	}
	if err = session.Close(); err != nil {
		t.Fatal(err)
	}
	session, err = OpenSession(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if session.Device() == nil {
		t.Fatal("device not restored")
	}
	devices, err := session.container.GetAllDevices(ctx)
	if err != nil || len(devices) != 1 {
		t.Fatalf("restored devices=%d err=%v", len(devices), err)
	}
	var foreignKeys int
	if err = session.db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil || foreignKeys != 1 {
		t.Fatalf("foreign_keys=%d err=%v", foreignKeys, err)
	}
}

func TestSessionPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "whatsapp.db")
	session, err := OpenSession(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0077 != 0 {
		t.Fatalf("session database permissions are too broad: %o", info.Mode().Perm())
	}
}
