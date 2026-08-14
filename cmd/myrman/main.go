package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/michaelvillegas/myrman/internal/backup"
	"github.com/michaelvillegas/myrman/internal/binlog"
	"github.com/michaelvillegas/myrman/internal/catalog"
	"github.com/michaelvillegas/myrman/internal/config"
	"github.com/michaelvillegas/myrman/internal/recover"
	"github.com/michaelvillegas/myrman/internal/retention"
	"github.com/michaelvillegas/myrman/internal/storage"
	"github.com/michaelvillegas/myrman/internal/version"
	"github.com/spf13/cobra"
)

var (
	cfgFile     string
	catalogPath string
	dryRun      bool
)

func main() {
	root := &cobra.Command{
		Use:   "myrman",
		Short: "MySQL/Percona/MariaDB backup & recovery manager",
	}
	root.PersistentFlags().StringVar(&cfgFile, "config", "", "YAML config path (used by 'config import')")
	root.PersistentFlags().StringVar(&catalogPath, "catalog", "", "SQLite catalog path (default: /var/lib/myrman/catalog.db or MYRMAN_CATALOG)")
	root.PersistentFlags().BoolVar(&dryRun, "dry-run", false, "do not delete/mutate (where supported)")

	root.AddCommand(
		versionCmd(),
		configCmd(),
		backupCmd(),
		binlogCmd(),
		catalogCmd(),
		retainCmd(),
		recoverCmd(),
	)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

// loadRuntime loads configuration from the SQLite settings table.
func loadRuntime() (*config.Config, *catalog.DB, *storage.Coordinator, error) {
	cfg, db, err := loadConfigFromCatalog()
	if err != nil {
		return nil, nil, nil, err
	}
	if err := cfg.EnsureDirs(); err != nil {
		_ = db.Close()
		return nil, nil, nil, err
	}
	store, err := storage.NewCoordinator(cfg)
	if err != nil {
		_ = db.Close()
		return nil, nil, nil, err
	}
	return cfg, db, store, nil
}

// loadConfigFromCatalog opens the catalog and materializes Config from settings.
// Does not create backup directories (safe for config show/get).
func loadConfigFromCatalog() (*config.Config, *catalog.DB, error) {
	path := config.ResolveCatalogPath(catalogPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, nil, fmt.Errorf("catalog dir: %w", err)
	}
	db, err := catalog.Open(path)
	if err != nil {
		return nil, nil, err
	}
	settings := catalog.NewSettingsRepo(db)
	n, err := settings.Count(context.Background())
	if err != nil {
		_ = db.Close()
		return nil, nil, err
	}
	if n == 0 {
		_ = db.Close()
		return nil, nil, fmt.Errorf("no configuration in catalog %s; run: myrman --catalog %s config import --config myrman.yaml", path, path)
	}
	kv, err := settings.GetAll(context.Background())
	if err != nil {
		_ = db.Close()
		return nil, nil, err
	}
	cfg, err := config.FromSettings(kv)
	if err != nil {
		_ = db.Close()
		return nil, nil, err
	}
	cfg.Catalog = path
	return cfg, db, nil
}

func openCatalogOnly() (*catalog.DB, string, error) {
	path := config.ResolveCatalogPath(catalogPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, "", fmt.Errorf("catalog dir: %w", err)
	}
	db, err := catalog.Open(path)
	if err != nil {
		return nil, "", err
	}
	return db, path, nil
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("myrman %s (commit %s, built %s)\n", version.Version, version.Commit, version.Date)
		},
	}
}

func configCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Manage configuration stored in the SQLite catalog"}

	importCmd := &cobra.Command{
		Use:   "import",
		Short: "Import a YAML config file into the catalog settings table",
		RunE: func(cmd *cobra.Command, args []string) error {
			if cfgFile == "" {
				return fmt.Errorf("--config is required for import")
			}
			fileCfg, err := config.Load(cfgFile)
			if err != nil {
				return err
			}
			// Catalog location: --catalog flag wins; else YAML catalog path.
			path := catalogPath
			if path == "" {
				path = fileCfg.Catalog
			}
			if path == "" {
				path = config.DefaultCatalogPath
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
				return err
			}
			db, err := catalog.Open(path)
			if err != nil {
				return err
			}
			defer db.Close()

			fileCfg.Catalog = path
			kv := config.ToSettings(fileCfg)
			if err := catalog.NewSettingsRepo(db).SetMany(context.Background(), kv); err != nil {
				return err
			}
			fmt.Printf("imported %d settings into %s\n", len(kv), path)
			return nil
		},
	}

	showCmd := &cobra.Command{
		Use:   "show",
		Short: "Show the active configuration from the catalog",
		RunE: func(cmd *cobra.Command, args []string) error {
			reveal, _ := cmd.Flags().GetBool("reveal-secrets")
			cfg, db, err := loadConfigFromCatalog()
			if err != nil {
				return err
			}
			defer db.Close()
			fmt.Print(config.FormatYAML(cfg, reveal))
			return nil
		},
	}
	showCmd.Flags().Bool("reveal-secrets", false, "show mysql.password in clear text")

	getCmd := &cobra.Command{
		Use:   "get <key>",
		Short: "Get a single configuration value",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			db, _, err := openCatalogOnly()
			if err != nil {
				return err
			}
			defer db.Close()
			key := args[0]
			if !config.IsKnownKey(key) {
				return fmt.Errorf("unknown key %q", key)
			}
			v, ok, err := catalog.NewSettingsRepo(db).Get(context.Background(), key)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("key %q is not set (import config first)", key)
			}
			if key == "mysql.password" && v != "" {
				reveal, _ := cmd.Flags().GetBool("reveal-secrets")
				if !reveal {
					v = "***"
				}
			}
			fmt.Println(v)
			return nil
		},
	}
	getCmd.Flags().Bool("reveal-secrets", false, "show mysql.password in clear text")

	setCmd := &cobra.Command{
		Use:   "set <key> <value>| <key>=<value>",
		Short: "Update a configuration value in the catalog",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			key, value, err := config.ParseSetArg(args)
			if err != nil {
				return err
			}
			if !config.IsKnownKey(key) {
				return fmt.Errorf("unknown key %q", key)
			}
			// Validate type by applying onto a temp config.
			tmp := config.Defaults()
			if err := config.SetField(tmp, key, value); err != nil {
				return err
			}
			db, path, err := openCatalogOnly()
			if err != nil {
				return err
			}
			defer db.Close()
			repo := catalog.NewSettingsRepo(db)
			n, err := repo.Count(context.Background())
			if err != nil {
				return err
			}
			if n == 0 {
				// Seed defaults then apply the set so first-time CLI config works.
				seed := config.Defaults()
				seed.Catalog = path
				if err := repo.SetMany(context.Background(), config.ToSettings(seed)); err != nil {
					return err
				}
			}
			if err := repo.Set(context.Background(), key, value); err != nil {
				return err
			}
			display := value
			if key == "mysql.password" && value != "" {
				display = "***"
			}
			fmt.Printf("set %s=%s (catalog %s)\n", key, display, path)
			return nil
		},
	}

	keysCmd := &cobra.Command{
		Use:   "keys",
		Short: "List supported configuration keys",
		Run: func(cmd *cobra.Command, args []string) {
			for _, k := range config.KnownKeys() {
				fmt.Println(k)
			}
		},
	}

	cmd.AddCommand(importCmd, showCmd, getCmd, setCmd, keysCmd)
	return cmd
}

func backupCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "backup", Short: "Run physical backups"}
	var parent string
	full := &cobra.Command{
		Use:   "full",
		Short: "Take a full physical backup",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, db, store, err := loadRuntime()
			if err != nil {
				return err
			}
			defer db.Close()
			rec, err := backup.NewRunner(cfg, db, store).Run(context.Background(), backup.Options{})
			if err != nil {
				return err
			}
			fmt.Printf("FULL backup completed: %s lsn=%d..%d\n", rec.ID, rec.LSNFrom, rec.LSNTo)
			return nil
		},
	}
	inc := &cobra.Command{
		Use:   "incremental",
		Short: "Take an incremental backup",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, db, store, err := loadRuntime()
			if err != nil {
				return err
			}
			defer db.Close()
			rec, err := backup.NewRunner(cfg, db, store).Run(context.Background(), backup.Options{
				Incremental: true,
				ParentID:    parent,
			})
			if err != nil {
				return err
			}
			fmt.Printf("INCREMENTAL backup completed: %s parent=%s lsn=%d..%d\n",
				rec.ID, parent, rec.LSNFrom, rec.LSNTo)
			return nil
		},
	}
	inc.Flags().StringVar(&parent, "parent", "latest", "parent backup UUID or 'latest'")
	cmd.AddCommand(full, inc)
	return cmd
}

func binlogCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "binlog", Short: "Continuous binlog streaming"}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "start",
			Short: "Start mysqlbinlog --stop-never archiver",
			RunE: func(cmd *cobra.Command, args []string) error {
				cfg, db, store, err := loadRuntime()
				if err != nil {
					return err
				}
				defer db.Close()
				return binlog.NewService(cfg, db, store).Start(context.Background())
			},
		},
		&cobra.Command{
			Use:   "stop",
			Short: "Stop binlog archiver",
			RunE: func(cmd *cobra.Command, args []string) error {
				cfg, db, store, err := loadRuntime()
				if err != nil {
					return err
				}
				defer db.Close()
				return binlog.NewService(cfg, db, store).Stop()
			},
		},
		&cobra.Command{
			Use:   "status",
			Short: "Show binlog archiver status",
			RunE: func(cmd *cobra.Command, args []string) error {
				cfg, db, store, err := loadRuntime()
				if err != nil {
					return err
				}
				defer db.Close()
				running, pid, err := binlog.NewService(cfg, db, store).Status()
				if err != nil {
					return err
				}
				if running {
					fmt.Printf("running pid=%d\n", pid)
				} else {
					fmt.Println("stopped")
				}
				return nil
			},
		},
	)
	return cmd
}

func catalogCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "catalog", Short: "Inspect backup catalog"}
	var kind string
	var limit int
	list := &cobra.Command{
		Use:   "list",
		Short: "List physical backups or binlogs",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, db, _, err := loadRuntime()
			if err != nil {
				return err
			}
			defer db.Close()
			ctx := context.Background()
			if kind == "binlog" {
				rows, err := catalog.NewBinlogRepo(db).List(ctx, limit)
				if err != nil {
					return err
				}
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(rows)
			}
			rows, err := catalog.NewPhysicalRepo(db).List(ctx, "", limit)
			if err != nil {
				return err
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(rows)
		},
	}
	list.Flags().StringVar(&kind, "type", "physical", "physical|binlog")
	list.Flags().IntVar(&limit, "limit", 50, "max rows")

	show := &cobra.Command{
		Use:   "show [id]",
		Short: "Show a physical backup by id",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, db, _, err := loadRuntime()
			if err != nil {
				return err
			}
			defer db.Close()
			b, err := catalog.NewPhysicalRepo(db).Get(context.Background(), args[0])
			if err != nil {
				return err
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(b)
		},
	}
	cmd.AddCommand(list, show)
	return cmd
}

func retainCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "retain", Short: "GFS retention tagging and pruning"}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "tag",
			Short: "Assign DAILY/MONTHLY/YEARLY tags",
			RunE: func(cmd *cobra.Command, args []string) error {
				cfg, db, store, err := loadRuntime()
				if err != nil {
					return err
				}
				defer db.Close()
				tagged, err := retention.NewEngine(cfg, db, store).Tag(context.Background())
				if err != nil {
					return err
				}
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(tagged)
			},
		},
		&cobra.Command{
			Use:   "run",
			Short: "Tag and prune expired backups (LSN-safe)",
			RunE: func(cmd *cobra.Command, args []string) error {
				cfg, db, store, err := loadRuntime()
				if err != nil {
					return err
				}
				defer db.Close()
				sum, err := retention.NewEngine(cfg, db, store).Run(context.Background(), dryRun)
				if err != nil {
					return err
				}
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(sum)
			},
		},
	)
	return cmd
}

func recoverCmd() *cobra.Command {
	var targetTime, targetGTID, outDir string
	var apply bool
	cmd := &cobra.Command{
		Use:   "recover",
		Short: "Point-in-time recovery orchestrator",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, db, store, err := loadRuntime()
			if err != nil {
				return err
			}
			defer db.Close()
			opts := recover.Options{TargetGTID: targetGTID, Apply: apply, OutputDir: outDir}
			if targetTime != "" {
				t, err := time.ParseInLocation("2006-01-02 15:04:05", targetTime, time.UTC)
				if err != nil {
					return fmt.Errorf("parse --target-time: %w", err)
				}
				opts.TargetTime = t
			}
			plan, err := recover.New(cfg, db, store).Recover(context.Background(), opts)
			if err != nil {
				return err
			}
			fmt.Printf("FULL: %s\n", plan.Full.ID)
			for i, inc := range plan.Incrementals {
				fmt.Printf("INC[%d]: %s\n", i, inc.ID)
			}
			fmt.Printf("Prepare steps: %d\n", len(plan.PrepareCmds))
			fmt.Printf("Binlogs: %d\n", len(plan.Binlogs))
			fmt.Printf("Script: %s\n", plan.ScriptPath)
			fmt.Printf("Binlog command:\n%s\n", plan.BinlogCmd)
			return nil
		},
	}
	cmd.Flags().StringVar(&targetTime, "target-time", "", "YYYY-MM-DD HH:MM:SS (UTC)")
	cmd.Flags().StringVar(&targetGTID, "target-gtid", "", "UUID:SEQ recovery target")
	cmd.Flags().StringVar(&outDir, "output-dir", "", "staging/output directory")
	cmd.Flags().BoolVar(&apply, "apply", false, "execute prepare and binlog apply")
	return cmd
}
