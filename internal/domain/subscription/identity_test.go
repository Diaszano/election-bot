package subscription

import (
	"testing"

	"github.com/Diaszano/election-bot/internal/domain/election"
)

func TestChannelIdentityIsolation(t *testing.T) {
	electionID := election.Identity{Year: 2026, Round: 2, Code: "6258", Scope: "br", Office: 1}
	tg, err := NewIdentity(electionID, Telegram, "12345")
	if err != nil {
		t.Fatal(err)
	}
	wa, err := NewIdentity(electionID, WhatsApp, "5511999999999@s.whatsapp.net")
	if err != nil {
		t.Fatal(err)
	}
	if tg.Key() == wa.Key() {
		t.Fatal("different channel recipients share identity")
	}
	if tg.Key() != (Identity{Election: electionID, Channel: Telegram, Recipient: "12345"}).Key() {
		t.Fatal("identity key is not deterministic")
	}
	for _, tc := range []struct {
		channel   Channel
		recipient string
	}{{"matrix", "x"}, {WhatsApp, ""}} {
		if _, err := NewIdentity(electionID, tc.channel, tc.recipient); err == nil {
			t.Fatalf("accepted invalid identity: %+v", tc)
		}
	}
}
