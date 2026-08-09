package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrPointLimit = errors.New("saved point limit reached")

type Point struct {
	ID        int64
	Name      string
	Latitude  float64
	Longitude float64
}

type DailyUsage struct {
	Day        time.Time
	Requests   int64
	Successful int64
	Failed     int64
}

type PostgreSQL struct{ pool *pgxpool.Pool }

func Open(ctx context.Context, host string, port int, name, user, password string, maxConns int32) (*PostgreSQL, error) {
	cfg, err := pgxpool.ParseConfig("")
	if err != nil {
		return nil, err
	}
	cfg.ConnConfig.Host, cfg.ConnConfig.Port = host, uint16(port)
	cfg.ConnConfig.Database, cfg.ConnConfig.User, cfg.ConnConfig.Password = name, user, password
	cfg.MaxConns = maxConns
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create PostgreSQL pool: %w", err)
	}
	db := &PostgreSQL{pool: pool}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect PostgreSQL: %w", err)
	}
	if err := db.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return db, nil
}

func (db *PostgreSQL) Close() {
	if db != nil && db.pool != nil {
		db.pool.Close()
	}
}

func (db *PostgreSQL) migrate(ctx context.Context) error {
	_, err := db.pool.Exec(ctx, `
CREATE TABLE IF NOT EXISTS bot_users (
 telegram_user_id bigint PRIMARY KEY, first_seen_at timestamptz NOT NULL DEFAULT now(), last_seen_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS saved_points (
 id bigserial PRIMARY KEY, telegram_user_id bigint NOT NULL REFERENCES bot_users(telegram_user_id) ON DELETE CASCADE,
 name varchar(64) NOT NULL, latitude double precision NOT NULL CHECK (latitude BETWEEN -90 AND 90),
 longitude double precision NOT NULL CHECK (longitude BETWEEN -180 AND 180), created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS saved_points_user_name_ci ON saved_points(telegram_user_id, lower(name));
CREATE INDEX IF NOT EXISTS saved_points_user_created ON saved_points(telegram_user_id, created_at, id);
CREATE TABLE IF NOT EXISTS forecast_usage_daily (
 day date NOT NULL, telegram_user_id bigint NOT NULL REFERENCES bot_users(telegram_user_id) ON DELETE CASCADE,
 request_count bigint NOT NULL DEFAULT 0 CHECK (request_count >= 0),
 successful_count bigint NOT NULL DEFAULT 0, failed_count bigint NOT NULL DEFAULT 0,
 PRIMARY KEY(day, telegram_user_id)
);
ALTER TABLE forecast_usage_daily ADD COLUMN IF NOT EXISTS successful_count bigint NOT NULL DEFAULT 0;
ALTER TABLE forecast_usage_daily ADD COLUMN IF NOT EXISTS failed_count bigint NOT NULL DEFAULT 0;
UPDATE forecast_usage_daily SET successful_count=request_count WHERE request_count>0 AND successful_count=0 AND failed_count=0;`)
	if err != nil {
		return fmt.Errorf("migrate PostgreSQL: %w", err)
	}
	if err := db.migrateWeb(ctx); err != nil {
		return err
	}
	return nil
}

func (db *PostgreSQL) TouchUser(ctx context.Context, userID int64) error {
	_, err := db.pool.Exec(ctx, `INSERT INTO bot_users(telegram_user_id) VALUES($1) ON CONFLICT (telegram_user_id) DO UPDATE SET last_seen_at=now()`, userID)
	return err
}

