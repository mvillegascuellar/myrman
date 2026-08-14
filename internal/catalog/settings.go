package catalog

import (
	"context"
	"database/sql"
	"fmt"
)

type SettingsRepo struct{ db *DB }

func NewSettingsRepo(db *DB) *SettingsRepo { return &SettingsRepo{db: db} }

func (r *SettingsRepo) GetAll(ctx context.Context) (map[string]string, error) {
	rows, err := r.db.SQL.QueryContext(ctx, `SELECT key, value FROM settings ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

func (r *SettingsRepo) Get(ctx context.Context, key string) (string, bool, error) {
	var v string
	err := r.db.SQL.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

func (r *SettingsRepo) Set(ctx context.Context, key, value string) error {
	_, err := r.db.SQL.ExecContext(ctx, `
INSERT INTO settings(key, value, updated_at) VALUES(?,?,?)
ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		key, value, NowUnix())
	return err
}

func (r *SettingsRepo) SetMany(ctx context.Context, kv map[string]string) error {
	return r.db.WithTx(ctx, func(tx *sql.Tx) error {
		now := NowUnix()
		stmt, err := tx.PrepareContext(ctx, `
INSERT INTO settings(key, value, updated_at) VALUES(?,?,?)
ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for k, v := range kv {
			if _, err := stmt.ExecContext(ctx, k, v, now); err != nil {
				return fmt.Errorf("set %s: %w", k, err)
			}
		}
		return nil
	})
}

func (r *SettingsRepo) Count(ctx context.Context) (int, error) {
	var n int
	err := r.db.SQL.QueryRowContext(ctx, `SELECT COUNT(1) FROM settings`).Scan(&n)
	return n, err
}

func (r *SettingsRepo) Delete(ctx context.Context, key string) error {
	_, err := r.db.SQL.ExecContext(ctx, `DELETE FROM settings WHERE key=?`, key)
	return err
}
