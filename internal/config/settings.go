package config

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

// DefaultCatalogPath is used when no --catalog / MYRMAN_CATALOG / imported setting exists.
const DefaultCatalogPath = "/var/lib/myrman/catalog.db"

// ResolveCatalogPath picks the SQLite catalog location for bootstrap.
// Priority: flag > MYRMAN_CATALOG env > default.
func ResolveCatalogPath(flag string) string {
	if strings.TrimSpace(flag) != "" {
		return flag
	}
	if v := os.Getenv("MYRMAN_CATALOG"); v != "" {
		return v
	}
	return DefaultCatalogPath
}

// KnownKeys returns all supported dotted setting keys (sorted).
func KnownKeys() []string {
	keys := make([]string, 0, len(knownKeySet))
	for k := range knownKeySet {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

var knownKeySet = map[string]struct{}{
	"datadir_hint":              {},
	"defaults_file":             {},
	"backup_tool":               {},
	"catalog":                   {},
	"compression":               {},
	"local.root":                {},
	"local.staging":             {},
	"local.physical_dir":        {},
	"local.binlog_dir":          {},
	"mysql.host":                {},
	"mysql.port":                {},
	"mysql.user":                {},
	"mysql.defaults_extra_file": {},
	"mysql.password":            {},
	"encryption.enabled":        {},
	"cloud.provider":            {},
	"cloud.prefix":              {},
	"cloud.server_name":         {},
	"cloud.s3.bucket":           {},
	"cloud.s3.region":           {},
	"cloud.s3.endpoint":         {},
	"cloud.gcs.bucket":          {},
	"cloud.azure.account":       {},
	"cloud.azure.container":     {},
	"retention.offsite_daily":       {},
	"retention.offsite_monthly":     {},
	"retention.offsite_yearly":      {},
	"retention.offsite_binlog_days": {},
	"retention.local_backup_sets":   {},
	"retention.local_binlog_days":   {},
}

func IsKnownKey(key string) bool {
	_, ok := knownKeySet[key]
	return ok
}

// ToSettings flattens a Config into dotted key/value pairs for SQLite.
func ToSettings(c *Config) map[string]string {
	m := map[string]string{
		"datadir_hint":              c.DatadirHint,
		"defaults_file":             c.DefaultsFile,
		"backup_tool":               c.BackupTool,
		"catalog":                   c.Catalog,
		"compression":               c.Compression,
		"local.root":                c.Local.Root,
		"local.staging":             c.Local.Staging,
		"local.physical_dir":        c.Local.PhysicalDir,
		"local.binlog_dir":          c.Local.BinlogDir,
		"mysql.host":                c.MySQL.Host,
		"mysql.port":                strconv.Itoa(c.MySQL.Port),
		"mysql.user":                c.MySQL.User,
		"mysql.defaults_extra_file": c.MySQL.DefaultsExtraFile,
		"encryption.enabled":        strconv.FormatBool(c.Encryption.Enabled),
		"cloud.provider":            c.Cloud.Provider,
		"cloud.prefix":              c.Cloud.Prefix,
		"cloud.server_name":         c.Cloud.ServerName,
		"cloud.s3.bucket":           c.Cloud.S3.Bucket,
		"cloud.s3.region":           c.Cloud.S3.Region,
		"cloud.s3.endpoint":         c.Cloud.S3.Endpoint,
		"cloud.gcs.bucket":          c.Cloud.GCS.Bucket,
		"cloud.azure.account":       c.Cloud.Azure.Account,
		"cloud.azure.container":     c.Cloud.Azure.Container,
		"retention.offsite_daily":       strconv.Itoa(c.Retention.OffsiteDaily),
		"retention.offsite_monthly":     strconv.Itoa(c.Retention.OffsiteMonthly),
		"retention.offsite_yearly":      strconv.Itoa(c.Retention.OffsiteYearly),
		"retention.offsite_binlog_days": strconv.Itoa(c.Retention.OffsiteBinlogDays),
		"retention.local_backup_sets":   strconv.Itoa(c.Retention.LocalBackupSets),
		"retention.local_binlog_days":   strconv.Itoa(c.Retention.LocalBinlogDays),
	}
	if c.MySQL.Password != "" {
		m["mysql.password"] = c.MySQL.Password
	}
	return m
}

// FromSettings builds a Config from defaults overlaid with SQLite settings.
func FromSettings(kv map[string]string) (*Config, error) {
	cfg := Defaults()
	if err := ApplySettings(cfg, kv); err != nil {
		return nil, err
	}
	normalize(cfg)
	return cfg, nil
}

// ApplySettings applies dotted key/values onto cfg.
func ApplySettings(cfg *Config, kv map[string]string) error {
	for k, v := range kv {
		if err := SetField(cfg, k, v); err != nil {
			return err
		}
	}
	return nil
}

// SetField sets one dotted config key on cfg.
func SetField(cfg *Config, key, value string) error {
	if !IsKnownKey(key) {
		return fmt.Errorf("unknown config key %q (use 'myrman config keys' for the list)", key)
	}
	switch key {
	case "datadir_hint":
		cfg.DatadirHint = value
	case "defaults_file":
		cfg.DefaultsFile = value
	case "backup_tool":
		cfg.BackupTool = value
	case "catalog":
		cfg.Catalog = value
	case "compression":
		cfg.Compression = value
	case "local.root":
		cfg.Local.Root = value
	case "local.staging":
		cfg.Local.Staging = value
	case "local.physical_dir":
		cfg.Local.PhysicalDir = value
	case "local.binlog_dir":
		cfg.Local.BinlogDir = value
	case "mysql.host":
		cfg.MySQL.Host = value
	case "mysql.port":
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("mysql.port: %w", err)
		}
		cfg.MySQL.Port = n
	case "mysql.user":
		cfg.MySQL.User = value
	case "mysql.defaults_extra_file":
		cfg.MySQL.DefaultsExtraFile = value
	case "mysql.password":
		cfg.MySQL.Password = value
	case "encryption.enabled":
		b, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("encryption.enabled: %w", err)
		}
		cfg.Encryption.Enabled = b
	case "cloud.provider":
		cfg.Cloud.Provider = value
	case "cloud.prefix":
		cfg.Cloud.Prefix = value
	case "cloud.server_name":
		cfg.Cloud.ServerName = value
	case "cloud.s3.bucket":
		cfg.Cloud.S3.Bucket = value
	case "cloud.s3.region":
		cfg.Cloud.S3.Region = value
	case "cloud.s3.endpoint":
		cfg.Cloud.S3.Endpoint = value
	case "cloud.gcs.bucket":
		cfg.Cloud.GCS.Bucket = value
	case "cloud.azure.account":
		cfg.Cloud.Azure.Account = value
	case "cloud.azure.container":
		cfg.Cloud.Azure.Container = value
	case "retention.offsite_daily":
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		cfg.Retention.OffsiteDaily = n
	case "retention.offsite_monthly":
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		cfg.Retention.OffsiteMonthly = n
	case "retention.offsite_yearly":
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		cfg.Retention.OffsiteYearly = n
	case "retention.offsite_binlog_days":
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		cfg.Retention.OffsiteBinlogDays = n
	case "retention.local_backup_sets":
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		cfg.Retention.LocalBackupSets = n
	case "retention.local_binlog_days":
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		cfg.Retention.LocalBinlogDays = n
	default:
		return fmt.Errorf("unhandled key %q", key)
	}
	return nil
}

