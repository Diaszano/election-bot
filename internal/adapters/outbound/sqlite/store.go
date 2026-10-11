// Package sqlite implements transactional application state using SQLite.
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Diaszano/election-bot/internal/application/ports"
	"github.com/Diaszano/election-bot/internal/domain/election"
	"github.com/Diaszano/election-bot/internal/domain/notification"
	"github.com/Diaszano/election-bot/internal/domain/subscription"
	"github.com/Diaszano/election-bot/migrations"
	_ "modernc.org/sqlite"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Store struct {
	db       *sql.DB
	election election.Identity
}

func Open(ctx context.Context, path string) (*Store, error) {
	return OpenForElection(ctx, path, election.Identity{Year: 2026, Round: 2, Code: "6258", Scope: "br", Office: 1})
}

func OpenForElection(ctx context.Context, path string, identity election.Identity) (*Store, error) {
	if path != ":memory:" {
		if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
			return nil, e
		}
	}
	db, e := sql.Open("sqlite", path)
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, election: identity}
	for _, q := range []string{"PRAGMA journal_mode=WAL", "PRAGMA foreign_keys=ON", "PRAGMA busy_timeout=5000", "PRAGMA synchronous=FULL"} {
		if _, e = db.ExecContext(ctx, q); e != nil {
			db.Close()
			return nil, e
		}
	}
	if _, e = db.ExecContext(ctx, migrations.Initial); e != nil {
		db.Close()
		return nil, e
	}
	var version int
	if e = db.QueryRowContext(ctx, "SELECT max(version) FROM schema_version").Scan(&version); e != nil {
		db.Close()
		return nil, e
	}
	if version == 1 && path != ":memory:" {
		if _, e = s.createMigrationBackup(ctx, path); e != nil {
			db.Close()
			return nil, fmt.Errorf("create schema migration backup: %w", e)
		}
	}
	if e = migrateV2(ctx, db, identity); e != nil {
		db.Close()
		return nil, e
	}
	return s, nil
}
func (s *Store) Close() error                   { return s.db.Close() }
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

type transaction struct {
	ctx      context.Context
	tx       *sql.Tx
	election election.Identity
}

func (s *Store) Transaction(ctx context.Context, f func(ports.Transaction) error) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = f(&transaction{ctx: ctx, tx: tx, election: s.election}); e != nil {
		return e
	}
	return tx.Commit()
}
func (t *transaction) Latest() (ports.StoredSnapshot, error) {
	var r ports.StoredSnapshot
	var body string
	e := t.tx.QueryRowContext(t.ctx, "SELECT body,etag,modified FROM snapshot WHERE id=1").Scan(&body, &r.Validators.ETag, &r.Validators.LastModified)
	if errors.Is(e, sql.ErrNoRows) {
		return r, ports.ErrNoSnapshot
	}
	if e != nil {
		return r, e
	}
	e = json.Unmarshal([]byte(body), &r.Snapshot)
	return r, e
}
func (t *transaction) Save(s election.Snapshot, v ports.Validators) error {
	b, e := json.Marshal(s)
	if e != nil {
		return e
	}
	_, e = t.tx.ExecContext(t.ctx, "INSERT INTO snapshot VALUES(1,?,?,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body,etag=excluded.etag,modified=excluded.modified", string(b), v.ETag, v.LastModified)
	return e
}
func (s *Store) Latest(ctx context.Context) (r ports.StoredSnapshot, e error) {
	e = s.Transaction(ctx, func(t ports.Transaction) error { var x error; r, x = t.Latest(); return x })
	return
}
func (t *transaction) Status() (ports.MonitoringStatus, error) {
	var r ports.MonitoringStatus
	var b string
	e := t.tx.QueryRowContext(t.ctx, "SELECT body FROM monitoring WHERE id=1").Scan(&b)
	if e == nil {
		e = json.Unmarshal([]byte(b), &r)
	}
	return r, e
}
func (t *transaction) SetStatus(r ports.MonitoringStatus) error {
	b, e := json.Marshal(r)
	if e != nil {
		return e
	}
	_, e = t.tx.ExecContext(t.ctx, "UPDATE monitoring SET body=? WHERE id=1", string(b))
	return e
}
func (s *Store) Status(ctx context.Context) (r ports.MonitoringStatus, e error) {
	e = s.Transaction(ctx, func(t ports.Transaction) error { var x error; r, x = t.Status(); return x })
	return
}
func scanSub(row interface{ Scan(...any) error }, electionID election.Identity) (subscription.Subscription, error) {
	var s subscription.Subscription
	var channel string
	var recipient, message string
	var next int64
	e := row.Scan(&s.ID, &channel, &recipient, &s.Active, &s.Generation, &s.Baseline, &s.ResultsEnabled, &s.MilestonesEnabled, &message, &next)
	if errors.Is(e, sql.ErrNoRows) {
		return s, ports.ErrNotFound
	}
	s.Identity = subscription.Identity{Election: electionID, Channel: subscription.Channel(channel), Recipient: recipient}
	if channel == string(subscription.Telegram) {
		s.ChatID, _ = strconv.ParseInt(recipient, 10, 64)
	}
	s.NextOutboundAt = unixTime(next)
	if message != "" {
		s.MessageID, _ = strconv.ParseInt(message, 10, 64)
	}
	return s, e
}

