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
	"time"
)

type Store struct{ db *sql.DB }

func Open(ctx context.Context, path string) (*Store, error) {
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
	s := &Store{db}
	for _, q := range []string{"PRAGMA journal_mode=WAL", "PRAGMA foreign_keys=ON", "PRAGMA busy_timeout=5000", "PRAGMA synchronous=FULL"} {
		if _, e = db.ExecContext(ctx, q); e != nil {
			db.Close()
			return nil, e
		}
	}
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		db.Close()
		return nil, e
	}
	defer tx.Rollback()
	var version int
	e = tx.QueryRowContext(ctx, "SELECT max(version) FROM schema_version").Scan(&version)
	if e == nil && version != 1 {
		db.Close()
		return nil, fmt.Errorf("unsupported migration version")
	}
	if _, e = tx.ExecContext(ctx, migrations.Initial); e != nil {
		db.Close()
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		db.Close()
		return nil, e
	}
	return s, nil
}
func (s *Store) Close() error                   { return s.db.Close() }
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

type transaction struct {
	ctx context.Context
	tx  *sql.Tx
}

func (s *Store) Transaction(ctx context.Context, f func(ports.Transaction) error) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = f(&transaction{ctx, tx}); e != nil {
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
func scanSub(row interface{ Scan(...any) error }) (subscription.Subscription, error) {
	var s subscription.Subscription
	e := row.Scan(&s.ChatID, &s.Active, &s.Generation, &s.Baseline, &s.ResultsEnabled, &s.MilestonesEnabled)
	if errors.Is(e, sql.ErrNoRows) {
		return s, ports.ErrNotFound
	}
	return s, e
}

const subColumns = "chat,active,generation,baseline,results_enabled,milestones_enabled"

func (t *transaction) Subscription(chat int64) (subscription.Subscription, error) {
	return scanSub(t.tx.QueryRowContext(t.ctx, "SELECT "+subColumns+" FROM subscriptions WHERE chat=?", chat))
}
func (t *transaction) Subscriptions() ([]subscription.Subscription, error) {
	rows, e := t.tx.QueryContext(t.ctx, "SELECT "+subColumns+" FROM subscriptions WHERE active=1 ORDER BY chat")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var r []subscription.Subscription
	for rows.Next() {
		s, e := scanSub(rows)
		if e != nil {
			return nil, e
		}
		r = append(r, s)
	}
	return r, rows.Err()
}
func (t *transaction) PutSubscription(s subscription.Subscription) error {
	_, e := t.tx.ExecContext(t.ctx, "INSERT INTO subscriptions(chat,active,generation,baseline,results_enabled,milestones_enabled) VALUES(?,?,?,?,?,?) ON CONFLICT(chat) DO UPDATE SET active=excluded.active,generation=excluded.generation,baseline=excluded.baseline,results_enabled=excluded.results_enabled,milestones_enabled=excluded.milestones_enabled,message_id=CASE WHEN subscriptions.generation=excluded.generation THEN subscriptions.message_id ELSE 0 END", s.ChatID, s.Active, s.Generation, s.Baseline, s.ResultsEnabled, s.MilestonesEnabled)
	return e
}
func (t *transaction) Cancel(chat int64) error {
	_, e := t.tx.ExecContext(t.ctx, "UPDATE outbox SET state='cancelled' WHERE chat=? AND state='pending'", chat)
	return e
}
func (t *transaction) Enqueue(d notification.Delivery) error {
	q := "INSERT INTO outbox(chat,generation,kind,milestone,revision,text,due) VALUES(?,?,?,?,?,?,?) ON CONFLICT(chat,kind,milestone) DO NOTHING"
	if d.Kind == "main" {
		q = "INSERT INTO outbox(chat,generation,kind,milestone,revision,text,due) VALUES(?,?,?,?,?,?,?) ON CONFLICT(chat,kind,milestone) DO UPDATE SET generation=excluded.generation,revision=excluded.revision,text=excluded.text,due=excluded.due,state='pending',attempts=0,last_error='' WHERE outbox.revision<>excluded.revision OR outbox.generation<>excluded.generation"
	}
	_, e := t.tx.ExecContext(t.ctx, q, d.ChatID, d.Generation, d.Kind, d.Milestone, d.Revision, d.Text, d.Due.UnixNano())
	return e
}
func (s *Store) Pending(ctx context.Context, now time.Time, limit int) ([]notification.Delivery, error) {
	rows, e := s.db.QueryContext(ctx, "SELECT o.id,o.chat,o.generation,o.kind,o.milestone,o.revision,o.text,o.due,o.attempts,s.message_id FROM outbox o JOIN subscriptions s ON s.chat=o.chat WHERE o.state='pending' AND s.active=1 AND s.generation=o.generation AND o.due<=? ORDER BY o.id LIMIT ?", now.UnixNano(), limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var r []notification.Delivery
	for rows.Next() {
		var d notification.Delivery
		var due int64
		if e = rows.Scan(&d.ID, &d.ChatID, &d.Generation, &d.Kind, &d.Milestone, &d.Revision, &d.Text, &due, &d.Attempts, &d.MessageID); e != nil {
			return nil, e
		}
		d.Due = time.Unix(0, due)
		r = append(r, d)
	}
	return r, rows.Err()
}
func (s *Store) Complete(ctx context.Context, d notification.Delivery, message int64, now time.Time) error {
	return s.Transaction(ctx, func(p ports.Transaction) error {
		t := p.(*transaction)
		if d.Kind == "main" {
			if _, e := t.tx.ExecContext(ctx, "UPDATE subscriptions SET message_id=? WHERE chat=? AND generation=? AND active=1", message, d.ChatID, d.Generation); e != nil {
				return e
			}
		}
		_, e := t.tx.ExecContext(ctx, "UPDATE outbox SET state='delivered',last_error='' WHERE id=? AND revision=? AND generation=?", d.ID, d.Revision, d.Generation)
		return e
	})
}
func (s *Store) Reschedule(ctx context.Context, d notification.Delivery, due time.Time, kind string) error {
	_, e := s.db.ExecContext(ctx, "UPDATE outbox SET due=?,attempts=attempts+1,last_error=? WHERE id=? AND revision=? AND generation=? AND state='pending'", due.UnixNano(), kind, d.ID, d.Revision, d.Generation)
	return e
}
func (s *Store) MarkUndeliverable(ctx context.Context, d notification.Delivery, kind string) error {
	return s.Transaction(ctx, func(p ports.Transaction) error {
		t := p.(*transaction)
		if kind == "blocked" || kind == "invalid_chat" {
			sub, e := t.Subscription(d.ChatID)
			if e != nil {
				return e
			}
			if sub.Generation == d.Generation {
				sub.Active = false
				if e = t.PutSubscription(sub); e != nil {
					return e
				}
				_, e = t.tx.ExecContext(ctx, "UPDATE outbox SET state='undeliverable',last_error=? WHERE chat=? AND generation=? AND state='pending'", kind, d.ChatID, d.Generation)
				return e
			}
		}
		_, e := t.tx.ExecContext(ctx, "UPDATE outbox SET state='failed',last_error=? WHERE id=? AND revision=?", kind, d.ID, d.Revision)
		return e
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
