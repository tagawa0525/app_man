CREATE TABLE licenses (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  product_id INTEGER NOT NULL REFERENCES products(id),
  owning_department_id INTEGER NOT NULL REFERENCES departments(id),
  license_slug TEXT NOT NULL,
  display_name TEXT NOT NULL,
  total_count INTEGER,
  count_unit TEXT NOT NULL,
  contract_type TEXT NOT NULL,
  purchased_at DATE,
  started_at DATE,
  expires_at DATE,
  vendor_order_no TEXT,
  purchaser TEXT,
  unit_price INTEGER,
  currency TEXT DEFAULT 'JPY',
  product_keys TEXT,
  fs_dir_path TEXT NOT NULL,
  note TEXT,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE(product_id, owning_department_id, license_slug)
);

CREATE TABLE license_documents (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  license_id INTEGER NOT NULL REFERENCES licenses(id),
  doc_type TEXT NOT NULL,
  stored_path TEXT NOT NULL,
  original_filename TEXT NOT NULL,
  sha256 TEXT NOT NULL,
  mime_type TEXT,
  size_bytes INTEGER,
  uploaded_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  uploaded_by_app_user_id INTEGER REFERENCES app_users(id),
  note TEXT
);

CREATE TABLE user_assignments (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  license_id INTEGER NOT NULL REFERENCES licenses(id),
  user_id INTEGER NOT NULL REFERENCES users(id),
  external_account_id TEXT,
  provisioned_at DATETIME,
  deprovisioned_at DATETIME,
  assigned_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  revoked_at DATETIME,
  note TEXT,
  UNIQUE(license_id, user_id, revoked_at)
);

CREATE TABLE device_assignments (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  license_id INTEGER NOT NULL REFERENCES licenses(id),
  device_id INTEGER NOT NULL REFERENCES devices(id),
  assigned_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  revoked_at DATETIME,
  note TEXT,
  UNIQUE(license_id, device_id, revoked_at)
);
