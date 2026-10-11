package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"

	"github.com/Diaszano/election-bot/internal/domain/election"
	"github.com/Diaszano/election-bot/migrations"
)

type legacyRow struct {
	ID        int64
	Chat      int64
	Kind      string
	Milestone int
}

func electionIdentityKey(e election.Identity) string {
	return fmt.Sprintf("%d/%d/%d/%q/%q", e.Year, e.Round, e.Office, e.Code, e.Scope)
}

type legacyDelivery struct {
	legacyRow
	Generation int64
	Revision   string
	Text       string
	Due        int64
	Attempts   int
	State      string
	LastError  string
}

func legacyEventKeys(rows []legacyRow) (map[int64]string, error) {
	result := make(map[int64]string, len(rows))
	groups := make(map[string][]legacyRow)
	byID := make(map[int64]legacyRow, len(rows))
	ids := make(map[int64]bool, len(rows))
	for _, row := range rows {
		if row.ID <= 0 || row.Chat <= 0 || ids[row.ID] {
			return nil, errors.New("invalid legacy outbox identity")
		}
		ids[row.ID] = true
		byID[row.ID] = row
		groups[fmt.Sprintf("%d\x00%s", row.Chat, row.Kind)] = append(groups[fmt.Sprintf("%d\x00%s", row.Chat, row.Kind)], row)
	}
	for _, group := range groups {
		canonical := int64(0)
		for _, row := range group {
			if row.Kind != "main" {
				continue
			}
			current := byID[canonical]
			if canonical == 0 || (row.Milestone == 0 && current.Milestone != 0) || (row.Milestone == current.Milestone && row.ID < canonical) {
				canonical = row.ID
			}
		}
		seen := map[string]bool{}
		for _, row := range group {
			key := ""
			switch row.Kind {
			case "main":
				if row.ID == canonical {
					key = "main"
				}
			case "milestone":
				key = "milestone:" + strconv.Itoa(row.Milestone)
			}
			if key == "" || seen[key] {
				key = "legacy:" + strconv.FormatInt(row.ID, 10)
			}
			seen[key] = true
			result[row.ID] = key
		}
	}
	return result, nil
}

func migrateV2(ctx context.Context, db *sql.DB, electionID election.Identity) error {
	return migrateV2WithHook(ctx, db, electionID, nil)
}

