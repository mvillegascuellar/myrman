-- 001_init.sql
CREATE TABLE IF NOT EXISTS schema_migrations (
  version INTEGER PRIMARY KEY
);

CREATE TABLE IF NOT EXISTS physical_backups (
  id               TEXT PRIMARY KEY,
  parent_id        TEXT REFERENCES physical_backups(id),
  backup_type      TEXT NOT NULL CHECK (backup_type IN ('FULL','INCREMENTAL')),
  backup_tool      TEXT NOT NULL,
  lsn_from         INTEGER NOT NULL,
  lsn_to           INTEGER NOT NULL,
  binlog_file      TEXT,
  binlog_pos       INTEGER,
  gtid_executed    TEXT,
  server_uuid      TEXT,
  start_time       INTEGER NOT NULL,
  end_time         INTEGER,
  gfs_tag          TEXT NOT NULL DEFAULT 'NONE'
                   CHECK (gfs_tag IN ('DAILY','MONTHLY','YEARLY','NONE')),
  storage_location TEXT NOT NULL
                   CHECK (storage_location IN ('LOCAL','OFFSITE','BOTH')),
  local_path       TEXT,
  cloud_url        TEXT,
  status           TEXT NOT NULL
                   CHECK (status IN ('RUNNING','COMPLETED','FAILED','PREPARED','DELETED')),
  artifact_name    TEXT,
  error_message    TEXT,
  created_at       INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS binlog_archives (
  id               TEXT PRIMARY KEY,
  filename         TEXT NOT NULL UNIQUE,
  sequence_number  INTEGER NOT NULL,
  start_time       INTEGER,
  end_time         INTEGER,
  start_gtid       TEXT,
  end_gtid         TEXT,
  file_size        INTEGER,
  storage_location TEXT NOT NULL
                   CHECK (storage_location IN ('LOCAL','OFFSITE','BOTH')),
  local_path       TEXT,
  cloud_url        TEXT,
  status           TEXT NOT NULL DEFAULT 'COMPLETED',
  created_at       INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS retention_runs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  started_at INTEGER NOT NULL,
  finished_at INTEGER,
  dry_run INTEGER NOT NULL DEFAULT 0,
  summary_json TEXT
);

CREATE INDEX IF NOT EXISTS idx_pb_lsn_to ON physical_backups(lsn_to);
CREATE INDEX IF NOT EXISTS idx_pb_lsn_from ON physical_backups(lsn_from);
CREATE INDEX IF NOT EXISTS idx_pb_parent ON physical_backups(parent_id);
CREATE INDEX IF NOT EXISTS idx_pb_start_time ON physical_backups(start_time);
CREATE INDEX IF NOT EXISTS idx_pb_gfs ON physical_backups(gfs_tag, start_time);
CREATE INDEX IF NOT EXISTS idx_pb_status ON physical_backups(status);
CREATE INDEX IF NOT EXISTS idx_ba_seq ON binlog_archives(sequence_number);
CREATE INDEX IF NOT EXISTS idx_ba_times ON binlog_archives(start_time, end_time);
CREATE INDEX IF NOT EXISTS idx_ba_start_gtid ON binlog_archives(start_gtid);
CREATE INDEX IF NOT EXISTS idx_ba_end_gtid ON binlog_archives(end_gtid);
