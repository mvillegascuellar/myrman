package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/viper"
)

type Config struct {
	DatadirHint  string          `mapstructure:"datadir_hint"`
	DefaultsFile string          `mapstructure:"defaults_file"`
	BackupTool   string          `mapstructure:"backup_tool"`
	Local        LocalConfig     `mapstructure:"local"`
	Catalog      string          `mapstructure:"catalog"`
	MySQL        MySQLConfig     `mapstructure:"mysql"`
	Compression  string          `mapstructure:"compression"`
	Encryption   EncryptionConfig `mapstructure:"encryption"`
	Cloud        CloudConfig     `mapstructure:"cloud"`
	Retention    RetentionConfig `mapstructure:"retention"`
}

type LocalConfig struct {
	Root        string `mapstructure:"root"`
	Staging     string `mapstructure:"staging"`
	PhysicalDir string `mapstructure:"physical_dir"`
	BinlogDir   string `mapstructure:"binlog_dir"`
}

type MySQLConfig struct {
	Host               string `mapstructure:"host"`
	Port               int    `mapstructure:"port"`
	User               string `mapstructure:"user"`
	DefaultsExtraFile  string `mapstructure:"defaults_extra_file"`
	Password           string `mapstructure:"-"` // from MYRMAN_MYSQL_PASSWORD
	BinlogStart        string `mapstructure:"binlog_start"`     // empty = auto from SHOW BINARY LOGS
	BinlogServerID     int    `mapstructure:"binlog_server_id"` // mysqlbinlog dump connection server_id; 0 = omit
}

type EncryptionConfig struct {
	Enabled bool `mapstructure:"enabled"`
}

type CloudConfig struct {
	Provider   string      `mapstructure:"provider"`
	Prefix     string      `mapstructure:"prefix"`
	ServerName string      `mapstructure:"server_name"`
	S3         S3Config    `mapstructure:"s3"`
	GCS        GCSConfig   `mapstructure:"gcs"`
	Azure      AzureConfig `mapstructure:"azure"`
}

type S3Config struct {
	Bucket   string `mapstructure:"bucket"`
	Region   string `mapstructure:"region"`
	Endpoint string `mapstructure:"endpoint"`
}

type GCSConfig struct {
	Bucket string `mapstructure:"bucket"`
}

type AzureConfig struct {
	Account   string `mapstructure:"account"`
	Container string `mapstructure:"container"`
}

type RetentionConfig struct {
	OffsiteDaily      int `mapstructure:"offsite_daily"`
	OffsiteMonthly    int `mapstructure:"offsite_monthly"`
	OffsiteYearly     int `mapstructure:"offsite_yearly"`
	OffsiteBinlogDays int `mapstructure:"offsite_binlog_days"`
	LocalBackupSets   int `mapstructure:"local_backup_sets"`
	LocalBinlogDays   int `mapstructure:"local_binlog_days"`
}

func Defaults() *Config {
	return &Config{
		DatadirHint:  "/var/lib/mysql",
		DefaultsFile: "/etc/my.cnf",
		BackupTool:   "auto",
		Local: LocalConfig{
			Root:        "/var/lib/myrman",
			Staging:     "/var/lib/myrman/staging",
			PhysicalDir: "/var/lib/myrman/physical",
			BinlogDir:   "/var/lib/myrman/binlogs",
		},
		Catalog:     "/var/lib/myrman/catalog.db",
		MySQL:       MySQLConfig{Host: "127.0.0.1", Port: 3306, User: "backup", BinlogServerID: 65534},
		Compression: "zstd",
		Encryption:  EncryptionConfig{Enabled: false},
		Cloud: CloudConfig{
			Provider:   "none",
			Prefix:     "myrman",
			ServerName: "mysql-primary",
			S3:         S3Config{Region: "us-east-1"},
		},
		Retention: RetentionConfig{
			OffsiteDaily:      30,
			OffsiteMonthly:    12,
			OffsiteYearly:     10,
			OffsiteBinlogDays: 30,
			LocalBackupSets:   2,
			LocalBinlogDays:   10,
		},
	}
}

// Load reads config from path (optional), env MYRMAN_*, and applies defaults.
func Load(path string) (*Config, error) {
	cfg := Defaults()
	v := viper.New()
	v.SetConfigType("yaml")
	v.SetEnvPrefix("MYRMAN")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	if path != "" {
		v.SetConfigFile(path)
		if err := v.ReadInConfig(); err != nil {
			return nil, fmt.Errorf("read config: %w", err)
		}
	}

	if err := v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	normalize(cfg)
	return cfg, nil
}

func (c *Config) EnsureDirs() error {
	dirs := []string{c.Local.Root, c.Local.Staging, c.Local.PhysicalDir, c.Local.BinlogDir, c.Local.Root + "/run"}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o750); err != nil {
			return fmt.Errorf("mkdir %s: %w", d, err)
		}
	}
	return nil
}
