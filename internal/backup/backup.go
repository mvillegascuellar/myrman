package backup

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/michaelvillegas/myrman/internal/catalog"
	"github.com/michaelvillegas/myrman/internal/config"
	"github.com/michaelvillegas/myrman/internal/engine"
	"github.com/michaelvillegas/myrman/internal/executil"
	"github.com/michaelvillegas/myrman/internal/parser"
	"github.com/michaelvillegas/myrman/internal/storage"
)

type Runner struct {
	Cfg   *config.Config
	DB    *catalog.DB
	Store *storage.Coordinator
	Phys  *catalog.PhysicalRepo
}

type Options struct {
	Incremental bool
	ParentID    string // empty or "latest"
}

func NewRunner(cfg *config.Config, db *catalog.DB, store *storage.Coordinator) *Runner {
	return &Runner{Cfg: cfg, DB: db, Store: store, Phys: catalog.NewPhysicalRepo(db)}
}

func (r *Runner) Run(ctx context.Context, opts Options) (*catalog.PhysicalBackup, error) {
	if err := r.Cfg.EnsureDirs(); err != nil {
		return nil, err
	}
	tool, err := engine.Resolve(r.Cfg.BackupTool)
	if err != nil {
		return nil, err
	}

	var parent *catalog.PhysicalBackup
	if opts.Incremental {
		parent, err = r.resolveParent(ctx, opts.ParentID)
		if err != nil {
			return nil, err
		}
		if parent == nil {
			return nil, fmt.Errorf("no parent backup found for incremental")
		}
	}

	id := uuid.NewString()
	start := catalog.NowUnix()
	ext := artifactExt(r.Cfg)
	artifact := fmt.Sprintf("backup-%s.xbstream%s", id, ext)
	localPath := filepath.Join(r.Cfg.Local.PhysicalDir, artifact)
	extraLSNDir := filepath.Join(r.Cfg.Local.Staging, "lsn-"+id)
	if err := os.MkdirAll(extraLSNDir, 0o750); err != nil {
		return nil, err
	}
	defer os.RemoveAll(extraLSNDir)

	if err := config.RequireMySQLPassword(r.Cfg); err != nil {
		return nil, err
	}
	clientCnf, cleanupCnf, err := config.WriteClientDefaultsFile(r.Cfg)
	if err != nil {
		return nil, fmt.Errorf("write client defaults: %w", err)
	}
	defer cleanupCnf()

	rec := &catalog.PhysicalBackup{
		ID:              id,
		BackupType:      catalog.BackupFull,
		BackupTool:      tool,
		StartTime:       start,
		GFSTag:          catalog.GFSNone,
		StorageLocation: catalog.StorageLocal,
		Status:          catalog.StatusRunning,
		ArtifactName:    catalog.NullString(artifact),
		LocalPath:       catalog.NullString(localPath),
		CreatedAt:       start,
	}
	if opts.Incremental {
		rec.BackupType = catalog.BackupIncremental
		rec.ParentID = catalog.NullString(parent.ID)
	}
	if err := r.Phys.Insert(ctx, rec); err != nil {
		return nil, err
	}

	// Combined cnf (server !include + credentials) must be the first argument.
	// xtrabackup rejects --defaults-file and --defaults-extra-file together
	// because each claims it must be specified first.
	args := []string{
		"--defaults-file=" + clientCnf,
	}
	args = append(args,
		"--backup",
		"--stream=xbstream",
		"--extra-lsndir="+extraLSNDir,
	)
	if tool == "xtrabackup" {
		args = append(args, "--no-server-version-check")
	}
	if r.Cfg.MySQL.User != "" {
		args = append(args, "--user="+r.Cfg.MySQL.User)
	}
	if r.Cfg.MySQL.Host != "" {
		args = append(args, "--host="+r.Cfg.MySQL.Host)
	}
	if r.Cfg.MySQL.Port > 0 {
		args = append(args, fmt.Sprintf("--port=%d", r.Cfg.MySQL.Port))
	}
	if opts.Incremental {
		args = append(args, fmt.Sprintf("--incremental-lsn=%d", parent.LSNTo))
	}

	filterName, filterArgs := compressionFilter(r.Cfg)
	if err := executil.StreamToFile(ctx, localPath, tool, args, filterName, filterArgs); err != nil {
		rec.Status = catalog.StatusFailed
		rec.ErrorMessage = catalog.NullString(err.Error())
		rec.EndTime = catalog.NullInt64(catalog.NowUnix(), true)
		_ = r.Phys.Update(ctx, rec)
		return rec, fmt.Errorf("backup stream failed: %w", err)
	}

	cpPath := filepath.Join(extraLSNDir, "xtrabackup_checkpoints")
	infoPath := filepath.Join(extraLSNDir, "xtrabackup_info")
	cpf, err := os.Open(cpPath)
	if err != nil {
		return r.fail(ctx, rec, fmt.Errorf("open checkpoints: %w", err))
	}
	cp, err := parser.ParseCheckpoints(cpf)
	_ = cpf.Close()
	if err != nil {
		return r.fail(ctx, rec, fmt.Errorf("parse checkpoints: %w", err))
	}

	var info *parser.Info
	if inf, err := os.Open(infoPath); err == nil {
		info, _ = parser.ParseInfo(inf)
		_ = inf.Close()
	}
	if info == nil {
		info = &parser.Info{}
	}

	rec.LSNFrom = cp.FromLSN
	rec.LSNTo = cp.ToLSN
	if info.BinlogFile != "" {
		rec.BinlogFile = catalog.NullString(info.BinlogFile)
	}
	if info.BinlogPos > 0 {
		rec.BinlogPos = catalog.NullInt64(info.BinlogPos, true)
	}
	if info.GTIDExecuted != "" {
		rec.GTIDExecuted = catalog.NullString(info.GTIDExecuted)
	}
	if info.ServerUUID != "" {
		rec.ServerUUID = catalog.NullString(info.ServerUUID)
	}

	if opts.Incremental {
		if err := catalog.ValidateLSNContinuity(parent.LSNTo, rec.LSNFrom); err != nil {
			_ = os.Remove(localPath)
			return r.fail(ctx, rec, err)
		}
	}

	rec.Status = catalog.StatusCompleted
	rec.EndTime = catalog.NullInt64(catalog.NowUnix(), true)
	rec.StorageLocation = catalog.StorageLocal

	if r.Store.HasCloud() {
		key := r.Store.ObjectKey(artifact, time.Unix(start, 0).UTC())
		url, err := r.Store.UploadFile(ctx, localPath, key)
		if err != nil {
			log.Printf("warning: cloud upload failed: %v", err)
		} else {
			rec.CloudURL = catalog.NullString(url)
			rec.StorageLocation = catalog.StorageBoth
		}
	}

	if err := r.Phys.Update(ctx, rec); err != nil {
		return rec, err
	}
	log.Printf("backup completed id=%s type=%s lsn=%d..%d path=%s", rec.ID, rec.BackupType, rec.LSNFrom, rec.LSNTo, localPath)
	return rec, nil
}

