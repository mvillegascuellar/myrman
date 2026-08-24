package binlog

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/michaelvillegas/myrman/internal/catalog"
	"github.com/michaelvillegas/myrman/internal/config"
	"github.com/michaelvillegas/myrman/internal/parser"
	"github.com/michaelvillegas/myrman/internal/storage"
)

const pidFileName = "binlog.pid"
const daemonEnv = "MYRMAN_BINLOG_DAEMON"

type Service struct {
	Cfg   *config.Config
	DB    *catalog.DB
	Store *storage.Coordinator
	Repo  *catalog.BinlogRepo
}

func NewService(cfg *config.Config, db *catalog.DB, store *storage.Coordinator) *Service {
	return &Service{Cfg: cfg, DB: db, Store: store, Repo: catalog.NewBinlogRepo(db)}
}

func (s *Service) pidPath() string {
	return filepath.Join(s.Cfg.Local.Root, "run", pidFileName)
}

func (s *Service) Start(ctx context.Context, foreground bool) error {
	if err := s.Cfg.EnsureDirs(); err != nil {
		return err
	}
	if err := executilLook("mysqlbinlog"); err != nil {
		return err
	}
	if running, pid := s.isRunning(); running {
		return fmt.Errorf("binlog streamer already running (pid %d)", pid)
	}

	if err := config.RequireMySQLPassword(s.Cfg); err != nil {
		return err
	}
	if os.Getenv(daemonEnv) != "1" && !foreground {
		return s.spawnDaemon()
	}
	clientCnf, cleanupCnf, err := config.WriteClientDefaultsFile(s.Cfg)
	if err != nil {
		return fmt.Errorf("write client defaults: %w", err)
	}
	defer cleanupCnf()

	if err := executilLook("mysql"); err != nil {
		return err
	}
	serverLogs, err := queryBinaryLogs(clientCnf)
	if err != nil {
		return err
	}

	requested := os.Getenv("MYRMAN_BINLOG_START")
	if requested == "" {
		requested = s.Cfg.MySQL.BinlogStart
	}
	lastCataloged := ""
	if latest, err := s.Repo.Latest(ctx); err != nil {
		return fmt.Errorf("catalog latest binlog: %w", err)
	} else if latest != nil {
		lastCataloged = latest.Filename
	}
	backupBinlog := latestPhysicalBinlog(ctx, s.DB)
	startLog, err := ChooseStartLog(StartHint{
		Requested:     requested,
		LastCataloged: lastCataloged,
		BackupBinlog:  backupBinlog,
	}, serverLogs)
	if err != nil {
		return err
	}
	candidates := logsFrom(serverLogs, startLog)
	log.Printf("binlog stream start file=%s candidates=%d (last cataloged=%q backup binlog=%q)", startLog, len(candidates), lastCataloged, backupBinlog)

	minSeq, _ := parser.SequenceFromFilename(startLog)
	watchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go s.watchAndCatalog(watchCtx, minSeq)

	var lastErr error
	for i, file := range candidates {
		lastErr = s.runDump(ctx, cancel, clientCnf, file)
		if lastErr == nil {
			return nil
		}
		if isAnonymousGTIDDumpError(lastErr.Error()) && i+1 < len(candidates) {
			log.Printf("skipping %s (%v); retrying from next binary log (likely pre-GTID anonymous events)", file, lastErr)
			continue
		}
		return lastErr
	}
	return lastErr
}

func (s *Service) spawnDaemon() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	logPath := filepath.Join(s.Cfg.Local.Root, "run", "binlog.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o750); err != nil {
		return err
	}
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	defer f.Close()

	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Env = append(os.Environ(), daemonEnv+"=1")
	cmd.Stdin = nil
	cmd.Stdout = f
	cmd.Stderr = f
	cmd.Dir = s.Cfg.Local.Root
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	log.Printf("binlog streamer daemon started pid=%d log=%s (use 'myrman binlog stop' to stop)", cmd.Process.Pid, logPath)
	return nil
}

func latestPhysicalBinlog(ctx context.Context, db *catalog.DB) string {
	backs, err := catalog.NewPhysicalRepo(db).ListCompleted(ctx)
	if err != nil {
		return ""
	}
	var best string
	var bestTime int64
	for _, b := range backs {
		if !b.BinlogFile.Valid || b.BinlogFile.String == "" {
			continue
		}
		t := b.StartTime
		if b.EndTime.Valid {
			t = b.EndTime.Int64
		}
		if t >= bestTime {
			bestTime = t
			best = b.BinlogFile.String
		}
	}
	return best
}

