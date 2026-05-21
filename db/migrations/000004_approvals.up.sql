CREATE TABLE department_product_approvals (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  department_id INTEGER NOT NULL REFERENCES departments(id),
  product_id INTEGER NOT NULL REFERENCES products(id),
  status TEXT NOT NULL,
  scope_type TEXT NOT NULL DEFAULT 'department_wide',
  conditions TEXT,
  approved_by_app_user_id INTEGER REFERENCES app_users(id),
  approved_at DATETIME,
  expires_at DATETIME,
  revoked_at DATETIME,
  revoked_by_app_user_id INTEGER REFERENCES app_users(id),
  revoke_reason TEXT,
  approval_source TEXT NOT NULL DEFAULT 'direct',
  source_request_id INTEGER REFERENCES approval_requests(id),
  note TEXT,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE(department_id, product_id, revoked_at)
);

CREATE TABLE approval_scope_users (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  approval_id INTEGER NOT NULL REFERENCES department_product_approvals(id),
  user_id INTEGER NOT NULL REFERENCES users(id),
  UNIQUE(approval_id, user_id)
);

CREATE TABLE approval_scope_devices (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  approval_id INTEGER NOT NULL REFERENCES department_product_approvals(id),
  device_id INTEGER NOT NULL REFERENCES devices(id),
  UNIQUE(approval_id, device_id)
);

CREATE TABLE approval_requests (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  product_id INTEGER NOT NULL REFERENCES products(id),
  department_id INTEGER NOT NULL REFERENCES departments(id),
  requester_employee_code TEXT,
  requester_email TEXT,
  requested_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  purpose TEXT,
  status TEXT NOT NULL DEFAULT 'pending',
  decided_at DATETIME,
  decided_by_app_user_id INTEGER REFERENCES app_users(id),
  decision_note TEXT,
  resulting_approval_id INTEGER REFERENCES department_product_approvals(id)
);
