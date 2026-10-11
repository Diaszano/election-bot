package whatsapp

import (
	"context"
	"errors"

	"github.com/Diaszano/election-bot/internal/domain/election"
	"github.com/Diaszano/election-bot/internal/domain/subscription"
	"go.mau.fi/whatsmeow/types"
)

type JIDMapper interface {
	GetAltJID(context.Context, types.JID) (types.JID, error)
}
type IdentityResolver struct {
	mapper   JIDMapper
	election election.Identity
}

func NewIdentityResolver(mapper JIDMapper, electionID election.Identity) *IdentityResolver {
	return &IdentityResolver{mapper: mapper, election: electionID}
}

// Resolve accepts only a private chat whose PN/LID relationship is verified by the device store.
func (r *IdentityResolver) Resolve(ctx context.Context, chatValue, senderValue, altValue string) (subscription.Identity, bool, error) {
	if r == nil || r.mapper == nil {
		return subscription.Identity{}, false, errors.New("WhatsApp identity mapping unavailable")
	}
	chat, err := types.ParseJID(chatValue)
	if err != nil {
		return subscription.Identity{}, false, errors.New("invalid WhatsApp chat identity")
	}
	sender, err := types.ParseJID(senderValue)
	if err != nil {
		return subscription.Identity{}, false, errors.New("invalid WhatsApp sender identity")
	}
	var alt types.JID
	if altValue != "" {
		alt, err = types.ParseJID(altValue)
		if err != nil {
			return subscription.Identity{}, false, errors.New("invalid WhatsApp alternate identity")
		}
	}
	chat, sender, alt = chat.ToNonAD(), sender.ToNonAD(), alt.ToNonAD()
	canonical, err := r.canonicalPN(ctx, sender, alt)
	if err != nil {
		return subscription.Identity{}, false, err
	}
	if !r.samePrivateIdentity(ctx, chat, sender, alt, canonical) {
		return subscription.Identity{}, false, errors.New("WhatsApp chat and sender identity mismatch")
	}
	identity, err := subscription.NewIdentity(r.election, subscription.WhatsApp, canonical.String())
	if err != nil {
		return subscription.Identity{}, false, errors.New("invalid canonical WhatsApp identity")
	}
	return identity, true, nil
}
func (r *IdentityResolver) canonicalPN(ctx context.Context, jid, alt types.JID) (types.JID, error) {
	switch jid.Server {
	case types.DefaultUserServer:
		if !validPN(jid) {
			return types.EmptyJID, errors.New("invalid WhatsApp PN identity")
		}
		if !alt.IsEmpty() {
			mapped, err := r.mapper.GetAltJID(ctx, jid)
			if err != nil || mapped.ToNonAD() != alt {
				return types.EmptyJID, errors.New("unverified WhatsApp alternate identity")
			}
		}
		return jid, nil
	case types.HiddenUserServer:
		mapped, err := r.mapper.GetAltJID(ctx, jid)
		if err != nil || mapped.Server != types.DefaultUserServer || !validPN(mapped) {
			return types.EmptyJID, errors.New("unresolved WhatsApp LID identity")
		}
		if !alt.IsEmpty() && alt != mapped.ToNonAD() {
			return types.EmptyJID, errors.New("conflicting WhatsApp alternate identity")
		}
		return mapped.ToNonAD(), nil
	default:
		return types.EmptyJID, errors.New("unsupported WhatsApp identity type")
	}
}
func (r *IdentityResolver) samePrivateIdentity(ctx context.Context, chat, sender, alt, canonical types.JID) bool {
	if chat == sender || (!alt.IsEmpty() && chat == alt) || chat == canonical {
		return true
	}
	mapped, err := r.mapper.GetAltJID(ctx, chat)
	if err != nil || mapped.IsEmpty() {
		return false
	}
	mapped = mapped.ToNonAD()
	return mapped == sender || mapped == canonical || (!alt.IsEmpty() && mapped == alt)
}
func validPN(jid types.JID) bool {
	if jid.Server != types.DefaultUserServer || len(jid.User) < 8 || len(jid.User) > 15 {
		return false
	}
	for _, r := range jid.User {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
