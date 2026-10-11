package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/Diaszano/election-bot/internal/domain/election"
	"github.com/Diaszano/election-bot/migrations"
	_ "modernc.org/sqlite"
)

func legacyDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "legacy.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(migrations.Initial); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO subscriptions(chat,active,generation,baseline,results_enabled,milestones_enabled,message_id) VALUES(42,1,7,25,1,1,922337203685477000)`); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		id               int
		kind             string
		milestone        int
		revision         string
		due              int64
		attempts         int
		state, lastError string
	}{
		{10, "main", 0, "old", 100, 0, "pending", ""},
		{11, "main", 25, "accepted-old", 200, 0, "delivered", ""},
		{12, "main", 50, "retry-ambiguous", 300, 1, "pending", "temporary"},
		{13, "main", 75, "accepted-new", 250, 0, "delivered", ""},
		{14, "main", 90, "future", 300, 0, "pending", ""},
		{15, "main", 95, "tie-newer-id", 250, 0, "pending", ""},
		{16, "main", 100, "accepted-new", 400, 0, "pending", ""},
		{17, "milestone", 25, "milestone-25", 500, 3, "failed", "rate limited"},
		{18, "milestone", 50, "milestone-50", 600, 0, "cancelled", "opt out"},
	} {
		_, err := db.Exec(`INSERT INTO outbox(id,chat,generation,kind,milestone,revision,text,due,attempts,state,last_error) VALUES(?,42,7,?,?,?,?,?,?,?,?)`, row.id, row.kind, row.milestone, row.revision, "payload-"+row.revision, row.due, row.attempts, row.state, row.lastError)
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO snapshot VALUES(1,'{"id":"generation"}','etag-value','modified-value')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE monitoring SET body='{"last_success":"2026-10-25T20:00:00Z"}' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE command_offset SET value=99 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestMigrationV1PreservesTelegram(t *testing.T) {
	db := legacyDB(t)
	e := election.Identity{Year: 2026, Round: 2, Code: "6258", Scope: "br", Office: 1}
	if err := migrateV2(context.Background(), db, e); err != nil {
		t.Fatal(err)
	}
	var version int
	if err := db.QueryRow(`SELECT max(version) FROM schema_version`).Scan(&version); err != nil || version != 2 {
		t.Fatalf("version=%d err=%v", version, err)
	}
	var active, generation, baseline, results, milestones int
	var recipient, message string
	if err := db.QueryRow(`SELECT active,generation,baseline,results_enabled,milestones_enabled,recipient,message_id FROM subscriptions`).Scan(&active, &generation, &baseline, &results, &milestones, &recipient, &message); err != nil {
		t.Fatal(err)
	}
	if active != 1 || generation != 7 || baseline != 25 || results != 1 || milestones != 1 || recipient != "42" || message != "922337203685477000" {
		t.Fatalf("Telegram state changed: %d %d %d %d %d %s %s", active, generation, baseline, results, milestones, recipient, message)
	}
	var snapshot, etag, modified string
	if err := db.QueryRow(`SELECT body,etag,modified FROM snapshot WHERE id=1`).Scan(&snapshot, &etag, &modified); err != nil {
		t.Fatal(err)
	}
	if snapshot != `{"id":"generation"}` || etag != "etag-value" || modified != "modified-value" {
		t.Fatal("snapshot validators changed")
	}
	var offset int
	if err := db.QueryRow(`SELECT value FROM command_offset WHERE id=1`).Scan(&offset); err != nil || offset != 99 {
		t.Fatalf("offset=%d err=%v", offset, err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM outbox`).Scan(&count); err != nil || count != 9 {
		t.Fatalf("outbox rows=%d err=%v", count, err)
	}
	var retryState, retryHistory, retryReason, retryKey string
	if err := db.QueryRow(`SELECT state,legacy_state,superseded_reason,event_key FROM outbox WHERE id=12`).Scan(&retryState, &retryHistory, &retryReason, &retryKey); err != nil {
		t.Fatal(err)
	}
	if retryState != "review_required" || retryHistory != "pending" || retryReason == "" || retryKey != "legacy:12" {
		t.Fatalf("ambiguous retry lost/eligible: %s %s %s %s", retryState, retryHistory, retryReason, retryKey)
	}
	var check string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&check); err != nil || check != "ok" {
		t.Fatalf("integrity=%q err=%v", check, err)
	}
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("foreign key violation")
	}
}

func TestMigrationCollisionKeys(t *testing.T) {
	keys, err := legacyEventKeys([]legacyRow{{ID: 11, Chat: 42, Kind: "main", Milestone: 0}, {ID: 12, Chat: 42, Kind: "main", Milestone: 25}, {ID: 13, Chat: 42, Kind: "main", Milestone: 50}, {ID: 14, Chat: 42, Kind: "milestone", Milestone: 25}})
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[int64]string{11: "main", 12: "legacy:12", 13: "legacy:13", 14: "milestone:25"} {
		if keys[id] != want {
			t.Fatalf("event key for %d: got %q want %q", id, keys[id], want)
		}
	}
	keys, err = legacyEventKeys([]legacyRow{{ID: 21, Chat: 42, Kind: "main", Milestone: 25}, {ID: 22, Chat: 42, Kind: "main", Milestone: 50}})
	if err != nil {
		t.Fatal(err)
	}
	if keys[21] != "main" || keys[22] != "legacy:22" {
		t.Fatalf("no-zero canonical mapping: %#v", keys)
	}
}

func TestLegacyPendingPresentationBarrier(t *testing.T) {
	db := legacyDB(t)
	if err := migrateV2(context.Background(), db, election.Identity{Year: 2026, Round: 2, Code: "6258", Scope: "br", Office: 1}); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[int64]string{10: "superseded", 12: "review_required", 14: "pending", 15: "pending", 16: "superseded"} {
		var got string
		if err := db.QueryRow(`SELECT state FROM outbox WHERE id=?`, id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("job %d state=%s want=%s", id, got, want)
		}
	}
	var oldState, reason, text string
	if err := db.QueryRow(`SELECT legacy_state,superseded_reason,text FROM outbox WHERE id=10`).Scan(&oldState, &reason, &text); err != nil {
		t.Fatal(err)
	}
	if oldState != "pending" || reason == "" || text != "payload-old" {
		t.Fatalf("superseded history lost: %s %s %s", oldState, reason, text)
	}
	var accepted, stale int
	if err := db.QueryRow(`SELECT count(*) FROM outbox WHERE state='delivered'`).Scan(&accepted); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM outbox WHERE state='superseded'`).Scan(&stale); err != nil {
		t.Fatal(err)
	}
	if accepted != 2 || stale != 2 {
		t.Fatalf("accepted=%d superseded=%d", accepted, stale)
	}
}

func TestMigrationPreservesEachOutboxRecord(t *testing.T) {
	db := legacyDB(t)
	if err := migrateV2(context.Background(), db, election.Identity{Year: 2026, Round: 2, Code: "6258", Scope: "br", Office: 1}); err != nil {
		t.Fatal(err)
	}
	want := map[int64]struct {
		kind, key, revision, text, state, legacyState, lastError string
		milestone, attempts                                      int
		due                                                      int64
	}{
		10: {"main", "main", "old", "payload-old", "superseded", "pending", "", 0, 0, 100},
		11: {"main", "legacy:11", "accepted-old", "payload-accepted-old", "delivered", "delivered", "", 25, 0, 200},
		12: {"main", "legacy:12", "retry-ambiguous", "payload-retry-ambiguous", "review_required", "pending", "temporary", 50, 1, 300},
		13: {"main", "legacy:13", "accepted-new", "payload-accepted-new", "delivered", "delivered", "", 75, 0, 250},
		14: {"main", "legacy:14", "future", "payload-future", "pending", "pending", "", 90, 0, 300},
		15: {"main", "legacy:15", "tie-newer-id", "payload-tie-newer-id", "pending", "pending", "", 95, 0, 250},
		16: {"main", "legacy:16", "accepted-new", "payload-accepted-new", "superseded", "pending", "", 100, 0, 400},
		17: {"milestone", "milestone:25", "milestone-25", "payload-milestone-25", "failed", "failed", "rate limited", 25, 3, 500},
		18: {"milestone", "milestone:50", "milestone-50", "payload-milestone-50", "cancelled", "cancelled", "opt out", 50, 0, 600},
	}
	rows, err := db.Query(`SELECT o.id,s.recipient,o.kind,o.event_key,o.milestone,o.revision,o.text,o.due,o.attempts,o.state,o.legacy_state,o.last_error FROM outbox o JOIN subscriptions s ON s.id=o.subscription_id ORDER BY o.id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var id, due int64
		var recipient, kind, key, revision, text, state, legacyState, lastError string
		var milestone, attempts int
		if err := rows.Scan(&id, &recipient, &kind, &key, &milestone, &revision, &text, &due, &attempts, &state, &legacyState, &lastError); err != nil {
			t.Fatal(err)
		}
		w, ok := want[id]
		if !ok {
			t.Fatalf("unexpected migrated row id %d", id)
		}
		if recipient != "42" || kind != w.kind || key != w.key || milestone != w.milestone || revision != w.revision || text != w.text || due != w.due || attempts != w.attempts || state != w.state || legacyState != w.legacyState || lastError != w.lastError {
			t.Errorf("row %d was changed or overwritten: recipient=%q kind=%q key=%q milestone=%d revision=%q text=%q due=%d attempts=%d state=%q legacy=%q error=%q", id, recipient, kind, key, milestone, revision, text, due, attempts, state, legacyState, lastError)
		}
		seen++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if seen != len(want) {
		t.Fatalf("preserved %d rows, want %d", seen, len(want))
	}
}

func TestMigrationFailureRollsBack(t *testing.T) {
	for _, phase := range []string{"after_ddl", "mid_copy"} {
		t.Run(phase, func(t *testing.T) {
			db := legacyDB(t)
			err := migrateV2WithHook(context.Background(), db, election.Identity{Year: 2026, Round: 2, Code: "6258", Scope: "br", Office: 1}, func(at string) error {
				if at == phase {
					return fmt.Errorf("injected migration failure")
				}
				return nil
			})
			if err == nil {
				t.Fatal("expected injected migration error")
			}
			var version int
			if e := db.QueryRow(`SELECT max(version) FROM schema_version`).Scan(&version); e != nil || version != 1 {
				t.Fatalf("version=%d err=%v", version, e)
			}
			var n int
			if e := db.QueryRow(`SELECT count(*) FROM outbox`).Scan(&n); e != nil || n != 9 {
				t.Fatalf("legacy records unusable: n=%d err=%v", n, e)
			}
		})
	}
}

func TestMigrationBackupRestore(t *testing.T) {
	ctx := context.Background()
	db := legacyDB(t)
	backup := filepath.Join(t.TempDir(), "backup.db")
	if _, err := db.Exec(`VACUUM INTO ?`, backup); err != nil {
		t.Fatal(err)
	}
	if err := migrateV2(ctx, db, election.Identity{Year: 2026, Round: 2, Code: "6258", Scope: "br", Office: 1}); err != nil {
		t.Fatal(err)
	}
	prior, err := sql.Open("sqlite", backup)
	if err != nil {
		t.Fatal(err)
	}
	defer prior.Close()
	var version int
	if err := prior.QueryRow(`SELECT max(version) FROM schema_version`).Scan(&version); err != nil || version != 1 {
		t.Fatalf("backup version=%d err=%v", version, err)
	}
	var integrity string
	if err := prior.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("backup integrity=%q err=%v", integrity, err)
	}
	fkRows, err := prior.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	if fkRows.Next() {
		t.Fatal("backup has a foreign key violation")
	}
	if err := fkRows.Err(); err != nil {
		t.Fatal(err)
	}
	_ = fkRows.Close()
	var n int
	if err := prior.QueryRow(`SELECT count(*) FROM outbox`).Scan(&n); err != nil || n != 9 {
		t.Fatalf("previous-version restore rows=%d err=%v", n, err)
	}
	var status string
	if err := prior.QueryRow(`SELECT state FROM outbox WHERE id=10`).Scan(&status); err != nil || status != "pending" {
		t.Fatalf("previous binary sees original pending state %q err=%v", status, err)
	}
	_ = prior.Close()
	previous, err := Open(ctx, backup)
	if err != nil {
		t.Fatalf("previous repository cannot reopen restored backup: %v", err)
	}
	defer previous.Close()
	pending, err := previous.Pending(ctx, time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC), 100)
	if err != nil {
		t.Fatalf("previous repository cannot read restored pending work: %v", err)
	}
	foundOld := false
	for _, delivery := range pending {
		if delivery.ID == 10 {
			foundOld = true
		}
	}
	if !foundOld {
		t.Fatal("previous repository did not recover original pending main notification")
	}
}
