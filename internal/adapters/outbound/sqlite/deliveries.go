package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/Diaszano/election-bot/internal/application/ports"
	"github.com/Diaszano/election-bot/internal/domain/election"
	"github.com/Diaszano/election-bot/internal/domain/notification"
	"github.com/Diaszano/election-bot/internal/domain/subscription"
)

var _ ports.ChannelDeliveries = (*Store)(nil)
var _ ports.ChannelStateStore = (*Store)(nil)
var _ ports.CommandStore = (*Store)(nil)

func electionKey(e election.Identity) string { return electionIdentityKey(e) }

func (t *transaction) deliveryIdentity(d notification.Delivery) (subscription.Identity, error) {
	if d.Identity.Channel != "" {
		if d.Identity.Election != t.election {
			return subscription.Identity{}, errors.New("delivery election does not match store")
		}
		return d.Identity, nil
	}
	return subscription.NewIdentity(t.election, subscription.Telegram, strconv.FormatInt(d.ChatID, 10))
}

func unixTime(n int64) time.Time {
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n).UTC()
}

func timestamp(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixNano()
}

func nullableTime(n sql.NullInt64) time.Time {
	if !n.Valid {
		return time.Time{}
	}
	return unixTime(n.Int64)
}

func (s *Store) PendingChannel(ctx context.Context, channel subscription.Channel, now time.Time, limit int) ([]notification.Delivery, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT o.id,o.subscription_id,s.channel,s.recipient,o.generation,o.kind,o.event_key,o.milestone,o.revision,o.semantic_revision,o.text,o.due,o.attempts,s.message_id,s.original_sent_at,s.last_edit_at,s.last_summary_at,s.last_activity_at,s.accepted_semantic_revision,s.accepted_presentation_revision,s.edit_unavailable,s.next_outbound_at
	FROM outbox o JOIN subscriptions s ON s.id=o.subscription_id WHERE s.channel=? AND s.election_key=? AND o.state='pending' AND (s.active=1 OR o.kind='reply') AND s.generation=o.generation AND max(o.due,s.next_outbound_at)<=? ORDER BY max(o.due,s.next_outbound_at),o.id LIMIT ?`, channel, electionIdentityKey(s.election), now.UnixNano(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []notification.Delivery
	for rows.Next() {
		var d notification.Delivery
		var kind, channelName, recipient, messageID string
		var due, next int64
		var original, lastEdit, lastSummary, lastActivity sql.NullInt64
		var editUnavailable bool
		if err = rows.Scan(&d.ID, &d.SubscriberID, &channelName, &recipient, &d.Generation, &kind, &d.EventKey, &d.Milestone, &d.Revision, &d.SemanticRevision, &d.Text, &due, &d.Attempts, &messageID, &original, &lastEdit, &lastSummary, &lastActivity, &d.MainState.AcceptedSemanticRevision, &d.MainState.AcceptedPresentationRevision, &editUnavailable, &next); err != nil {
			return nil, err
		}
		d.Kind = kind
		d.Due = unixTime(due)
		d.Identity = subscription.Identity{Election: s.election, Channel: subscription.Channel(channelName), Recipient: recipient}
		if channelName == string(subscription.Telegram) {
			d.ChatID, _ = strconv.ParseInt(recipient, 10, 64)
			d.MessageID, _ = strconv.ParseInt(messageID, 10, 64)
		}
		d.MainState.Ref = notification.MessageRef{ID: messageID, OriginalSentAt: nullableTime(original)}
		d.MainState.LastEditAt = nullableTime(lastEdit)
		d.MainState.LastSummaryAt = nullableTime(lastSummary)
		d.MainState.LastActivityAt = nullableTime(lastActivity)
		d.MainState.EditUnavailable = editUnavailable
		if due < next {
			d.Due = unixTime(next)
		}
		result = append(result, d)
	}
	return result, rows.Err()
}

func (s *Store) PrepareAttempt(ctx context.Context, d notification.Delivery, operation notification.Operation, proposedID string) (notification.Attempt, error) {
	if proposedID == "" || (operation != notification.Send && operation != notification.Edit) {
		return notification.Attempt{}, errors.New("invalid delivery attempt")
	}
	var result notification.Attempt
	err := s.Transaction(ctx, func(p ports.Transaction) error {
		t := p.(*transaction)
		identity, err := t.deliveryIdentity(d)
		if err != nil {
			return err
		}
		var active bool
		var generation int64
		var messageID string
		if err = t.tx.QueryRowContext(ctx, `SELECT s.id,s.active,s.generation,s.message_id FROM subscriptions s WHERE s.election_key=? AND s.channel=? AND s.recipient=?`, electionKey(identity.Election), identity.Channel, identity.Recipient).Scan(&result.ID, &active, &generation, &messageID); err != nil {
			return err
		}
		var revision, text, state, kind string
		var outGeneration int64
		if err = t.tx.QueryRowContext(ctx, `SELECT revision,text,state,generation,kind FROM outbox WHERE id=? AND subscription_id=?`, d.ID, result.ID).Scan(&revision, &text, &state, &outGeneration, &kind); err != nil {
			return err
		}
		if generation != d.Generation || outGeneration != d.Generation || state != "pending" || (!active && kind != "reply") {
			return ports.ErrNotFound
		}
		if d.Revision != "" && d.Revision != revision {
			return ports.ErrNotFound
		}
		target := ""
		if operation == notification.Edit {
			target = d.MainState.Ref.ID
			if target == "" {
				target = messageID
			}
			if target == "" {
				return errors.New("missing edit target")
			}
		}
		var prior notification.Attempt
		err = t.tx.QueryRowContext(ctx, `SELECT id,outbox_id,generation,operation,request_id,revision,text,target_message_id FROM delivery_attempts WHERE outbox_id=? AND generation=? AND operation=? AND revision=? AND text=? AND target_message_id=? ORDER BY id DESC LIMIT 1`, d.ID, d.Generation, operation, revision, text, target).Scan(&prior.ID, &prior.DeliveryID, &prior.Generation, &prior.Operation, &prior.RequestID, &prior.Revision, &prior.Text, &prior.TargetMessageID)
		if err == nil {
			result = prior
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		prepared := s.clockNow()
		res, err := t.tx.ExecContext(ctx, `INSERT INTO delivery_attempts(outbox_id,generation,operation,request_id,revision,text,target_message_id,prepared_at) VALUES(?,?,?,?,?,?,?,?)`, d.ID, d.Generation, operation, proposedID, revision, text, target, prepared.UnixNano())
		if err != nil {
			return err
		}
		attemptID, _ := res.LastInsertId()
		result = notification.Attempt{ID: attemptID, DeliveryID: d.ID, Generation: d.Generation, Operation: operation, RequestID: proposedID, Revision: revision, Text: text, TargetMessageID: target}
		return nil
	})
	return result, err
}

func (s *Store) clockNow() time.Time { return time.Now().UTC() }

func (s *Store) CompleteAttempt(ctx context.Context, d notification.Delivery, a notification.Attempt, accepted notification.Acceptance) error {
	return s.Transaction(ctx, func(p ports.Transaction) error {
		t := p.(*transaction)
		identity, err := t.deliveryIdentity(d)
		if err != nil {
			return err
		}
		var sid int64
		var active bool
		var generation int64
		if err = t.tx.QueryRowContext(ctx, `SELECT id,active,generation FROM subscriptions WHERE election_key=? AND channel=? AND recipient=?`, electionKey(identity.Election), identity.Channel, identity.Recipient).Scan(&sid, &active, &generation); err != nil {
			return err
		}
		if generation != a.Generation || (!active && d.Kind != "reply") {
			return nil
		}
		acceptedAt := accepted.AcceptedAt
		if acceptedAt.IsZero() {
			acceptedAt = time.Now().UTC()
		}
		if !accepted.NextOutboundAt.IsZero() {
			if _, err = t.tx.ExecContext(ctx, `UPDATE subscriptions SET next_outbound_at=? WHERE id=? AND generation=? AND active=1`, accepted.NextOutboundAt.UnixNano(), sid, a.Generation); err != nil {
				return err
			}
		}
		res, err := t.tx.ExecContext(ctx, `UPDATE delivery_attempts SET accepted_at=?,outcome='accepted' WHERE id=? AND outbox_id=? AND generation=? AND operation=? AND request_id=? AND revision=? AND text=?`, acceptedAt.UnixNano(), a.ID, d.ID, a.Generation, a.Operation, a.RequestID, a.Revision, a.Text)
		if err != nil {
			return err
		}
		changed, _ := res.RowsAffected()
		if changed == 0 {
			return ports.ErrNotFound
		}
		if d.Kind == "main" {
			messageID := d.MainState.Ref.ID
			original := d.MainState.Ref.OriginalSentAt
			if a.Operation == notification.Send {
				messageID = accepted.Ref.ID
				original = accepted.Ref.OriginalSentAt
				if original.IsZero() || original.After(acceptedAt) {
					original = acceptedAt
				}
			}
			if messageID == "" {
				return errors.New("accepted main result has no message reference")
			}
			_, err = t.tx.ExecContext(ctx, `UPDATE subscriptions SET message_id=?,original_sent_at=?,last_edit_at=CASE WHEN ?='edit' THEN ? ELSE last_edit_at END,last_summary_at=CASE WHEN ?='send' THEN ? ELSE last_summary_at END,last_activity_at=?,accepted_semantic_revision=?,accepted_presentation_revision=?,edit_unavailable=0 WHERE id=? AND generation=? AND active=1`, messageID, original.UnixNano(), a.Operation, acceptedAt.UnixNano(), a.Operation, acceptedAt.UnixNano(), acceptedAt.UnixNano(), d.SemanticRevision, a.Revision, sid, a.Generation)
			if err != nil {
				return err
			}
		}
		_, err = t.tx.ExecContext(ctx, `UPDATE outbox SET state='delivered',last_error='' WHERE id=? AND generation=? AND revision=? AND state='pending'`, d.ID, a.Generation, a.Revision)
		return err
	})
}

func (s *Store) RescheduleChannel(ctx context.Context, d notification.Delivery, due time.Time, kind string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE outbox SET due=?,attempts=attempts+1,last_error=? WHERE id=? AND generation=? AND revision=? AND state='pending' AND EXISTS(SELECT 1 FROM subscriptions s WHERE s.id=outbox.subscription_id AND s.generation=outbox.generation AND (s.active=1 OR outbox.kind='reply'))`, due.UnixNano(), safeKind(kind), d.ID, d.Generation, d.Revision)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil
	}
	return nil
}

