CREATE TABLE app_users (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  username TEXT NOT NULL UNIQUE,
  password_hash TEXT,
  linked_user_id INTEGER REFERENCES users(id),
  notify_email TEXT,
  auth_type TEXT NOT NULL DEFAULT 'local',
  disabled_at DATETIME,
  last_login_at DATETIME,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE user_department_roles (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  app_user_id INTEGER NOT NULL REFERENCES app_users(id),
  department_id INTEGER REFERENCES departments(id),
  role TEXT NOT NULL,
  granted_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  revoked_at DATETIME,
  UNIQUE(app_user_id, department_id, role, revoked_at)
);

CREATE TABLE inventory_audits (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  department_id INTEGER NOT NULL REFERENCES departments(id),
  fiscal_period TEXT NOT NULL,
  initiated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  initiated_by_app_user_id INTEGER REFERENCES app_users(id),
  due_date DATE,
  status TEXT NOT NULL DEFAULT 'pending',
  completed_at DATETIME,
  completed_by_app_user_id INTEGER REFERENCES app_users(id),
  result_note TEXT,
  snapshot_json TEXT,
  UNIQUE(department_id, fiscal_period)
);

CREATE TABLE audit_logs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  app_user_id INTEGER REFERENCES app_users(id),
  action TEXT NOT NULL,
  entity_type TEXT NOT NULL,
  entity_id INTEGER,
  diff_json TEXT,
  occurred_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE app_settings (
  key TEXT PRIMARY KEY,
  value TEXT,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_by_app_user_id INTEGER REFERENCES app_users(id)
);

CREATE TABLE notifications (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  kind TEXT NOT NULL,
  channel TEXT NOT NULL,
  recipient TEXT NOT NULL,
  subject TEXT,
  body TEXT,
  related_entity_type TEXT,
  related_entity_id INTEGER,
  status TEXT NOT NULL DEFAULT 'pending',
  retry_count INTEGER NOT NULL DEFAULT 0,
  last_attempted_at DATETIME,
  last_error TEXT,
  sent_at DATETIME,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