func migrateV2WithHook(ctx context.Context, db *sql.DB, electionID election.Identity, hook func(string) error) (err error) {
	if electionID.Year <= 0 || electionID.Round <= 0 || electionID.Code == "" || electionID.Scope == "" || electionID.Office <= 0 {
		return errors.New("invalid migration election identity")
	}
	if _, err = db.ExecContext(ctx, `PRAGMA foreign_keys=ON`); err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	var version int
	if err = tx.QueryRowContext(ctx, `SELECT max(version) FROM schema_version`).Scan(&version); err != nil {
		return err
	}
	if version == 2 {
		return tx.Commit()
	}
	if version != 1 {
		return fmt.Errorf("unsupported migration version")
	}
	if _, err = tx.ExecContext(ctx, `PRAGMA defer_foreign_keys=ON`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, migrations.ChannelNotifications); err != nil {
		return err
	}
	if hook != nil {
		if err = hook("after_ddl"); err != nil {
			return err
		}
	}
	identityKey := fmt.Sprintf("%d/%d/%d/%q/%q", electionID.Year, electionID.Round, electionID.Office, electionID.Code, electionID.Scope)
	subs, err := tx.QueryContext(ctx, `SELECT chat,active,generation,baseline,results_enabled,milestones_enabled,message_id FROM subscriptions ORDER BY chat`)
	if err != nil {
		return err
	}
	type legacySub struct {
		chat                                                       int64
		active, generation, baseline, results, milestones, message int64
	}
	var oldSubs []legacySub
	for subs.Next() {
		var s legacySub
		if err = subs.Scan(&s.chat, &s.active, &s.generation, &s.baseline, &s.results, &s.milestones, &s.message); err != nil {
			_ = subs.Close()
			return err
		}
		oldSubs = append(oldSubs, s)
	}
	if err = subs.Err(); err != nil {
		_ = subs.Close()
		return err
	}
	if err = subs.Close(); err != nil {
		return err
	}
	chatToID := make(map[int64]int64, len(oldSubs))
	for n, s := range oldSubs {
		id := int64(n + 1)
		chatToID[s.chat] = id
		message := ""
		if s.message != 0 {
			message = strconv.FormatInt(s.message, 10)
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO subscriptions_v2(id,election_key,channel,recipient,active,generation,baseline,results_enabled,milestones_enabled,message_id) VALUES(?,?,'telegram',?,?,?,?,?,?,?)`, id, identityKey, strconv.FormatInt(s.chat, 10), s.active, s.generation, s.baseline, s.results, s.milestones, message); err != nil {
			return err
		}
	}
	if hook != nil {
		if err = hook("mid_copy"); err != nil {
			return err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,chat,generation,kind,milestone,revision,text,due,attempts,state,last_error FROM outbox ORDER BY id`)
	if err != nil {
		return err
	}
	var jobs []legacyDelivery
	for rows.Next() {
		var d legacyDelivery
		if err = rows.Scan(&d.ID, &d.Chat, &d.Generation, &d.Kind, &d.Milestone, &d.Revision, &d.Text, &d.Due, &d.Attempts, &d.State, &d.LastError); err != nil {
			_ = rows.Close()
			return err
		}
		jobs = append(jobs, d)
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	legacyRows := make([]legacyRow, 0, len(jobs))
	for _, job := range jobs {
		if _, ok := chatToID[job.Chat]; !ok {
			return errors.New("legacy outbox references missing subscription")
		}
		legacyRows = append(legacyRows, job.legacyRow)
	}
	eventKeys, err := legacyEventKeys(legacyRows)
	if err != nil {
		return err
	}
	accepted := make(map[int64]legacyDelivery)
	for _, job := range jobs {
		if job.Kind != "main" || job.State != "delivered" {
			continue
		}
		previous, ok := accepted[job.Chat]
		if !ok || job.Due > previous.Due || (job.Due == previous.Due && job.ID > previous.ID) {
			accepted[job.Chat] = job
		}
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].ID < jobs[j].ID })
	for _, job := range jobs {
		key := eventKeys[job.ID]
		state, legacyState, reason := job.State, job.State, ""
		if job.Kind == "main" && job.State == "pending" {
			if baseline, ok := accepted[job.Chat]; ok && baseline.Generation == job.Generation {
				switch {
				case job.Revision == baseline.Revision:
					state, reason = "superseded", "presentation already accepted"
				case job.Attempts > 0:
					state, reason = "review_required", "retry due time obscures presentation precedence"
				case job.Due < baseline.Due || (job.Due == baseline.Due && job.ID <= baseline.ID):
					state, reason = "superseded", "newer presentation already accepted"
				}
			}
		}
		semantic := job.Revision
		if i := indexByte(semantic, ':'); i >= 0 {
			semantic = semantic[:i]
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO outbox_v2(id,subscription_id,generation,kind,event_key,milestone,revision,semantic_revision,text,due,attempts,state,last_error,legacy_state,superseded_reason) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, job.ID, chatToID[job.Chat], job.Generation, job.Kind, key, job.Milestone, job.Revision, semantic, job.Text, job.Due, job.Attempts, state, job.LastError, legacyState, reason); err != nil {
			return err
		}
	}
	if err = validateCopiedRows(ctx, tx, len(oldSubs), len(jobs)); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DROP TABLE outbox; DROP TABLE subscriptions; ALTER TABLE subscriptions_v2 RENAME TO subscriptions; ALTER TABLE outbox_v2 RENAME TO outbox; DROP INDEX outbox_v2_due; CREATE INDEX outbox_due ON outbox(state,due,id); INSERT INTO channel_state(channel) VALUES('telegram'),('whatsapp'); INSERT INTO schema_version(version) VALUES(2);`); err != nil {
		return err
	}
	if err = validateIntegrity(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

func validateCopiedRows(ctx context.Context, tx *sql.Tx, wantSubs, wantJobs int) error {
	for table, want := range map[string]int{"subscriptions_v2": wantSubs, "outbox_v2": wantJobs} {
		var got int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM `+table).Scan(&got); err != nil {
			return err
		}
		if got != want {
			return fmt.Errorf("migration row count mismatch")
		}
	}
	var duplicate int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM (SELECT subscription_id,kind,event_key FROM outbox_v2 GROUP BY subscription_id,kind,event_key HAVING count(*)>1)`).Scan(&duplicate); err != nil {
		return err
	}
	if duplicate != 0 {
		return errors.New("migration event key collision")
	}
	return nil
}

func validateIntegrity(ctx context.Context, tx *sql.Tx) error {
	var table, parent string
	var rowID, fkID sql.NullInt64
	rows, err := tx.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	if rows.Next() {
		_ = rows.Scan(&table, &rowID, &parent, &fkID)
		_ = rows.Close()
		return errors.New("migration foreign key check failed")
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	rows, err = tx.QueryContext(ctx, `PRAGMA integrity_check`)
	if err != nil {
		return err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var result string
		if err = rows.Scan(&result); err != nil {
			return err
		}
		count++
		if result != "ok" {
			return errors.New("migration integrity check failed")
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if count != 1 {
		return errors.New("migration integrity check returned no result")
	}
	return nil
}

func indexByte(s string, b byte) int {
	for i := range s {
		if s[i] == b {
			return i
		}
	}
	return -1
}