func (s *Service) runDump(ctx context.Context, cancel context.CancelFunc, clientCnf, startLog string) error {
	args := []string{
		"--defaults-file=" + clientCnf,
		"--read-from-remote-server",
		"--raw",
		"--stop-never",
		"--to-last-log",
		"--host=" + s.Cfg.MySQL.Host,
		fmt.Sprintf("--port=%d", s.Cfg.MySQL.Port),
		"--user=" + s.Cfg.MySQL.User,
		"--result-file=" + ensureTrailingSlash(s.Cfg.Local.BinlogDir),
	}
	if s.Cfg.MySQL.BinlogServerID > 0 && mysqlbinlogSupports("connection-server-id") {
		args = append(args, fmt.Sprintf("--connection-server-id=%d", s.Cfg.MySQL.BinlogServerID))
	}
	args = append(args, startLog)

	cmd := exec.Command("mysqlbinlog", args...)
	cmd.Dir = s.Cfg.Local.BinlogDir
	cmd.Stdout = os.Stdout
	var errBuf bytes.Buffer
	cmd.Stderr = io.MultiWriter(os.Stderr, &errBuf)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	if err := os.WriteFile(s.pidPath(), []byte(strconv.Itoa(cmd.Process.Pid)), 0o640); err != nil {
		_ = cmd.Process.Kill()
		return err
	}
	log.Printf("binlog streamer started pid=%d file=%s dir=%s", cmd.Process.Pid, startLog, s.Cfg.Local.BinlogDir)

	dumpCtx, dumpCancel := context.WithCancel(ctx)
	defer dumpCancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		select {
		case <-sigCh:
			cancel()
			dumpCancel()
			_ = cmd.Process.Signal(syscall.SIGTERM)
		case <-dumpCtx.Done():
		}
	}()

	err := cmd.Wait()
	dumpCancel()
	_ = os.Remove(s.pidPath())
	if err != nil {
		msg := strings.TrimSpace(errBuf.String())
		if msg != "" {
			return fmt.Errorf("%w: %s", err, msg)
		}
	}
	return err
}

func (s *Service) Stop() error {
	running, pid := s.isRunning()
	if !running {
		_ = os.Remove(s.pidPath())
		return fmt.Errorf("binlog streamer not running")
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return err
	}
	_ = os.Remove(s.pidPath())
	log.Printf("sent SIGTERM to binlog streamer pid=%d", pid)
	return nil
}

func (s *Service) Status() (running bool, pid int, err error) {
	running, pid = s.isRunning()
	return running, pid, nil
}

func (s *Service) isRunning() (bool, int) {
	b, err := os.ReadFile(s.pidPath())
	if err != nil {
		return false, 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return false, 0
	}
	if err := syscall.Kill(pid, 0); err != nil {
		return false, pid
	}
	return true, pid
}

func (s *Service) watchAndCatalog(ctx context.Context, minSeq int64) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	s.pruneLeftoverBinlogs(ctx, minSeq)
	s.scanBinlogDir(ctx, minSeq)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.scanBinlogDir(ctx, minSeq)
		}
	}
}

func (s *Service) pruneLeftoverBinlogs(ctx context.Context, minSeq int64) {
	entries, err := os.ReadDir(s.Cfg.Local.BinlogDir)
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	for _, name := range leftoverBinlogNames(names, minSeq) {
		path := filepath.Join(s.Cfg.Local.BinlogDir, name)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			log.Printf("remove leftover binlog %s: %v", name, err)
			continue
		}
		log.Printf("removed leftover binlog %s (older than dump start seq=%d)", name, minSeq)
		if rec, err := s.Repo.GetByFilename(ctx, name); err == nil && rec != nil {
			_ = s.Repo.MarkDeleted(ctx, rec.ID)
		}
	}
}

func (s *Service) scanBinlogDir(ctx context.Context, minSeq int64) {
	entries, err := os.ReadDir(s.Cfg.Local.BinlogDir)
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		names = append(names, e.Name())
	}
	maxSeq := highestBinlogSeq(names, minSeq)
	for _, name := range names {
		if !shouldCatalogBinlog(name, minSeq) {
			continue
		}
		seq, _ := parser.SequenceFromFilename(name)
		path := filepath.Join(s.Cfg.Local.BinlogDir, name)
		st, err := os.Stat(path)
		if err != nil || st.Size() == 0 {
			continue
		}
		live := seq == maxSeq
		if err := s.syncBinlogFile(ctx, path, name, st.Size(), live); err != nil {
			log.Printf("catalog binlog %s: %v", name, err)
		}
	}
}

