CREATE TABLE subscriptions_v2(
    id INTEGER PRIMARY KEY,
    election_key TEXT NOT NULL,
    channel TEXT NOT NULL CHECK(channel IN ('telegram','whatsapp')),
    recipient TEXT NOT NULL,
    active INTEGER NOT NULL CHECK(active IN (0,1)),
    generation INTEGER NOT NULL,
    baseline INTEGER NOT NULL,
    results_enabled INTEGER NOT NULL CHECK(results_enabled IN (0,1)),
    milestones_enabled INTEGER NOT NULL CHECK(milestones_enabled IN (0,1)),
    message_id TEXT NOT NULL DEFAULT '',
    original_sent_at INTEGER,
    last_edit_at INTEGER,
    last_summary_at INTEGER,
    last_activity_at INTEGER,
    accepted_semantic_revision TEXT NOT NULL DEFAULT '',
    accepted_presentation_revision TEXT NOT NULL DEFAULT '',
    edit_unavailable INTEGER NOT NULL DEFAULT 0 CHECK(edit_unavailable IN (0,1)),
    next_outbound_at INTEGER NOT NULL DEFAULT 0,
    UNIQUE(election_key,channel,recipient)
);

CREATE TABLE outbox_v2(
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    subscription_id INTEGER NOT NULL REFERENCES subscriptions_v2(id),
    generation INTEGER NOT NULL,
    kind TEXT NOT NULL,
    event_key TEXT NOT NULL,
    milestone INTEGER NOT NULL,
    revision TEXT NOT NULL,
    semantic_revision TEXT NOT NULL DEFAULT '',
    text TEXT NOT NULL,
    due INTEGER NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0,
    state TEXT NOT NULL DEFAULT 'pending',
    last_error TEXT NOT NULL DEFAULT '',
    legacy_state TEXT NOT NULL,
    superseded_reason TEXT NOT NULL DEFAULT '',
    UNIQUE(subscription_id,kind,event_key)
);

CREATE INDEX outbox_v2_due ON outbox_v2(state,due,id);

CREATE TABLE delivery_attempts(
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    outbox_id INTEGER NOT NULL REFERENCES outbox_v2(id),
    generation INTEGER NOT NULL,
    operation TEXT NOT NULL CHECK(operation IN ('send','edit')),
    request_id TEXT NOT NULL UNIQUE,
    revision TEXT NOT NULL,
    text TEXT NOT NULL,
    target_message_id TEXT NOT NULL DEFAULT '',
    prepared_at INTEGER NOT NULL,
    accepted_at INTEGER,
    outcome TEXT NOT NULL DEFAULT 'prepared'
);

CREATE TABLE inbound_commands(
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    subscription_id INTEGER NOT NULL REFERENCES subscriptions_v2(id),
    incoming_message_id TEXT NOT NULL,
    verb TEXT NOT NULL,
    received_at INTEGER NOT NULL,
    UNIQUE(subscription_id,incoming_message_id)
);

CREATE TABLE subscription_audit(
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    subscription_id INTEGER NOT NULL REFERENCES subscriptions_v2(id),
    generation INTEGER NOT NULL,
    action TEXT NOT NULL CHECK(action IN ('opt_in','opt_out')),
    incoming_message_id TEXT NOT NULL,
    occurred_at INTEGER NOT NULL
);

CREATE TABLE channel_state(
    channel TEXT PRIMARY KEY CHECK(channel IN ('telegram','whatsapp')),
    global_next_send_at INTEGER NOT NULL DEFAULT 0,
    paused_until INTEGER NOT NULL DEFAULT 0,
    authentication_required INTEGER NOT NULL DEFAULT 0 CHECK(authentication_required IN (0,1)),
    account_binding TEXT NOT NULL DEFAULT '',
    pause_reason TEXT NOT NULL DEFAULT ''
);
