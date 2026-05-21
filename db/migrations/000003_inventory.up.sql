CREATE TABLE import_logs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  source_type TEXT NOT NULL,
  source_file TEXT NOT NULL,
  imported_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  row_count INTEGER,
  success_count INTEGER,
  error_count INTEGER,
  status TEXT NOT NULL,
  error_log TEXT
);

CREATE TABLE installations (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  device_id INTEGER NOT NULL REFERENCES devices(id),
  product_id INTEGER NOT NULL REFERENCES products(id),
  version TEXT,
  first_detected_at DATETIME NOT NULL,
  last_detected_at DATETIME NOT NULL,
  last_used_at DATETIME,
  uninstalled_at DATETIME,
  UNIQUE(device_id, product_id, version)
);

CREATE TABLE raw_installations (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  import_log_id INTEGER NOT NULL REFERENCES import_logs(id),
  device_asset_code TEXT NOT NULL,
  raw_product_name TEXT NOT NULL,
  raw_vendor_name TEXT,
  version TEXT,
  detected_at DATETIME,
  last_used_at DATETIME,
  resolved_device_id INTEGER REFERENCES devices(id),
  resolved_product_id INTEGER REFERENCES products(id),
  status TEXT NOT NULL DEFAULT 'pending',
  resolved_at DATETIME,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