func (s *Store) DeferDelivery(ctx context.Context, d notification.Delivery, due time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE outbox SET due=? WHERE id=? AND generation=? AND revision=? AND state='pending' AND EXISTS(SELECT 1 FROM subscriptions s WHERE s.id=outbox.subscription_id AND s.generation=outbox.generation AND (s.active=1 OR outbox.kind='reply'))`, due.UnixNano(), d.ID, d.Generation, d.Revision)
	return err
}

func (s *Store) MarkDeliveryUndeliverable(ctx context.Context, d notification.Delivery, kind string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE outbox SET state='failed',last_error=? WHERE id=? AND generation=? AND revision=? AND state='pending' AND EXISTS(SELECT 1 FROM subscriptions s WHERE s.id=outbox.subscription_id AND s.generation=outbox.generation AND (s.active=1 OR outbox.kind='reply'))`, safeKind(kind), d.ID, d.Generation, d.Revision)
	return err
}

func (s *Store) DisableEditing(ctx context.Context, d notification.Delivery, due time.Time, kind string) error {
	return s.Transaction(ctx, func(p ports.Transaction) error {
		t := p.(*transaction)
		identity, err := t.deliveryIdentity(d)
		if err != nil {
			return err
		}
		res, err := t.tx.ExecContext(ctx, `UPDATE subscriptions SET edit_unavailable=1 WHERE election_key=? AND channel=? AND recipient=? AND generation=? AND active=1 AND message_id=?`, electionKey(identity.Election), identity.Channel, identity.Recipient, d.Generation, d.MainState.Ref.ID)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return nil
		}
		_, err = t.tx.ExecContext(ctx, `UPDATE outbox SET due=?,last_error=? WHERE id=? AND generation=? AND state='pending'`, due.UnixNano(), safeKind(kind), d.ID, d.Generation)
		return err
	})
}