func (db *PostgreSQL) SavePoint(ctx context.Context, userID int64, name string, lat, lon float64) error {
	tx, err := db.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `INSERT INTO bot_users(telegram_user_id) VALUES($1) ON CONFLICT (telegram_user_id) DO UPDATE SET last_seen_at=now()`, userID); err != nil {
		return err
	}
	var n int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM saved_points WHERE telegram_user_id=$1`, userID).Scan(&n); err != nil {
		return err
	}
	if n >= 10 {
		return ErrPointLimit
	}
	if _, err = tx.Exec(ctx, `INSERT INTO saved_points(telegram_user_id,name,latitude,longitude) VALUES($1,$2,$3,$4)`, userID, name, lat, lon); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (db *PostgreSQL) Points(ctx context.Context, userID int64) ([]Point, error) {
	rows, err := db.pool.Query(ctx, `SELECT id,name,latitude,longitude FROM saved_points WHERE telegram_user_id=$1 ORDER BY created_at,id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Point
	for rows.Next() {
		var p Point
		if err := rows.Scan(&p.ID, &p.Name, &p.Latitude, &p.Longitude); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (db *PostgreSQL) DeletePoint(ctx context.Context, userID, pointID int64) (bool, error) {
	tag, err := db.pool.Exec(ctx, `DELETE FROM saved_points WHERE telegram_user_id=$1 AND id=$2`, userID, pointID)
	return tag.RowsAffected() == 1, err
}

func (db *PostgreSQL) RecordForecast(ctx context.Context, userID int64, successful bool) error {
	if err := db.TouchUser(ctx, userID); err != nil {
		return err
	}
	successCount, failureCount := 0, 1
	if successful {
		successCount, failureCount = 1, 0
	}
	_, err := db.pool.Exec(ctx, `INSERT INTO forecast_usage_daily(day,telegram_user_id,request_count,successful_count,failed_count) VALUES((now() AT TIME ZONE 'Europe/Moscow')::date,$1,1,$2,$3) ON CONFLICT(day,telegram_user_id) DO UPDATE SET request_count=forecast_usage_daily.request_count+1,successful_count=forecast_usage_daily.successful_count+excluded.successful_count,failed_count=forecast_usage_daily.failed_count+excluded.failed_count`, userID, successCount, failureCount)
	return err
}

func (db *PostgreSQL) Stats(ctx context.Context) (int64, []DailyUsage, error) {
	var users int64
	if err := db.pool.QueryRow(ctx, `SELECT count(*) FROM bot_users`).Scan(&users); err != nil {
		return 0, nil, err
	}
	rows, err := db.pool.Query(ctx, `SELECT d::date,coalesce(sum(f.request_count),0)::bigint,coalesce(sum(f.successful_count),0)::bigint,coalesce(sum(f.failed_count),0)::bigint FROM generate_series((now() AT TIME ZONE 'Europe/Moscow')::date-29,(now() AT TIME ZONE 'Europe/Moscow')::date,'1 day') d LEFT JOIN forecast_usage_daily f ON f.day=d::date GROUP BY d ORDER BY d`)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()
	var days []DailyUsage
	for rows.Next() {
		var d DailyUsage
		if err := rows.Scan(&d.Day, &d.Requests, &d.Successful, &d.Failed); err != nil {
			return 0, nil, err
		}
		days = append(days, d)
	}
	return users, days, rows.Err()
}

// RunMaintenance bounds usage history while preserving the 30-day admin chart.
func (db *PostgreSQL) RunMaintenance(ctx context.Context, logf func(string, ...any)) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	cleanup := func() {
		tag, err := db.pool.Exec(ctx, `DELETE FROM forecast_usage_daily WHERE day < (now() AT TIME ZONE 'Europe/Moscow')::date-90`)
		if err != nil {
			if ctx.Err() == nil {
				logf("PostgreSQL maintenance failed: %v", err)
			}
			return
		}
		if tag.RowsAffected() > 0 {
			logf("PostgreSQL maintenance removed %d old daily usage rows", tag.RowsAffected())
		}
		if err := db.cleanupWeb(ctx, logf); err != nil && ctx.Err() == nil {
			logf("PostgreSQL web maintenance failed: %v", err)
		}
	}
	cleanup()
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cleanup()
		}
	}
}
