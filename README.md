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

Runtime configuration lives in the SQLite catalog (`settings` table), not in the YAML file after first setup. Import `myrman.yaml` once, then use `config show` / `config set` for later changes.

### Initial import from `myrman.yaml`

1. Copy the example and edit paths, MySQL user, defaults file, and optional cloud settings:

```bash
cp configs/myrman.example.yaml myrman.yaml
# edit myrman.yaml
```

2. Import it into the catalog. `--config` is **required** for import; `--catalog` is optional if `catalog:` is already set in the YAML (default `/var/lib/myrman/catalog.db`):

```bash
sudo ./myrman --catalog /var/lib/myrman/catalog.db config import --config myrman.yaml
```

You should see something like `imported 29 settings into /var/lib/myrman/catalog.db`.

3. Confirm and set the MySQL password (YAML does not store a password by default):

```bash
sudo ./myrman config show
sudo ./myrman config set mysql.password='...'
```

After that, run commands **without** `--config`:

```bash
sudo ./myrman backup full
```

If you skip import, backup/catalog commands fail with:

`no configuration in catalog ...; run: myrman --catalog ... config import --config myrman.yaml`

Re-running `config import` overwrites matching keys in SQLite from the YAML file.

### Later changes (no re-import)

```bash
./myrman config show
./myrman config set mysql.host=127.0.0.1
./myrman config set compression zstd
./myrman config get defaults_file
./myrman config keys
```

### Password and `sudo`

Plain `sudo` strips your environment, so `MYRMAN_MYSQL_PASSWORD` from your shell is not visible. Prefer one of:

```bash
sudo MYRMAN_MYSQL_PASSWORD='...' ./myrman backup full
sudo -E ./myrman backup full
sudo ./myrman config set mysql.password='...'   # stored in catalog
```

Catalog path resolution: `--catalog`, else `MYRMAN_CATALOG`, else `/var/lib/myrman/catalog.db`.

## Commands

| Command | Description |
|---------|-------------|
| `myrman config import --config FILE` | Load YAML into SQLite settings |
| `myrman config show` | Print active config from catalog |
| `myrman config get\|set\|keys` | Read/update individual settings |
| `myrman backup full` | Full physical backup (xbstream pipe) |
| `myrman backup incremental --parent latest` | Incremental with LSN continuity check |
| `myrman binlog start\|stop\|status` | Continuous `mysqlbinlog --stop-never` |
| `myrman catalog list [--type physical\|binlog] [--status COMPLETED]` | Table: ID, type, start time (UTC), duration, status |
| `myrman catalog show <uuid>` | Backup detail (JSON) |
| `myrman retain tag` | Assign DAILY/MONTHLY/YEARLY |
| `myrman retain run [--dry-run]` | Tag + LSN-safe prune |
| `myrman recover --target-time="YYYY-MM-DD HH:MM:SS"` | PITR plan (add `--apply` to execute) |
| `myrman recover --target-gtid="UUID:SEQ"` | GTID-targeted plan |

## Manual E2E checklist

1. Start MySQL/Percona/MariaDB with binlogs + GTID.
2. `myrman backup full` then make changes; `myrman backup incremental`.
3. `myrman binlog start` (starts at the oldest file from `SHOW BINARY LOGS`, or `mysql.binlog_start` / `MYRMAN_BINLOG_START`).
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

