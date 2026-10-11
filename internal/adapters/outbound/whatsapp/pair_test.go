package whatsapp

import (
	"context"
	"io"
	"path/filepath"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waAdv"
	"go.mau.fi/whatsmeow/types"
)

type fakePairClient struct {
	paired, connected     bool
	qrCalls, connectCalls int
	qr                    chan PairItem
	events                chan ConnectionEvent
	session               *SessionStore
}

func (f *fakePairClient) Paired() bool { return f.paired }
func (f *fakePairClient) GetQRChannel(context.Context) (<-chan PairItem, error) {
	f.qrCalls++
	return f.qr, nil
}
func (f *fakePairClient) Connect(context.Context) error  { f.connectCalls++; return nil }
func (f *fakePairClient) Events() <-chan ConnectionEvent { return f.events }
func (f *fakePairClient) IsLoggedIn() bool               { return f.connected }
func (f *fakePairClient) complete() {
	f.connected = true
	f.paired = true
	jid := types.NewJID("15551234567", "s.whatsapp.net")
	f.session.Device().ID = &jid
	f.session.Device().Account = &waAdv.ADVSignedDeviceIdentity{Details: []byte{1}, AccountSignature: make([]byte, 64), AccountSignatureKey: make([]byte, 32), DeviceSignature: make([]byte, 64)}
}
func openPairSession(t *testing.T) *SessionStore {
	t.Helper()
	s, e := OpenSession(context.Background(), filepath.Join(t.TempDir(), "pair.db"))
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func TestPairExistingSession(t *testing.T) {
	s := openPairSession(t)
	defer s.Close()
	jid := types.NewJID("15551234567", "s.whatsapp.net")
	s.Device().ID = &jid
	s.Device().Account = &waAdv.ADVSignedDeviceIdentity{Details: []byte{1}, AccountSignature: make([]byte, 64), AccountSignatureKey: make([]byte, 32), DeviceSignature: make([]byte, 64)}
	fake := &fakePairClient{paired: true, connected: true, events: make(chan ConnectionEvent, 1), session: s}
	fake.events <- ConnectionEvent{Kind: ConnectionConnected}
	rendered := false
	if err := Pair(context.Background(), fake, s, io.Discard, true, func(io.Writer, string) error { rendered = true; return nil }); err != nil {
		t.Fatal(err)
	}
	if fake.qrCalls != 0 || fake.connectCalls != 1 || rendered {
		t.Fatalf("QR calls=%d connects=%d rendered=%t", fake.qrCalls, fake.connectCalls, rendered)
	}
}
func TestPairQRIsTerminalOnly(t *testing.T) {
	s := openPairSession(t)
	defer s.Close()
	fake := &fakePairClient{events: make(chan ConnectionEvent)}
	if err := Pair(context.Background(), fake, s, io.Discard, false, func(io.Writer, string) error { return nil }); err == nil {
		t.Fatal("non-terminal pairing accepted")
	}
	if fake.qrCalls != 0 || fake.connectCalls != 0 {
		t.Fatal("QR or connection was exposed before terminal validation")
	}
}
func TestPairPersistenceAndCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pair.db")
	s, err := OpenSession(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakePairClient{events: make(chan ConnectionEvent, 2), qr: make(chan PairItem)}
	fake.session = s
	rendered := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- Pair(context.Background(), fake, s, &captureWriter{write: func(b []byte) (int, error) { rendered <- string(b); return len(b), nil }}, true, func(w io.Writer, code string) error { _, e := io.WriteString(w, code); return e })
	}()
	fake.qr <- PairItem{Event: "code", Code: "secret-pairing-payload"}
	select {
	case code := <-rendered:
		if code != "secret-pairing-payload" {
			t.Fatal("unexpected QR output")
		}
	case <-time.After(time.Second):
		t.Fatal("pairing code not rendered")
	}
	fake.complete()
	fake.qr <- PairItem{Event: "success"}
	fake.events <- ConnectionEvent{Kind: ConnectionConnected}
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("pairing did not finish")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenSession(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Device().GetJID().IsEmpty() {
		t.Fatal("paired device not restored")
	}
	s.Close()
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()
	s2 := openPairSession(t)
	defer s2.Close()
	fake2 := &fakePairClient{events: make(chan ConnectionEvent), qr: make(chan PairItem)}
	if err = Pair(cancelCtx, fake2, s2, io.Discard, true, func(io.Writer, string) error { return nil }); err == nil {
		t.Fatal("cancelled pairing returned success")
	}
}

type captureWriter struct{ write func([]byte) (int, error) }

func (w *captureWriter) Write(p []byte) (int, error) { return w.write(p) }
func TestLogoutNeedsExclusiveSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.db")
	first, err := OpenSession(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = OpenSession(context.Background(), path); err == nil {
		t.Fatal("second session access was not rejected")
	}
	if err = first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := OpenSession(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	second.Close()
}
