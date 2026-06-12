CREATE TABLE sessions (
  id TEXT PRIMARY KEY,
  app_user_id INTEGER REFERENCES app_users(id),
  csrf_token TEXT NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  last_seen_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at DATETIME NOT NULL
);

CREATE INDEX idx_sessions_expires_at ON sessions(expires_at);
