package whatsapp

import (
	"context"
	"testing"

	"github.com/Diaszano/election-bot/internal/domain/election"
	"go.mau.fi/whatsmeow/types"
)

type fakeJIDMapper map[string]types.JID

func (m fakeJIDMapper) GetAltJID(_ context.Context, jid types.JID) (types.JID, error) {
	return m[jid.String()], nil
}

func TestPNLIDCanonicalDedup(t *testing.T) {
	pn := types.NewJID("5511999999999", types.DefaultUserServer)
	lid := types.NewJID("987654321", types.HiddenUserServer)
	r := NewIdentityResolver(fakeJIDMapper{pn.String(): lid, lid.String(): pn}, election.Identity{Year: 2026, Round: 2, Code: "6258", Scope: "br", Office: 1})
	fromPN, ok, err := r.Resolve(context.Background(), pn.String(), pn.String(), lid.String())
	if err != nil || !ok {
		t.Fatalf("PN resolve: %v %t %v", fromPN, ok, err)
	}
	fromLID, ok, err := r.Resolve(context.Background(), lid.String(), lid.String(), pn.String())
	if err != nil || !ok {
		t.Fatalf("LID resolve: %v %t %v", fromLID, ok, err)
	}
	if fromPN.Key() != fromLID.Key() || fromPN.Recipient != "5511999999999@s.whatsapp.net" {
		t.Fatalf("PN/LID did not canonicalize: %+v %+v", fromPN, fromLID)
	}
}
func TestUnresolvedIdentityFailsClosed(t *testing.T) {
	lid := types.NewJID("987654321", types.HiddenUserServer)
	r := NewIdentityResolver(fakeJIDMapper{}, election.Identity{Year: 2026, Round: 2, Code: "6258", Scope: "br", Office: 1})
	if _, ok, err := r.Resolve(context.Background(), lid.String(), lid.String(), ""); err == nil || ok {
		t.Fatal("unresolved LID was authorized")
	}
}
func TestConflictingAlternateIdentityFailsClosed(t *testing.T) {
	pn := types.NewJID("5511999999999", types.DefaultUserServer)
	lid := types.NewJID("987654321", types.HiddenUserServer)
	other := types.NewJID("987654322", types.HiddenUserServer)
	r := NewIdentityResolver(fakeJIDMapper{pn.String(): lid}, election.Identity{Year: 2026, Round: 2, Code: "6258", Scope: "br", Office: 1})
	if _, ok, err := r.Resolve(context.Background(), pn.String(), pn.String(), other.String()); err == nil || ok {
		t.Fatal("conflicting alternate identity was authorized")
	}
}

func TestChatSenderMismatchFailsClosed(t *testing.T) {
	pn := types.NewJID("5511999999999", types.DefaultUserServer)
	other := types.NewJID("5511888888888", types.DefaultUserServer)
	r := NewIdentityResolver(fakeJIDMapper{}, election.Identity{Year: 2026, Round: 2, Code: "6258", Scope: "br", Office: 1})
	if _, ok, err := r.Resolve(context.Background(), other.String(), pn.String(), ""); err == nil || ok {
		t.Fatal("mismatched private chat was authorized")
	}
}
