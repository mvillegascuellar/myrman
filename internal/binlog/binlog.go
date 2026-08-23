package binlog

import (
	"context"
	"fmt"
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

func (s *Service) Start(ctx context.Context) error {
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
	startLog, err := ChooseStartLog(requested, lastCataloged, serverLogs)
	if err != nil {
		return err
	}
	log.Printf("binlog stream start file=%s (server has %d files, last cataloged=%q)", startLog, len(serverLogs), lastCataloged)

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
		startLog,
	}

	cmd := exec.Command("mysqlbinlog", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	if err := os.WriteFile(s.pidPath(), []byte(strconv.Itoa(cmd.Process.Pid)), 0o640); err != nil {
		_ = cmd.Process.Kill()
		return err
	}
	log.Printf("binlog streamer started pid=%d dir=%s", cmd.Process.Pid, s.Cfg.Local.BinlogDir)

	watchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go s.watchAndCatalog(watchCtx)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case <-sigCh:
			cancel()
			_ = cmd.Process.Signal(syscall.SIGTERM)
		case <-ctx.Done():
			cancel()
			_ = cmd.Process.Signal(syscall.SIGTERM)
		}
	}()

	err = cmd.Wait()
	_ = os.Remove(s.pidPath())
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

func (s *Service) watchAndCatalog(ctx context.Context) {
	seen := map[string]int64{}
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			entries, err := os.ReadDir(s.Cfg.Local.BinlogDir)
			if err != nil {
				continue
			}
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				name := e.Name()
				path := filepath.Join(s.Cfg.Local.BinlogDir, name)
				st, err := os.Stat(path)
				if err != nil {
					continue
				}
				prev, ok := seen[name]
				if !ok {
					seen[name] = st.Size()
					continue
				}
				// Stable size => closed enough to catalog
				if prev == st.Size() && st.Size() > 0 {
					if _, err := s.Repo.GetByFilename(ctx, name); err == nil {
						continue // already cataloged
					}
					if err := s.catalogFile(ctx, path, name, st.Size()); err != nil {
						log.Printf("catalog binlog %s: %v", name, err)
						continue
					}
					seen[name] = st.Size()
				} else {
					seen[name] = st.Size()
				}
			}
		}
	}
}

func (s *Service) catalogFile(ctx context.Context, path, name string, size int64) error {
	bounds, err := parser.ParseBinlogBounds(path)
	if err != nil {
		// Still catalog with sequence only
		seq, _ := parser.SequenceFromFilename(name)
		bounds = &parser.BinlogBounds{Filename: name, Sequence: seq}
		log.Printf("warning: parse bounds for %s: %v", name, err)
	}
	rec := &catalog.BinlogArchive{
		ID:              uuid.NewString(),
		Filename:        name,
		SequenceNumber:  bounds.Sequence,
		FileSize:        catalog.NullInt64(size, true),
		StorageLocation: catalog.StorageLocal,
		LocalPath:       catalog.NullString(path),
		Status:          "COMPLETED",
		CreatedAt:       catalog.NowUnix(),
	}
	if !bounds.StartTime.IsZero() {
		rec.StartTime = catalog.NullInt64(bounds.StartTime.Unix(), true)
	}
	if !bounds.EndTime.IsZero() {
		rec.EndTime = catalog.NullInt64(bounds.EndTime.Unix(), true)
	}
	if bounds.StartGTID != "" {
		rec.StartGTID = catalog.NullString(bounds.StartGTID)
	}
	if bounds.EndGTID != "" {
		rec.EndGTID = catalog.NullString(bounds.EndGTID)
	}
	if err := s.Repo.Insert(ctx, rec); err != nil {
		return err
	}
	if s.Store.HasCloud() {
		key := s.Store.ObjectKey(name, time.Now().UTC())
		url, err := s.Store.UploadFile(ctx, path, key)
		if err != nil {
			log.Printf("binlog cloud upload %s: %v", name, err)
		} else {
			rec.CloudURL = catalog.NullString(url)
			rec.StorageLocation = catalog.StorageBoth
			_ = s.Repo.UpdateStorage(ctx, rec.ID, catalog.StorageBoth, nil, catalog.PtrString(url))
		}
	}
	log.Printf("cataloged binlog %s seq=%d", name, rec.SequenceNumber)
	return nil
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