const subColumns = "id,channel,recipient,active,generation,baseline,results_enabled,milestones_enabled,message_id,next_outbound_at"

func (t *transaction) Subscription(chat int64) (subscription.Subscription, error) {
	identity, err := subscription.NewIdentity(t.election, subscription.Telegram, strconv.FormatInt(chat, 10))
	if err != nil {
		return subscription.Subscription{}, err
	}
	return t.SubscriptionByIdentity(identity)
}
func (t *transaction) Subscriptions() ([]subscription.Subscription, error) {
	return t.SubscriptionsFor(t.election, subscription.Telegram)
}
func (t *transaction) SubscriptionByIdentity(identity subscription.Identity) (subscription.Subscription, error) {
	if identity.Election != t.election {
		return subscription.Subscription{}, ports.ErrNotFound
	}
	return scanSub(t.tx.QueryRowContext(t.ctx, "SELECT "+subColumns+" FROM subscriptions WHERE election_key=? AND channel=? AND recipient=?", electionKey(identity.Election), identity.Channel, identity.Recipient), identity.Election)
}
func (t *transaction) SubscriptionsFor(electionID election.Identity, channel subscription.Channel) ([]subscription.Subscription, error) {
	query := "SELECT " + subColumns + " FROM subscriptions WHERE election_key=? AND active=1"
	args := []any{electionKey(electionID)}
	if channel != "" {
		query += " AND channel=?"
		args = append(args, channel)
	}
	query += " ORDER BY channel,recipient"
	rows, e := t.tx.QueryContext(t.ctx, query, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var r []subscription.Subscription
	for rows.Next() {
		s, e := scanSub(rows, electionID)
		if e != nil {
			return nil, e
		}
		r = append(r, s)
	}
	return r, rows.Err()
}
func (t *transaction) PutSubscription(s subscription.Subscription) error {
	identity := s.Identity
	if identity.Channel == "" {
		var err error
		identity, err = subscription.NewIdentity(t.election, subscription.Telegram, strconv.FormatInt(s.ChatID, 10))
		if err != nil {
			return err
		}
	}
	if identity.Election != t.election {
		return errors.New("subscription election does not match store")
	}
	message := ""
	if identity.Channel == subscription.Telegram && s.MessageID != 0 {
		message = strconv.FormatInt(s.MessageID, 10)
	}
	_, e := t.tx.ExecContext(t.ctx, `INSERT INTO subscriptions(election_key,channel,recipient,active,generation,baseline,results_enabled,milestones_enabled,message_id,next_outbound_at)
	VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(election_key,channel,recipient) DO UPDATE SET active=excluded.active,generation=excluded.generation,baseline=excluded.baseline,results_enabled=excluded.results_enabled,milestones_enabled=excluded.milestones_enabled,
	message_id=CASE WHEN subscriptions.generation=excluded.generation THEN subscriptions.message_id ELSE '' END,
		original_sent_at=CASE WHEN subscriptions.generation=excluded.generation THEN subscriptions.original_sent_at ELSE NULL END,
	last_edit_at=CASE WHEN subscriptions.generation=excluded.generation THEN subscriptions.last_edit_at ELSE NULL END,
	last_summary_at=CASE WHEN subscriptions.generation=excluded.generation THEN subscriptions.last_summary_at ELSE NULL END,
	last_activity_at=CASE WHEN subscriptions.generation=excluded.generation THEN subscriptions.last_activity_at ELSE NULL END,
	accepted_semantic_revision=CASE WHEN subscriptions.generation=excluded.generation THEN subscriptions.accepted_semantic_revision ELSE '' END,
	accepted_presentation_revision=CASE WHEN subscriptions.generation=excluded.generation THEN subscriptions.accepted_presentation_revision ELSE '' END,
	edit_unavailable=CASE WHEN subscriptions.generation=excluded.generation THEN subscriptions.edit_unavailable ELSE 0 END,
	next_outbound_at=CASE WHEN subscriptions.generation=excluded.generation THEN subscriptions.next_outbound_at ELSE 0 END`, electionKey(identity.Election), identity.Channel, identity.Recipient, s.Active, s.Generation, s.Baseline, s.ResultsEnabled, s.MilestonesEnabled, message, timestamp(s.NextOutboundAt))
	return e
}
func (t *transaction) Cancel(chat int64) error {
	identity, err := subscription.NewIdentity(t.election, subscription.Telegram, strconv.FormatInt(chat, 10))
	if err != nil {
		return err
	}
	return t.CancelIdentity(identity)
}

func (t *transaction) CancelIdentity(identity subscription.Identity) error {
	if identity.Election != t.election {
		return errors.New("subscription election does not match transaction")
	}
	_, e := t.tx.ExecContext(t.ctx, `UPDATE outbox SET state='cancelled' WHERE subscription_id=(SELECT id FROM subscriptions WHERE election_key=? AND channel=? AND recipient=?) AND state='pending'`, electionKey(identity.Election), identity.Channel, identity.Recipient)
	return e
}
func (t *transaction) Enqueue(d notification.Delivery) error {
	identity := d.Identity
	if identity.Channel == "" {
		var err error
		identity, err = subscription.NewIdentity(t.election, subscription.Telegram, strconv.FormatInt(d.ChatID, 10))
		if err != nil {
			return err
		}
	}
	var subscriberID, generation int64
	e := t.tx.QueryRowContext(t.ctx, `SELECT id,generation FROM subscriptions WHERE election_key=? AND channel=? AND recipient=? AND active=1`, electionKey(identity.Election), identity.Channel, identity.Recipient).Scan(&subscriberID, &generation)
	if errors.Is(e, sql.ErrNoRows) {
		return ports.ErrNotFound
	}
	if e != nil {
		return e
	}
	if d.Generation == 0 {
		d.Generation = generation
	}
	if d.Generation != generation {
		return nil
	}
	eventKey := d.EventKey
	if eventKey == "" {
		switch d.Kind {
		case "main":
			eventKey = "main:" + strconv.FormatInt(d.Generation, 10)
		case "milestone":
			eventKey = "milestone:" + strconv.Itoa(d.Milestone)
		default:
			eventKey = d.Kind + ":" + d.Revision
		}
	}
	if strings.HasPrefix(eventKey, "legacy:") {
		return errors.New("reserved legacy outbox event key")
	}
	semantic := d.SemanticRevision
	if semantic == "" {
		semantic = d.Revision
		if i := strings.IndexByte(semantic, ':'); i >= 0 {
			semantic = semantic[:i]
		}
	}
	if d.Kind == "main" {
		// Historical keys are preserved; only this generation's live coalescing key is updated.
		if _, e = t.tx.ExecContext(t.ctx, `UPDATE outbox SET state='superseded',legacy_state='pending',superseded_reason='newer desired presentation' WHERE subscription_id=? AND generation=? AND kind='main' AND state='pending' AND event_key NOT LIKE 'main:%'`, subscriberID, d.Generation); e != nil {
			return e
		}
		_, e = t.tx.ExecContext(t.ctx, `INSERT INTO outbox(subscription_id,generation,kind,event_key,milestone,revision,semantic_revision,text,due,attempts,state,last_error,legacy_state)
		VALUES(?,?,'main',?,0,?,?,?,?,0,'pending','', 'pending') ON CONFLICT(subscription_id,kind,event_key) DO UPDATE SET generation=excluded.generation,revision=excluded.revision,semantic_revision=excluded.semantic_revision,text=excluded.text,due=CASE WHEN outbox.state='pending' AND outbox.attempts>0 THEN outbox.due ELSE excluded.due END,attempts=CASE WHEN outbox.state='pending' THEN outbox.attempts ELSE 0 END,legacy_state=CASE WHEN outbox.state='pending' THEN outbox.legacy_state ELSE outbox.state END,state='pending',last_error='' WHERE outbox.revision<>excluded.revision AND outbox.state IN ('pending','delivered','failed')`, subscriberID, d.Generation, eventKey, d.Revision, semantic, d.Text, d.Due.UnixNano())
		return e
	}
	_, e = t.tx.ExecContext(t.ctx, `INSERT OR IGNORE INTO outbox(subscription_id,generation,kind,event_key,milestone,revision,semantic_revision,text,due,attempts,state,last_error,legacy_state) VALUES(?,?,?,?,?,?,?,?,?,0,'pending','','pending')`, subscriberID, d.Generation, d.Kind, eventKey, d.Milestone, d.Revision, semantic, d.Text, d.Due.UnixNano())
	return e
}
func (s *Store) Pending(ctx context.Context, now time.Time, limit int) ([]notification.Delivery, error) {
	return s.PendingChannel(ctx, subscription.Telegram, now, limit)
}
func (s *Store) Complete(ctx context.Context, d notification.Delivery, message int64, now time.Time) error {
	return s.Transaction(ctx, func(p ports.Transaction) error {
		t := p.(*transaction)
		identity, err := t.deliveryIdentity(d)
		if err != nil {
			return err
		}
		sub, err := t.SubscriptionByIdentity(identity)
		if err != nil {
			return err
		}
		if sub.Generation != d.Generation || !sub.Active {
			return nil
		}
		if d.Kind == "main" {
			if _, err = t.tx.ExecContext(ctx, `UPDATE subscriptions SET message_id=?,original_sent_at=CASE WHEN message_id='' THEN ? ELSE original_sent_at END,last_summary_at=CASE WHEN message_id='' THEN ? ELSE last_summary_at END,last_activity_at=? WHERE id=? AND generation=? AND active=1`, strconv.FormatInt(message, 10), now.UnixNano(), now.UnixNano(), now.UnixNano(), sub.ID, d.Generation); err != nil {
				return err
			}
		}
		_, err = t.tx.ExecContext(ctx, "UPDATE outbox SET state='delivered',last_error='' WHERE id=? AND revision=? AND generation=? AND state='pending'", d.ID, d.Revision, d.Generation)
		return err
	})
}
func (s *Store) Reschedule(ctx context.Context, d notification.Delivery, due time.Time, kind string) error {
	_, e := s.db.ExecContext(ctx, "UPDATE outbox SET due=?,attempts=attempts+1,last_error=? WHERE id=? AND revision=? AND generation=? AND state='pending'", due.UnixNano(), kind, d.ID, d.Revision, d.Generation)
	return e
}
func (s *Store) MarkUndeliverable(ctx context.Context, d notification.Delivery, kind string) error {
	return s.Transaction(ctx, func(p ports.Transaction) error {
		t := p.(*transaction)
		identity, err := t.deliveryIdentity(d)
		if err != nil {
			return err
		}
		sub, err := t.SubscriptionByIdentity(identity)
		if errors.Is(err, ports.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if sub.Generation != d.Generation {
			return nil
		}
		if kind == "blocked" || kind == "invalid_chat" {
			if sub.Identity.Channel == subscription.Telegram {
				sub.Active = false
				if err = t.PutSubscription(sub); err != nil {
					return err
				}
			}
			_, err = t.tx.ExecContext(ctx, "UPDATE outbox SET state='undeliverable',last_error=? WHERE subscription_id=? AND generation=? AND state='pending'", kind, sub.ID, d.Generation)
			return err
		}
		_, err = t.tx.ExecContext(ctx, "UPDATE outbox SET state='failed',last_error=? WHERE id=? AND revision=? AND generation=? AND state='pending'", kind, d.ID, d.Revision, d.Generation)
		return err
	})
}
func (s *Store) Offset(ctx context.Context) (int64, error) {
	var o int64
	e := s.db.QueryRowContext(ctx, "SELECT value FROM command_offset WHERE id=1").Scan(&o)
	return o, e
}
func (s *Store) SetOffset(ctx context.Context, o int64) error {
	_, e := s.db.ExecContext(ctx, "UPDATE command_offset SET value=max(value,?) WHERE id=1", o)
	return e
}
