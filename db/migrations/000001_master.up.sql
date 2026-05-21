CREATE TABLE departments (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  code TEXT NOT NULL UNIQUE,
  name TEXT NOT NULL,
  parent_id INTEGER REFERENCES departments(id),
  successor_department_id INTEGER REFERENCES departments(id),
  valid_from DATE,
  valid_to DATE,
  source TEXT NOT NULL DEFAULT 'manual',
  source_ou TEXT,
  last_synced_at DATETIME,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE vendors (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL UNIQUE,
  url TEXT,
  note TEXT,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE products (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  vendor_id INTEGER NOT NULL REFERENCES vendors(id),
  canonical_name TEXT NOT NULL,
  edition TEXT,
  software_type TEXT NOT NULL DEFAULT 'installed',
  license_required BOOLEAN,
  default_approval_status TEXT NOT NULL DEFAULT 'unknown',
  canonical_download_url TEXT,
  service_admin_url TEXT,
  license_terms_url TEXT,
  note TEXT,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE(vendor_id, canonical_name, edition)
);

CREATE TABLE product_aliases (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  product_id INTEGER NOT NULL REFERENCES products(id),
  alias_name TEXT NOT NULL UNIQUE,
  source TEXT NOT NULL DEFAULT 'manual',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE product_version_advisories (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  product_id INTEGER NOT NULL REFERENCES products(id),
  advisory_code TEXT,
  severity TEXT,
  affected_version_range TEXT NOT NULL,
  fixed_version TEXT,
  summary TEXT,
  detail_url TEXT,
  published_at DATETIME,
  notified_at DATETIME,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE users (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  employee_code TEXT NOT NULL UNIQUE,
  username TEXT,
  name TEXT NOT NULL,
  email TEXT,
  department_id INTEGER REFERENCES departments(id),
  deactivated_at DATETIME,
  source TEXT NOT NULL DEFAULT 'manual',
  source_dn TEXT,
  ad_modified_at DATETIME,
  last_synced_at DATETIME,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE devices (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  asset_code TEXT NOT NULL UNIQUE,
  hostname TEXT,
  primary_user_id INTEGER REFERENCES users(id),
  department_id INTEGER REFERENCES departments(id),
  retired_at DATETIME,
  last_seen_at DATETIME,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
