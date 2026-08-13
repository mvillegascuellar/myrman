package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

type BackupType string

const (
	BackupFull        BackupType = "FULL"
	BackupIncremental BackupType = "INCREMENTAL"
)

type GFSTag string

const (
	GFSNone    GFSTag = "NONE"
	GFSDaily   GFSTag = "DAILY"
	GFSMonthly GFSTag = "MONTHLY"
	GFSYearly  GFSTag = "YEARLY"
)

type StorageLocation string

const (
	StorageLocal   StorageLocation = "LOCAL"
	StorageOffsite StorageLocation = "OFFSITE"
	StorageBoth    StorageLocation = "BOTH"
)

type BackupStatus string

const (
	StatusRunning   BackupStatus = "RUNNING"
	StatusCompleted BackupStatus = "COMPLETED"
	StatusFailed    BackupStatus = "FAILED"
	StatusPrepared  BackupStatus = "PREPARED"
	StatusDeleted   BackupStatus = "DELETED"
)

type PhysicalBackup struct {
	ID               string
	ParentID         sql.NullString
	BackupType       BackupType
	BackupTool       string
	LSNFrom          int64
	LSNTo            int64
	BinlogFile       sql.NullString
	BinlogPos        sql.NullInt64
	GTIDExecuted     sql.NullString
	ServerUUID       sql.NullString
	StartTime        int64
	EndTime          sql.NullInt64
	GFSTag           GFSTag
	StorageLocation  StorageLocation
	LocalPath        sql.NullString
	CloudURL         sql.NullString
	Status           BackupStatus
	ArtifactName     sql.NullString
	ErrorMessage     sql.NullString
	CreatedAt        int64
}

type BinlogArchive struct {
	ID               string
	Filename         string
	SequenceNumber   int64
	StartTime        sql.NullInt64
	EndTime          sql.NullInt64
	StartGTID        sql.NullString
	EndGTID          sql.NullString
	FileSize         sql.NullInt64
	StorageLocation  StorageLocation
	LocalPath        sql.NullString
	CloudURL         sql.NullString
	Status           string
	CreatedAt        int64
}

type PhysicalRepo struct{ db *DB }
type BinlogRepo struct{ db *DB }

func NewPhysicalRepo(db *DB) *PhysicalRepo { return &PhysicalRepo{db: db} }
func NewBinlogRepo(db *DB) *BinlogRepo     { return &BinlogRepo{db: db} }