func (s *Store) ResolveAlreadyAccepted(ctx context.Context, d notification.Delivery) error {
	return s.Transaction(ctx, func(p ports.Transaction) error {
		t := p.(*transaction)
		identity, err := t.deliveryIdentity(d)
		if err != nil {
			return err
		}
		var accepted string
		if err = t.tx.QueryRowContext(ctx, `SELECT accepted_presentation_revision FROM subscriptions WHERE election_key=? AND channel=? AND recipient=? AND generation=? AND active=1`, electionKey(identity.Election), identity.Channel, identity.Recipient, d.Generation).Scan(&accepted); err != nil {
			return err
		}
		if accepted != d.Revision {
			return nil
		}
		_, err = t.tx.ExecContext(ctx, `UPDATE outbox SET state='delivered' WHERE id=? AND generation=? AND revision=? AND state='pending'`, d.ID, d.Generation, d.Revision)
		return err
	})
}

func (s *Store) RequeueFailed(ctx context.Context, channel subscription.Channel) error {
	_, err := s.db.ExecContext(ctx, `UPDATE outbox SET state='pending',due=?,last_error='' WHERE state='failed' AND NOT(kind='main' AND event_key LIKE 'legacy:%') AND subscription_id IN (SELECT id FROM subscriptions WHERE channel=? AND generation=outbox.generation AND (active=1 OR outbox.kind='reply'))`, time.Now().UTC().UnixNano(), channel)
	return err
}