// FormatYAML returns a human-readable YAML-ish dump; secrets redacted unless revealSecrets.
func FormatYAML(c *Config, revealSecrets bool) string {
	pw := c.MySQL.Password
	if pw != "" && !revealSecrets {
		pw = "***"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "datadir_hint: %s\n", c.DatadirHint)
	fmt.Fprintf(&b, "defaults_file: %s\n", c.DefaultsFile)
	fmt.Fprintf(&b, "backup_tool: %s\n", c.BackupTool)
	fmt.Fprintf(&b, "catalog: %s\n", c.Catalog)
	fmt.Fprintf(&b, "compression: %s\n", c.Compression)
	fmt.Fprintf(&b, "local:\n")
	fmt.Fprintf(&b, "  root: %s\n", c.Local.Root)
	fmt.Fprintf(&b, "  staging: %s\n", c.Local.Staging)
	fmt.Fprintf(&b, "  physical_dir: %s\n", c.Local.PhysicalDir)
	fmt.Fprintf(&b, "  binlog_dir: %s\n", c.Local.BinlogDir)
	fmt.Fprintf(&b, "mysql:\n")
	fmt.Fprintf(&b, "  host: %s\n", c.MySQL.Host)
	fmt.Fprintf(&b, "  port: %d\n", c.MySQL.Port)
	fmt.Fprintf(&b, "  user: %s\n", c.MySQL.User)
	fmt.Fprintf(&b, "  defaults_extra_file: %q\n", c.MySQL.DefaultsExtraFile)
	fmt.Fprintf(&b, "  password: %s\n", pw)
	fmt.Fprintf(&b, "encryption:\n")
	fmt.Fprintf(&b, "  enabled: %t\n", c.Encryption.Enabled)
	fmt.Fprintf(&b, "cloud:\n")
	fmt.Fprintf(&b, "  provider: %s\n", c.Cloud.Provider)
	fmt.Fprintf(&b, "  prefix: %s\n", c.Cloud.Prefix)
	fmt.Fprintf(&b, "  server_name: %s\n", c.Cloud.ServerName)
	fmt.Fprintf(&b, "  s3:\n")
	fmt.Fprintf(&b, "    bucket: %s\n", c.Cloud.S3.Bucket)
	fmt.Fprintf(&b, "    region: %s\n", c.Cloud.S3.Region)
	fmt.Fprintf(&b, "    endpoint: %s\n", c.Cloud.S3.Endpoint)
	fmt.Fprintf(&b, "  gcs:\n")
	fmt.Fprintf(&b, "    bucket: %s\n", c.Cloud.GCS.Bucket)
	fmt.Fprintf(&b, "  azure:\n")
	fmt.Fprintf(&b, "    account: %s\n", c.Cloud.Azure.Account)
	fmt.Fprintf(&b, "    container: %s\n", c.Cloud.Azure.Container)
	fmt.Fprintf(&b, "retention:\n")
	fmt.Fprintf(&b, "  offsite_daily: %d\n", c.Retention.OffsiteDaily)
	fmt.Fprintf(&b, "  offsite_monthly: %d\n", c.Retention.OffsiteMonthly)
	fmt.Fprintf(&b, "  offsite_yearly: %d\n", c.Retention.OffsiteYearly)
	fmt.Fprintf(&b, "  offsite_binlog_days: %d\n", c.Retention.OffsiteBinlogDays)
	fmt.Fprintf(&b, "  local_backup_sets: %d\n", c.Retention.LocalBackupSets)
	fmt.Fprintf(&b, "  local_binlog_days: %d\n", c.Retention.LocalBinlogDays)
	return b.String()
}

