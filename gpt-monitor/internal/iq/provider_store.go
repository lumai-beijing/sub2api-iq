package iq

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ProviderRecord 是库里的一个第三方供应商。APIKeySealed 是加密后的 Key，只在发题前解密。
type ProviderRecord struct {
	ID              int64
	Name            string
	BaseURL         string
	APIFormat       string
	APIKeySealed    string
	APIKeyHint      string
	Models          string // 与号池同格式：gpt-6-astra@xhigh,gpt-5.6-sol@max
	Enabled         bool
	IntervalSeconds int
	JitterSeconds   int
	SamplesPerModel int
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

var ErrProviderNotFound = errors.New("供应商不存在")

const providerSchema = `
CREATE TABLE IF NOT EXISTS iq_providers (
	id INTEGER PRIMARY KEY,
	name TEXT NOT NULL,
	base_url TEXT NOT NULL,
	api_format TEXT NOT NULL,
	api_key_sealed TEXT NOT NULL,
	api_key_hint TEXT NOT NULL DEFAULT '',
	models TEXT NOT NULL,
	enabled INTEGER NOT NULL DEFAULT 1,
	interval_seconds INTEGER NOT NULL,
	jitter_seconds INTEGER NOT NULL,
	samples_per_model INTEGER NOT NULL DEFAULT 1,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_iq_providers_name ON iq_providers(name);`

const providerColumns = `id, name, base_url, api_format, api_key_sealed, api_key_hint, models, enabled,
	interval_seconds, jitter_seconds, samples_per_model, created_at, updated_at`

func scanProvider(row interface{ Scan(...any) error }) (ProviderRecord, error) {
	var (
		p                ProviderRecord
		enabled          int
		created, updated int64
	)
	if err := row.Scan(&p.ID, &p.Name, &p.BaseURL, &p.APIFormat, &p.APIKeySealed, &p.APIKeyHint, &p.Models, &enabled,
		&p.IntervalSeconds, &p.JitterSeconds, &p.SamplesPerModel, &created, &updated); err != nil {
		return ProviderRecord{}, err
	}
	p.Enabled = enabled == 1
	p.CreatedAt, p.UpdatedAt = time.Unix(created, 0).UTC(), time.Unix(updated, 0).UTC()
	return p, nil
}

func (s *Store) ListProviders(ctx context.Context) ([]ProviderRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+providerColumns+` FROM iq_providers ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list iq providers: %w", err)
	}
	defer rows.Close()
	out := []ProviderRecord{}
	for rows.Next() {
		p, err := scanProvider(rows)
		if err != nil {
			return nil, fmt.Errorf("scan iq provider: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) GetProvider(ctx context.Context, id int64) (ProviderRecord, error) {
	p, err := scanProvider(s.db.QueryRowContext(ctx, `SELECT `+providerColumns+` FROM iq_providers WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ProviderRecord{}, ErrProviderNotFound
	}
	if err != nil {
		return ProviderRecord{}, fmt.Errorf("get iq provider: %w", err)
	}
	return p, nil
}

func (s *Store) CountProviders(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM iq_providers`).Scan(&n)
	return n, err
}

func (s *Store) InsertProvider(ctx context.Context, p ProviderRecord) (int64, error) {
	result, err := s.db.ExecContext(ctx, `
INSERT INTO iq_providers (name, base_url, api_format, api_key_sealed, api_key_hint, models, enabled,
	interval_seconds, jitter_seconds, samples_per_model, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.Name, p.BaseURL, p.APIFormat, p.APIKeySealed, p.APIKeyHint, p.Models, boolToInt(p.Enabled),
		p.IntervalSeconds, p.JitterSeconds, p.SamplesPerModel, p.CreatedAt.Unix(), p.UpdatedAt.Unix())
	if err != nil {
		return 0, fmt.Errorf("insert iq provider: %w", err)
	}
	return result.LastInsertId()
}

func (s *Store) UpdateProvider(ctx context.Context, p ProviderRecord) error {
	result, err := s.db.ExecContext(ctx, `
UPDATE iq_providers SET name = ?, base_url = ?, api_format = ?, api_key_sealed = ?, api_key_hint = ?, models = ?,
	enabled = ?, interval_seconds = ?, jitter_seconds = ?, samples_per_model = ?, updated_at = ?
WHERE id = ?`,
		p.Name, p.BaseURL, p.APIFormat, p.APIKeySealed, p.APIKeyHint, p.Models, boolToInt(p.Enabled),
		p.IntervalSeconds, p.JitterSeconds, p.SamplesPerModel, p.UpdatedAt.Unix(), p.ID)
	if err != nil {
		return fmt.Errorf("update iq provider: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrProviderNotFound
	}
	return nil
}

// DeleteProvider 删除供应商，并按 keepProbes 决定是否连同它的检测记录一起删。
func (s *Store) DeleteProvider(ctx context.Context, id int64, keepProbes bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `DELETE FROM iq_providers WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete iq provider: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrProviderNotFound
	}
	if !keepProbes {
		if _, err := tx.ExecContext(ctx, `DELETE FROM iq_probes WHERE provider_id = ?`, id); err != nil {
			return fmt.Errorf("delete iq provider probes: %w", err)
		}
	}
	return tx.Commit()
}
