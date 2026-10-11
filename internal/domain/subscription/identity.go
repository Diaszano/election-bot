package subscription

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Diaszano/election-bot/internal/domain/election"
)

type Channel string

const (
	Telegram Channel = "telegram"
	WhatsApp Channel = "whatsapp"
)

// Identity identifies one election subscriber independently of transport IDs.
type Identity struct {
	Election  election.Identity
	Channel   Channel
	Recipient string
}

func NewIdentity(e election.Identity, channel Channel, recipient string) (Identity, error) {
	if e.Year <= 0 || e.Round <= 0 || e.Code == "" || e.Scope == "" || e.Office <= 0 || recipient == "" {
		return Identity{}, fmt.Errorf("invalid subscription identity")
	}
	switch channel {
	case Telegram:
		id, err := strconv.ParseInt(recipient, 10, 64)
		if err != nil || id <= 0 || strconv.FormatInt(id, 10) != recipient {
			return Identity{}, fmt.Errorf("invalid subscription identity")
		}
	case WhatsApp:
		const suffix = "@s.whatsapp.net"
		if !strings.HasSuffix(recipient, suffix) {
			return Identity{}, fmt.Errorf("invalid subscription identity")
		}
		pn := strings.TrimSuffix(recipient, suffix)
		if len(pn) < 8 || len(pn) > 15 {
			return Identity{}, fmt.Errorf("invalid subscription identity")
		}
		for _, r := range pn {
			if r < '0' || r > '9' {
				return Identity{}, fmt.Errorf("invalid subscription identity")
			}
		}
	default:
		return Identity{}, fmt.Errorf("invalid subscription identity")
	}
	return Identity{Election: e, Channel: channel, Recipient: recipient}, nil
}

// Key length-prefixes variable values so distinct identities cannot collide.
func (i Identity) Key() string {
	return fmt.Sprintf("%d/%d/%d/%q/%q/%s/%d:%s", i.Election.Year, i.Election.Round, i.Election.Office, i.Election.Code, i.Election.Scope, i.Channel, len(i.Recipient), i.Recipient)
}