func (s *Service) syncBinlogFile(ctx context.Context, path, name string, size int64, live bool) error {
	seq, _ := parser.SequenceFromFilename(name)
	bounds, err := parser.ParseBinlogBounds(path)
	if err != nil {
		bounds = &parser.BinlogBounds{Filename: name, Sequence: seq}
	}
	status := string(catalog.StatusStreaming)
	if !live {
		status = string(catalog.StatusCompleted)
	}

	rec, err := s.Repo.GetByFilename(ctx, name)
	if err != nil {
		return err
	}
	if rec == nil {
		rec = &catalog.BinlogArchive{
			ID:              uuid.NewString(),
			Filename:        name,
			SequenceNumber:  seq,
			StorageLocation: catalog.StorageLocal,
			LocalPath:       catalog.NullString(path),
			Status:          status,
			CreatedAt:       catalog.NowUnix(),
		}
		applyBinlogBounds(rec, bounds, live)
		rec.FileSize = catalog.NullInt64(size, true)
		if err := s.Repo.Insert(ctx, rec); err != nil {
			return err
		}
		log.Printf("cataloged binlog %s seq=%d status=%s size=%d", name, seq, status, size)
		if !live {
			s.uploadBinlog(ctx, rec, path, name)
		}
		return nil
	}

	wasComplete := rec.Status == string(catalog.StatusCompleted)
	if live && rec.Status == string(catalog.StatusStreaming) && rec.FileSize.Valid && rec.FileSize.Int64 == size {
		return nil
	}
	if wasComplete && !live && rec.FileSize.Valid && rec.FileSize.Int64 == size {
		return nil
	}
	applyBinlogBounds(rec, bounds, live)
	rec.FileSize = catalog.NullInt64(size, true)
	rec.LocalPath = catalog.NullString(path)
	rec.Status = status
	if err := s.Repo.Update(ctx, rec); err != nil {
		return err
	}
	if !live && !wasComplete {
		log.Printf("cataloged binlog %s seq=%d status=%s size=%d", name, seq, status, size)
		s.uploadBinlog(ctx, rec, path, name)
	}
	return nil
}

func applyBinlogBounds(rec *catalog.BinlogArchive, bounds *parser.BinlogBounds, live bool) {
	if bounds == nil {
		return
	}
	if bounds.Sequence != 0 {
		rec.SequenceNumber = bounds.Sequence
	}
	if !bounds.StartTime.IsZero() {
		rec.StartTime = catalog.NullInt64(bounds.StartTime.Unix(), true)
	}
	if bounds.StartGTID != "" {
		rec.StartGTID = catalog.NullString(bounds.StartGTID)
	}
	if live {
		rec.EndTime = catalog.NullInt64(0, false)
		rec.EndGTID = catalog.NullString("")
		return
	}
	if !bounds.EndTime.IsZero() {
		rec.EndTime = catalog.NullInt64(bounds.EndTime.Unix(), true)
	}
	if bounds.EndGTID != "" {
		rec.EndGTID = catalog.NullString(bounds.EndGTID)
	}
}

func (s *Service) uploadBinlog(ctx context.Context, rec *catalog.BinlogArchive, path, name string) {
	if !s.Store.HasCloud() {
		return
	}
	key := s.Store.ObjectKey(name, time.Now().UTC())
	url, err := s.Store.UploadFile(ctx, path, key)
	if err != nil {
		log.Printf("binlog cloud upload %s: %v", name, err)
		return
	}
	rec.CloudURL = catalog.NullString(url)
	rec.StorageLocation = catalog.StorageBoth
	_ = s.Repo.UpdateStorage(ctx, rec.ID, catalog.StorageBoth, nil, catalog.PtrString(url))
}

func ensureTrailingSlash(p string) string {
	if strings.HasSuffix(p, string(os.PathSeparator)) {
		return p
	}
	return p + string(os.PathSeparator)
}

func executilLook(name string) error {
	_, err := exec.LookPath(name)
	if err != nil {
		return fmt.Errorf("%s not found on PATH", name)
	}
	return nil
}

func mysqlbinlogSupports(flag string) bool {
	out, _ := exec.Command("mysqlbinlog", "--help").CombinedOutput()
	return strings.Contains(string(out), "--"+strings.TrimLeft(flag, "-"))
}