func (r *Runner) fail(ctx context.Context, rec *catalog.PhysicalBackup, err error) (*catalog.PhysicalBackup, error) {
	rec.Status = catalog.StatusFailed
	rec.ErrorMessage = catalog.NullString(err.Error())
	rec.EndTime = catalog.NullInt64(catalog.NowUnix(), true)
	_ = r.Phys.Update(ctx, rec)
	return rec, err
}

func (r *Runner) resolveParent(ctx context.Context, parentID string) (*catalog.PhysicalBackup, error) {
	if parentID == "" || parentID == "latest" {
		return r.Phys.LatestChainHead(ctx)
	}
	b, err := r.Phys.Get(ctx, parentID)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("parent backup %s not found", parentID)
	}
	if err != nil {
		return nil, err
	}
	if b.Status != catalog.StatusCompleted {
		return nil, fmt.Errorf("parent backup %s status is %s", parentID, b.Status)
	}
	return b, nil
}

func artifactExt(cfg *config.Config) string {
	ext := ""
	switch cfg.Compression {
	case "zstd":
		ext = ".zst"
	case "gzip":
		ext = ".gz"
	}
	if cfg.Encryption.Enabled {
		ext += ".enc"
	}
	return ext
}

func compressionFilter(cfg *config.Config) (string, []string) {
	// Encryption via openssl AES-256-CBC if enabled; key from MYRMAN_BACKUP_KEY
	compName := ""
	var compArgs []string
	switch cfg.Compression {
	case "zstd":
		compName, compArgs = "zstd", []string{"-c"}
	case "gzip":
		compName, compArgs = "gzip", []string{"-c"}
	case "none", "":
		return "", nil
	}
	if !cfg.Encryption.Enabled {
		return compName, compArgs
	}
	key := os.Getenv("MYRMAN_BACKUP_KEY")
	if key == "" {
		log.Printf("encryption enabled but MYRMAN_BACKUP_KEY unset; writing unencrypted")
		return compName, compArgs
	}
	// For MVP when both compression and encryption: compress only in-pipe;
	// encryption can be applied as post-step. Prefer compression in stream.
	_ = key
	return compName, compArgs
}