func normalize(cfg *Config) {
	if cfg.Local.PhysicalDir == "" {
		cfg.Local.PhysicalDir = cfg.Local.Root + "/physical"
	}
	if cfg.Local.BinlogDir == "" {
		cfg.Local.BinlogDir = cfg.Local.Root + "/binlogs"
	}
	if cfg.Local.Staging == "" {
		cfg.Local.Staging = cfg.Local.Root + "/staging"
	}
	if cfg.Catalog == "" {
		cfg.Catalog = cfg.Local.Root + "/catalog.db"
	}
	cfg.BackupTool = strings.ToLower(strings.TrimSpace(cfg.BackupTool))
	cfg.Compression = strings.ToLower(strings.TrimSpace(cfg.Compression))
	cfg.Cloud.Provider = strings.ToLower(strings.TrimSpace(cfg.Cloud.Provider))
	if p := os.Getenv("MYRMAN_MYSQL_PASSWORD"); p != "" {
		cfg.MySQL.Password = p
	}
}

// ParseSetArg accepts "key=value" or ("key", "value").
func ParseSetArg(args []string) (key, value string, err error) {
	if len(args) == 1 {
		k, v, ok := strings.Cut(args[0], "=")
		if !ok || strings.TrimSpace(k) == "" {
			return "", "", fmt.Errorf("expected key=value")
		}
		return strings.TrimSpace(k), v, nil
	}
	if len(args) == 2 {
		return strings.TrimSpace(args[0]), args[1], nil
	}
	return "", "", fmt.Errorf("usage: config set <key> <value> | config set <key>=<value>")
}