func (s *Store) ReadChannelState(ctx context.Context, channel subscription.Channel) (notification.ChannelState, error) {
	var state notification.ChannelState
	var global, paused int64
	err := s.db.QueryRowContext(ctx, `SELECT global_next_send_at,paused_until,authentication_required,account_binding,pause_reason FROM channel_state WHERE channel=?`, channel).Scan(&global, &paused, &state.AuthenticationRequired, &state.AccountBinding, &state.PauseReason)
	state.GlobalNextSendAt = unixTime(global)
	state.PausedUntil = unixTime(paused)
	if errors.Is(err, sql.ErrNoRows) {
		return state, nil
	}
	return state, err
}

func (s *Store) SaveChannelState(ctx context.Context, channel subscription.Channel, state notification.ChannelState) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO channel_state(channel,global_next_send_at,paused_until,authentication_required,account_binding,pause_reason) VALUES(?,?,?,?,?,?) ON CONFLICT(channel) DO UPDATE SET global_next_send_at=excluded.global_next_send_at,paused_until=excluded.paused_until,authentication_required=excluded.authentication_required,account_binding=excluded.account_binding,pause_reason=excluded.pause_reason`, channel, timestamp(state.GlobalNextSendAt), timestamp(state.PausedUntil), state.AuthenticationRequired, state.AccountBinding, safeKind(state.PauseReason))
	return err
}

func (s *Store) ProcessCommand(ctx context.Context, command notification.Command, apply func(ports.Transaction) error) (bool, error) {
	processed := false
	err := s.Transaction(ctx, func(tx ports.Transaction) error {
		if _, err := tx.SubscriptionByIdentity(command.Identity); errors.Is(err, ports.ErrNotFound) {
			seed := subscription.Subscription{Identity: command.Identity, Active: false, Generation: 0, ResultsEnabled: true, MilestonesEnabled: true}
			if e := tx.PutSubscription(seed); e != nil {
				return e
			}
		} else if err != nil {
			return err
		}
		ok, err := tx.RecordCommand(command)
		if err != nil || !ok {
			return err
		}
		processed = true
		if apply == nil {
			return nil
		}
		return apply(tx)
	})
	if err != nil {
		return false, err
	}
	return processed, nil
}

func (t *transaction) RecordCommand(command notification.Command) (bool, error) {
	if command.Identity.Channel != subscription.WhatsApp || command.IncomingID == "" || !oneOf(command.Verb, "start", "agora", "status", "parar", "help") {
		return false, errors.New("invalid command record")
	}
	sub, err := t.SubscriptionByIdentity(command.Identity)
	if err != nil {
		return false, err
	}
	res, err := t.tx.ExecContext(t.ctx, `INSERT OR IGNORE INTO inbound_commands(subscription_id,incoming_message_id,verb,received_at) VALUES(?,?,?,?)`, sub.ID, command.IncomingID, command.Verb, command.ReceivedAt.UnixNano())
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

func (t *transaction) AuditSubscription(sub subscription.Subscription, action, incomingID string, at time.Time) error {
	if action != "opt_in" && action != "opt_out" {
		return errors.New("invalid subscription audit action")
	}
	if sub.ID == 0 {
		return errors.New("subscription must be persisted before audit")
	}
	_, err := t.tx.ExecContext(t.ctx, `INSERT INTO subscription_audit(subscription_id,generation,action,incoming_message_id,occurred_at) VALUES(?,?,?,?,?)`, sub.ID, sub.Generation, action, incomingID, at.UnixNano())
	return err
}

func (s *Store) Statistics(ctx context.Context) (subscribers, pending int64, oldest time.Time, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM subscriptions WHERE active=1 AND election_key=?`, electionIdentityKey(s.election)).Scan(&subscribers)
	if err != nil {
		return
	}
	var due sql.NullInt64
	err = s.db.QueryRowContext(ctx, `SELECT count(*),min(due) FROM outbox WHERE state='pending'`).Scan(&pending, &due)
	if due.Valid {
		oldest = unixTime(due.Int64)
	}
	return
}

func safeKind(value string) string {
	value = strings.ToLower(value)
	if value == "" || len(value) > 64 {
		return "unknown"
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return "unknown"
		}
	}
	return value
}

func oneOf(value string, choices ...string) bool {
	for _, choice := range choices {
		if value == choice {
			return true
		}
	}
	return false
}