func (r *PhysicalRepo) Insert(ctx context.Context, b *PhysicalBackup) error {
	_, err := r.db.SQL.ExecContext(ctx, `
INSERT INTO physical_backups (
  id, parent_id, backup_type, backup_tool, lsn_from, lsn_to,
  binlog_file, binlog_pos, gtid_executed, server_uuid,
  start_time, end_time, gfs_tag, storage_location, local_path, cloud_url,
  status, artifact_name, error_message, created_at
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		b.ID, nullStr(b.ParentID), b.BackupType, b.BackupTool, b.LSNFrom, b.LSNTo,
		nullStr(b.BinlogFile), nullInt(b.BinlogPos), nullStr(b.GTIDExecuted), nullStr(b.ServerUUID),
		b.StartTime, nullInt(b.EndTime), b.GFSTag, b.StorageLocation, nullStr(b.LocalPath), nullStr(b.CloudURL),
		b.Status, nullStr(b.ArtifactName), nullStr(b.ErrorMessage), b.CreatedAt,
	)
	return err
}

func (r *PhysicalRepo) Update(ctx context.Context, b *PhysicalBackup) error {
	_, err := r.db.SQL.ExecContext(ctx, `
UPDATE physical_backups SET
  parent_id=?, backup_type=?, backup_tool=?, lsn_from=?, lsn_to=?,
  binlog_file=?, binlog_pos=?, gtid_executed=?, server_uuid=?,
  start_time=?, end_time=?, gfs_tag=?, storage_location=?, local_path=?, cloud_url=?,
  status=?, artifact_name=?, error_message=?
WHERE id=?`,
		nullStr(b.ParentID), b.BackupType, b.BackupTool, b.LSNFrom, b.LSNTo,
		nullStr(b.BinlogFile), nullInt(b.BinlogPos), nullStr(b.GTIDExecuted), nullStr(b.ServerUUID),
		b.StartTime, nullInt(b.EndTime), b.GFSTag, b.StorageLocation, nullStr(b.LocalPath), nullStr(b.CloudURL),
		b.Status, nullStr(b.ArtifactName), nullStr(b.ErrorMessage), b.ID,
	)
	return err
}

func (r *PhysicalRepo) Get(ctx context.Context, id string) (*PhysicalBackup, error) {
	row := r.db.SQL.QueryRowContext(ctx, physicalSelect+` WHERE id=?`, id)
	return scanPhysical(row)
}

func (r *PhysicalRepo) List(ctx context.Context, status string, limit int) ([]PhysicalBackup, error) {
	q := physicalSelect + ` WHERE 1=1`
	args := []any{}
	if status != "" {
		q += ` AND status=?`
		args = append(args, status)
	}
	q += ` ORDER BY start_time DESC`
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := r.db.SQL.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PhysicalBackup
	for rows.Next() {
		b, err := scanPhysicalRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	return out, rows.Err()
}

func (r *PhysicalRepo) ListCompleted(ctx context.Context) ([]PhysicalBackup, error) {
	return r.List(ctx, string(StatusCompleted), 0)
}

func (r *PhysicalRepo) LatestChainHead(ctx context.Context) (*PhysicalBackup, error) {
	row := r.db.SQL.QueryRowContext(ctx, physicalSelect+`
 WHERE status=? ORDER BY lsn_to DESC LIMIT 1`, StatusCompleted)
	b, err := scanPhysical(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return b, err
}

func (r *PhysicalRepo) Children(ctx context.Context, parentID string) ([]PhysicalBackup, error) {
	rows, err := r.db.SQL.QueryContext(ctx, physicalSelect+` WHERE parent_id=? AND status!=?`, parentID, StatusDeleted)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PhysicalBackup
	for rows.Next() {
		b, err := scanPhysicalRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	return out, rows.Err()
}

func (r *PhysicalRepo) SetGFSTag(ctx context.Context, id string, tag GFSTag) error {
	_, err := r.db.SQL.ExecContext(ctx, `UPDATE physical_backups SET gfs_tag=? WHERE id=?`, tag, id)
	return err
}

func (r *PhysicalRepo) MarkDeleted(ctx context.Context, id string) error {
	_, err := r.db.SQL.ExecContext(ctx, `
UPDATE physical_backups SET status=?, local_path=NULL, cloud_url=NULL, storage_location=? WHERE id=?`,
		StatusDeleted, StorageLocal, id)
	return err
}

func (r *PhysicalRepo) UpdateStorage(ctx context.Context, id string, loc StorageLocation, localPath, cloudURL *string) error {
	var lp, cu any
	if localPath != nil {
		lp = *localPath
	}
	if cloudURL != nil {
		cu = *cloudURL
	}
	_, err := r.db.SQL.ExecContext(ctx, `
UPDATE physical_backups SET storage_location=?, local_path=COALESCE(?, local_path), cloud_url=COALESCE(?, cloud_url) WHERE id=?`,
		loc, lp, cu, id)
	return err
}

const physicalSelect = `SELECT id, parent_id, backup_type, backup_tool, lsn_from, lsn_to,
  binlog_file, binlog_pos, gtid_executed, server_uuid, start_time, end_time, gfs_tag,
  storage_location, local_path, cloud_url, status, artifact_name, error_message, created_at
FROM physical_backups`

type scannable interface {
	Scan(dest ...any) error
}

func scanPhysical(row scannable) (*PhysicalBackup, error) {
	var b PhysicalBackup
	err := row.Scan(
		&b.ID, &b.ParentID, &b.BackupType, &b.BackupTool, &b.LSNFrom, &b.LSNTo,
		&b.BinlogFile, &b.BinlogPos, &b.GTIDExecuted, &b.ServerUUID, &b.StartTime, &b.EndTime, &b.GFSTag,
		&b.StorageLocation, &b.LocalPath, &b.CloudURL, &b.Status, &b.ArtifactName, &b.ErrorMessage, &b.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func scanPhysicalRows(rows *sql.Rows) (*PhysicalBackup, error) {
	return scanPhysical(rows)
}

func (r *BinlogRepo) Insert(ctx context.Context, a *BinlogArchive) error {
	_, err := r.db.SQL.ExecContext(ctx, `
INSERT INTO binlog_archives (
  id, filename, sequence_number, start_time, end_time, start_gtid, end_gtid,
  file_size, storage_location, local_path, cloud_url, status, created_at
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		a.ID, a.Filename, a.SequenceNumber, nullInt(a.StartTime), nullInt(a.EndTime),
		nullStr(a.StartGTID), nullStr(a.EndGTID), nullInt(a.FileSize),
		a.StorageLocation, nullStr(a.LocalPath), nullStr(a.CloudURL), a.Status, a.CreatedAt,
	)
	return err
}

func (r *BinlogRepo) GetByFilename(ctx context.Context, name string) (*BinlogArchive, error) {
	row := r.db.SQL.QueryRowContext(ctx, binlogSelect+` WHERE filename=?`, name)
	return scanBinlog(row)
}

func (r *BinlogRepo) List(ctx context.Context, limit int) ([]BinlogArchive, error) {
	q := binlogSelect + ` WHERE status!='DELETED' ORDER BY sequence_number ASC`
	args := []any{}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := r.db.SQL.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BinlogArchive
	for rows.Next() {
		a, err := scanBinlogRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

func (r *BinlogRepo) ListInRange(ctx context.Context, fromSeq int64, fromTime, toTime int64) ([]BinlogArchive, error) {
	rows, err := r.db.SQL.QueryContext(ctx, binlogSelect+`
 WHERE status!='DELETED'
   AND sequence_number >= ?
   AND (end_time IS NULL OR end_time >= ?)
   AND (start_time IS NULL OR start_time <= ?)
 ORDER BY sequence_number ASC`, fromSeq, fromTime, toTime)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BinlogArchive
	for rows.Next() {
		a, err := scanBinlogRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

func (r *BinlogRepo) MarkDeleted(ctx context.Context, id string) error {
	_, err := r.db.SQL.ExecContext(ctx, `
UPDATE binlog_archives SET status='DELETED', local_path=NULL, cloud_url=NULL WHERE id=?`, id)
	return err
}

func (r *BinlogRepo) UpdateStorage(ctx context.Context, id string, loc StorageLocation, localPath, cloudURL *string) error {
	var lp, cu any
	if localPath != nil {
		lp = *localPath
	}
	if cloudURL != nil {
		cu = *cloudURL
	}
	_, err := r.db.SQL.ExecContext(ctx, `
UPDATE binlog_archives SET storage_location=?, local_path=COALESCE(?, local_path), cloud_url=COALESCE(?, cloud_url) WHERE id=?`,
		loc, lp, cu, id)
	return err
}

const binlogSelect = `SELECT id, filename, sequence_number, start_time, end_time, start_gtid, end_gtid,
  file_size, storage_location, local_path, cloud_url, status, created_at FROM binlog_archives`

func scanBinlog(row scannable) (*BinlogArchive, error) {
	var a BinlogArchive
	err := row.Scan(
		&a.ID, &a.Filename, &a.SequenceNumber, &a.StartTime, &a.EndTime, &a.StartGTID, &a.EndGTID,
		&a.FileSize, &a.StorageLocation, &a.LocalPath, &a.CloudURL, &a.Status, &a.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func scanBinlogRows(rows *sql.Rows) (*BinlogArchive, error) {
	return scanBinlog(rows)
}

func InsertRetentionRun(ctx context.Context, db *DB, dryRun bool, summary string, started, finished int64) error {
	_, err := db.SQL.ExecContext(ctx, `
INSERT INTO retention_runs(started_at, finished_at, dry_run, summary_json) VALUES (?,?,?,?)`,
		started, finished, boolToInt(dryRun), summary)
	return err
}

func nullStr(ns sql.NullString) any {
	if ns.Valid {
		return ns.String
	}
	return nil
}

func nullInt(ni sql.NullInt64) any {
	if ni.Valid {
		return ni.Int64
	}
	return nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func NullString(s string) sql.NullString {
	if strings.TrimSpace(s) == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func NullInt64(v int64, ok bool) sql.NullInt64 {
	return sql.NullInt64{Int64: v, Valid: ok}
}

func PtrString(s string) *string { return &s }

func ValidateLSNContinuity(parentTo, childFrom int64) error {
	if parentTo != childFrom {
		return fmt.Errorf("LSN continuity broken: parent to_lsn=%d != incremental from_lsn=%d", parentTo, childFrom)
	}
	return nil
}
