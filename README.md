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

Copy [`configs/myrman.example.yaml`](configs/myrman.example.yaml) and set paths, MySQL credentials, and optional cloud provider.

```bash
export MYRMAN_MYSQL_PASSWORD='...'
./myrman --config /path/to/myrman.yaml catalog list
```

## Commands

| Command | Description |
|---------|-------------|
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

