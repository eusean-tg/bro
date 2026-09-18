CREATE TABLE IF NOT EXISTS messages (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  workspace  TEXT    NOT NULL,
  sender     TEXT    NOT NULL,
  kind       TEXT    NOT NULL DEFAULT 'note',   -- 'note' | 'control'
  body       TEXT    NOT NULL,
  created_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS messages_ws_id ON messages(workspace, id);

-- A row per addressed mailbox. No rows means "to the room".
CREATE TABLE IF NOT EXISTS mentions (
  message_id      INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
  mailbox TEXT    NOT NULL,
  PRIMARY KEY (message_id, mailbox)
);

CREATE TABLE IF NOT EXISTS mailboxes (
  workspace   TEXT    NOT NULL,
  name        TEXT    NOT NULL,
  description TEXT    NOT NULL DEFAULT '',
  cursor      INTEGER NOT NULL DEFAULT 0,
  created_at  INTEGER NOT NULL,
  PRIMARY KEY (workspace, name)
);

CREATE TABLE IF NOT EXISTS workspaces (
  name       TEXT    PRIMARY KEY,
  closed_id INTEGER                        -- id of the unit-complete message
);
