# MyRMAN

Production-oriented CLI for MySQL, Percona Server, and MariaDB physical backup, continuous binlog archiving, hybrid local/cloud storage, GFS retention with LSN dependency locks, and point-in-time recovery.

## Requirements

On `PATH`:

- `xtrabackup` **or** `mariabackup`
- `xbstream`
- `mysqlbinlog`, `mysql`
- `zstd` and/or `gzip` (if compression enabled)

Go **1.22+** to build.

## Build

```bash
make build
./myrman version
```

## Configure

Configuration is stored in the SQLite catalog (`settings` table). Import a YAML file once, then run commands without `--config`.

```bash
# 1) Edit configs/myrman.example.yaml (or your own myrman.yaml)
# 2) Import into the catalog (creates/updates settings rows)
sudo ./myrman --catalog /var/lib/myrman/catalog.db config import --config myrman.yaml

# 3) Inspect / tweak without re-importing
./myrman config show
./myrman config set mysql.host=127.0.0.1
./myrman config set compression zstd
./myrman config get defaults_file
./myrman config keys

# Optional: password via env (overrides stored mysql.password).
# Important: plain `sudo` strips your environment. Prefer one of:
#   sudo MYRMAN_MYSQL_PASSWORD='...' ./myrman backup full
#   sudo -E ./myrman backup full
#   sudo ./myrman config set mysql.password='...'   # stored in catalog
export MYRMAN_MYSQL_PASSWORD='...'

# Catalog path: --catalog, else MYRMAN_CATALOG, else /var/lib/myrman/catalog.db
./myrman backup full
```

## Commands

| Command | Description |
|---------|-------------|
| `myrman config import --config FILE` | Load YAML into SQLite settings |
| `myrman config show` | Print active config from catalog |
| `myrman config get\|set\|keys` | Read/update individual settings |
| `myrman backup full` | Full physical backup (xbstream pipe) |
| `myrman backup incremental --parent latest` | Incremental with LSN continuity check |
| `myrman binlog start\|stop\|status` | Continuous `mysqlbinlog --stop-never` |
| `myrman catalog list [--type physical\|binlog]` | Inventory |
| `myrman catalog show <uuid>` | Backup detail |
| `myrman retain tag` | Assign DAILY/MONTHLY/YEARLY |
| `myrman retain run [--dry-run]` | Tag + LSN-safe prune |
| `myrman recover --target-time="YYYY-MM-DD HH:MM:SS"` | PITR plan (add `--apply` to execute) |
| `myrman recover --target-gtid="UUID:SEQ"` | GTID-targeted plan |

## Manual E2E checklist

1. Start MySQL/Percona/MariaDB with binlogs + GTID.
2. `myrman backup full` then make changes; `myrman backup incremental`.
3. `myrman binlog start` (set `MYRMAN_BINLOG_START` if needed).
4. `myrman catalog list` / `retain run --dry-run`.
5. `myrman recover --target-time="..." ` review `recover.sh`, then `--apply` in a lab.

## Layout

```
cmd/myrman/          CLI entrypoint (cobra)
internal/catalog/    SQLite schema (migrations/) + repositories
internal/parser/     xtrabackup_checkpoints / xtrabackup_info / binlog bounds
internal/storage/    Local + S3 / GCS / Azure
internal/backup/     FULL / INCREMENTAL runner + LSN continuity
internal/binlog/     mysqlbinlog --stop-never daemon
internal/retention/  GFS tagging + LSN-safe prune
internal/recover/    PITR chain select, prepare, binlog plan
configs/             Example YAML
```

