package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"bot_astrosferum/internal/app/astroweb"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func OpenExisting(ctx context.Context, host string, port int, name, user, password string, maxConns int32) (*PostgreSQL, error) {
	cfg, err := pgxpool.ParseConfig("")
	if err != nil {
		return nil, err
	}
	cfg.ConnConfig.Host, cfg.ConnConfig.Port = host, uint16(port)
	cfg.ConnConfig.Database, cfg.ConnConfig.User, cfg.ConnConfig.Password = name, user, password
	cfg.MaxConns = maxConns
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create existing PostgreSQL pool: %w", err)
	}
	database := &PostgreSQL{pool: pool}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect existing PostgreSQL: %w", err)
	}
	return database, nil
}

func (db *PostgreSQL) migrateWeb(ctx context.Context) error {
	_, err := db.pool.Exec(ctx, `
ALTER TABLE bot_users ADD COLUMN IF NOT EXISTS preferred_web_language varchar(2);
DO $migration$
BEGIN
 IF NOT EXISTS (
  SELECT 1 FROM pg_constraint
  WHERE conname='bot_users_preferred_web_language_check'
   AND conrelid='bot_users'::regclass
 ) THEN
  ALTER TABLE bot_users ADD CONSTRAINT bot_users_preferred_web_language_check
   CHECK (preferred_web_language IN ('ru','en'));
 END IF;
END
$migration$;
CREATE TABLE IF NOT EXISTS web_auth_transactions (
 handle_hash bytea PRIMARY KEY CHECK (octet_length(handle_hash)=32),
 state_hash bytea NOT NULL UNIQUE CHECK (octet_length(state_hash)=32),
 pkce_verifier text NOT NULL,
 nonce text NOT NULL,
 language varchar(2) NOT NULL CHECK (language IN ('ru','en')),
 expires_at timestamptz NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS web_auth_transactions_expires ON web_auth_transactions(expires_at);
CREATE TABLE IF NOT EXISTS web_sessions (
 token_hash bytea PRIMARY KEY CHECK (octet_length(token_hash)=32),
 telegram_user_id bigint NOT NULL REFERENCES bot_users(telegram_user_id) ON DELETE CASCADE,
 oidc_issuer text NOT NULL,
 oidc_subject text NOT NULL,
 language varchar(2) NOT NULL CHECK (language IN ('ru','en')),
 created_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL,
 revoked_at timestamptz,
 last_seen_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE web_sessions ADD COLUMN IF NOT EXISTS oidc_issuer text;
ALTER TABLE web_sessions ADD COLUMN IF NOT EXISTS oidc_subject text;
DELETE FROM web_sessions WHERE oidc_issuer IS NULL OR oidc_subject IS NULL;
ALTER TABLE web_sessions ALTER COLUMN oidc_issuer SET NOT NULL;
ALTER TABLE web_sessions ALTER COLUMN oidc_subject SET NOT NULL;
CREATE INDEX IF NOT EXISTS web_sessions_user ON web_sessions(telegram_user_id, expires_at);
CREATE INDEX IF NOT EXISTS web_sessions_oidc_identity ON web_sessions(oidc_issuer,oidc_subject);
CREATE INDEX IF NOT EXISTS web_sessions_expiry ON web_sessions(expires_at) WHERE revoked_at IS NULL;
CREATE TABLE IF NOT EXISTS web_astrodome_visualizations (
 id varchar(36) PRIMARY KEY,
 telegram_user_id bigint NOT NULL REFERENCES bot_users(telegram_user_id) ON DELETE CASCADE,
	 coordinate_key char(32) NOT NULL,
	 point_name varchar(64) NOT NULL DEFAULT '',
	 pending_point_name varchar(64),
 latitude double precision NOT NULL CHECK (latitude BETWEEN -90 AND 90),
 longitude double precision NOT NULL CHECK (longitude BETWEEN -180 AND 180),
 pending_job_id varchar(128),
 pending_created_at timestamptz,
 source_job_id varchar(128),
 dataset_file varchar(64),
 dataset_bytes bigint CHECK (dataset_bytes > 0),
 dataset_sha256 char(64),
 etag varchar(80),
 provider varchar(32),
 run_id varchar(32),
 grid_profile varchar(64),
 geometry_digest varchar(96),
 generated_at timestamptz,
 expires_at timestamptz,
 admin_fixture boolean NOT NULL DEFAULT false,
 created_at timestamptz NOT NULL DEFAULT now(),
 CHECK ((pending_job_id IS NULL)=(pending_created_at IS NULL)),
 CHECK (
  (dataset_file IS NULL AND dataset_bytes IS NULL AND dataset_sha256 IS NULL AND etag IS NULL AND
   source_job_id IS NULL AND provider IS NULL AND run_id IS NULL AND grid_profile IS NULL AND
   geometry_digest IS NULL AND generated_at IS NULL AND expires_at IS NULL)
  OR
  (dataset_file IS NOT NULL AND dataset_bytes IS NOT NULL AND dataset_sha256 IS NOT NULL AND etag IS NOT NULL AND
   source_job_id IS NOT NULL AND provider IS NOT NULL AND run_id IS NOT NULL AND grid_profile IS NOT NULL AND
   geometry_digest IS NOT NULL AND generated_at IS NOT NULL AND expires_at IS NOT NULL)
	 )
);
ALTER TABLE web_astrodome_visualizations ADD COLUMN IF NOT EXISTS pending_point_name varchar(64);
ALTER TABLE web_astrodome_visualizations ADD COLUMN IF NOT EXISTS admin_fixture boolean NOT NULL DEFAULT false;
ALTER TABLE web_astrodome_visualizations DROP CONSTRAINT IF EXISTS web_astrodome_visualizations_telegram_user_id_coordinate_ke_key;
CREATE UNIQUE INDEX IF NOT EXISTS web_astrodome_visualizations_owner_coordinate
 ON web_astrodome_visualizations(telegram_user_id,coordinate_key) WHERE NOT admin_fixture;
CREATE UNIQUE INDEX IF NOT EXISTS web_astrodome_visualizations_one_admin_fixture
 ON web_astrodome_visualizations(admin_fixture) WHERE admin_fixture;
CREATE UNIQUE INDEX IF NOT EXISTS web_astrodome_visualizations_pending_job
 ON web_astrodome_visualizations(telegram_user_id,pending_job_id) WHERE pending_job_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS web_astrodome_visualizations_source_job
 ON web_astrodome_visualizations(telegram_user_id,source_job_id) WHERE source_job_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS web_astrodome_visualizations_expiry
 ON web_astrodome_visualizations(expires_at) WHERE dataset_file IS NOT NULL;`)
	if err != nil {
		return fmt.Errorf("migrate PostgreSQL web schema: %w", err)
	}
	return nil
}

func (db *PostgreSQL) CreateAuthTransaction(ctx context.Context, transaction astroweb.AuthTransaction) error {
	_, err := db.pool.Exec(ctx, `
INSERT INTO web_auth_transactions(handle_hash,state_hash,pkce_verifier,nonce,language,expires_at)
VALUES($1,$2,$3,$4,$5,$6)`, transaction.HandleHash[:], transaction.StateHash[:], transaction.PKCEVerifier,
		transaction.Nonce, transaction.Language, transaction.ExpiresAt)
	return err
}

func (db *PostgreSQL) ConsumeAuthTransaction(ctx context.Context, handleHash, stateHash [sha256.Size]byte, now time.Time) (astroweb.AuthTransaction, error) {
	var transaction astroweb.AuthTransaction
	var storedHandle, storedState []byte
	err := db.pool.QueryRow(ctx, `
DELETE FROM web_auth_transactions
WHERE handle_hash=$1 AND state_hash=$2 AND expires_at>$3
RETURNING handle_hash,state_hash,pkce_verifier,nonce,language,expires_at`, handleHash[:], stateHash[:], now).
		Scan(&storedHandle, &storedState, &transaction.PKCEVerifier, &transaction.Nonce, &transaction.Language, &transaction.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return astroweb.AuthTransaction{}, astroweb.ErrAuthTransactionStale
	}
	if err != nil {
		return astroweb.AuthTransaction{}, err
	}
	if len(storedHandle) != sha256.Size || len(storedState) != sha256.Size {
		return astroweb.AuthTransaction{}, errors.New("invalid authentication digest stored in PostgreSQL")
	}
	copy(transaction.HandleHash[:], storedHandle)
	copy(transaction.StateHash[:], storedState)
	return transaction, nil
}

func (db *PostgreSQL) CreateWebSession(ctx context.Context, session astroweb.WebSession) error {
	tx, err := db.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
INSERT INTO bot_users(telegram_user_id,preferred_web_language) VALUES($1,$2)
ON CONFLICT (telegram_user_id) DO UPDATE
SET last_seen_at=now(),preferred_web_language=excluded.preferred_web_language`, session.TelegramUserID, session.Language); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO web_sessions(token_hash,telegram_user_id,oidc_issuer,oidc_subject,language,created_at,expires_at)
VALUES($1,$2,$3,$4,$5,$6,$7)`, session.TokenHash[:], session.TelegramUserID, session.OIDCIssuer, session.OIDCSubject,
		session.Language, session.CreatedAt, session.ExpiresAt); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (db *PostgreSQL) WebSession(ctx context.Context, tokenHash [sha256.Size]byte, now time.Time) (astroweb.WebSession, error) {
	var session astroweb.WebSession
	var storedHash []byte
	err := db.pool.QueryRow(ctx, `
SELECT sessions.token_hash,sessions.telegram_user_id,sessions.oidc_issuer,sessions.oidc_subject,
 coalesce(users.preferred_web_language,sessions.language),sessions.created_at,sessions.expires_at
FROM web_sessions AS sessions
JOIN bot_users AS users ON users.telegram_user_id=sessions.telegram_user_id
WHERE sessions.token_hash=$1 AND sessions.revoked_at IS NULL AND sessions.expires_at>$2`, tokenHash[:], now).
		Scan(&storedHash, &session.TelegramUserID, &session.OIDCIssuer, &session.OIDCSubject, &session.Language, &session.CreatedAt, &session.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return astroweb.WebSession{}, astroweb.ErrUnauthenticated
	}
	if err != nil {
		return astroweb.WebSession{}, err
	}
	if len(storedHash) != sha256.Size {
		return astroweb.WebSession{}, errors.New("invalid session digest stored in PostgreSQL")
	}
	copy(session.TokenHash[:], storedHash)
	return session, nil
}

func (db *PostgreSQL) UpdateWebSessionLanguage(
	ctx context.Context,
	tokenHash [sha256.Size]byte,
	userID int64,
	language string,
) error {
	tx, err := db.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := tx.Exec(ctx, `
UPDATE bot_users AS users
SET preferred_web_language=$3,last_seen_at=now()
WHERE users.telegram_user_id=$2
 AND EXISTS (
  SELECT 1 FROM web_sessions AS sessions
  WHERE sessions.token_hash=$1 AND sessions.telegram_user_id=$2
   AND sessions.revoked_at IS NULL AND sessions.expires_at>now()
 )`, tokenHash[:], userID, language)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return astroweb.ErrUnauthenticated
	}
	if _, err := tx.Exec(ctx, `
UPDATE web_sessions SET language=$2
WHERE telegram_user_id=$1 AND revoked_at IS NULL AND expires_at>now()`, userID, language); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (db *PostgreSQL) RevokeWebSession(ctx context.Context, tokenHash [sha256.Size]byte) error {
	_, err := db.pool.Exec(ctx, `UPDATE web_sessions SET revoked_at=coalesce(revoked_at,now()) WHERE token_hash=$1`, tokenHash[:])
	return err
}

func (db *PostgreSQL) WebPoints(ctx context.Context, userID int64) ([]astroweb.SavedPoint, error) {
	points, err := db.Points(ctx, userID)
	if err != nil {
		return nil, err
	}
	result := make([]astroweb.SavedPoint, len(points))
	for index, point := range points {
		result[index] = astroweb.SavedPoint{
			ID: point.ID, Name: point.Name, Latitude: point.Latitude, Longitude: point.Longitude,
		}
	}
	return result, nil
}

func (db *PostgreSQL) WebPoint(ctx context.Context, userID, pointID int64) (astroweb.SavedPoint, error) {
	var point astroweb.SavedPoint
	err := db.pool.QueryRow(ctx, `
SELECT id,name,latitude,longitude
FROM saved_points
WHERE telegram_user_id=$1 AND id=$2`, userID, pointID).
		Scan(&point.ID, &point.Name, &point.Latitude, &point.Longitude)
	if errors.Is(err, pgx.ErrNoRows) {
		return astroweb.SavedPoint{}, astroweb.ErrJobNotFound
	}
	return point, err
}

func (db *PostgreSQL) BeginVisualization(ctx context.Context, pending astroweb.PendingVisualization) error {
	_, err := db.pool.Exec(ctx, `
INSERT INTO web_astrodome_visualizations(
 id,telegram_user_id,coordinate_key,point_name,pending_point_name,latitude,longitude,pending_job_id,pending_created_at)
VALUES($1,$2,$3,$4,$4,$5,$6,$7,$8)
ON CONFLICT(telegram_user_id,coordinate_key) WHERE NOT admin_fixture DO UPDATE SET
	point_name=CASE WHEN web_astrodome_visualizations.dataset_file IS NULL THEN excluded.point_name ELSE web_astrodome_visualizations.point_name END,
	pending_point_name=excluded.pending_point_name,latitude=excluded.latitude,longitude=excluded.longitude,
 pending_job_id=excluded.pending_job_id,pending_created_at=excluded.pending_created_at`,
		pending.ID, pending.TelegramUserID, pending.CoordinateKey, pending.Name, pending.Latitude,
		pending.Longitude, pending.JobID, pending.CreatedAt)
	return err
}

func (db *PostgreSQL) PendingVisualization(ctx context.Context, userID int64, jobID string) (astroweb.PendingVisualization, error) {
	var pending astroweb.PendingVisualization
	err := db.pool.QueryRow(ctx, `
SELECT id,telegram_user_id,coordinate_key,coalesce(pending_point_name,point_name),latitude,longitude,pending_job_id,pending_created_at
FROM web_astrodome_visualizations
WHERE telegram_user_id=$1 AND pending_job_id=$2`, userID, jobID).
		Scan(&pending.ID, &pending.TelegramUserID, &pending.CoordinateKey, &pending.Name, &pending.Latitude,
			&pending.Longitude, &pending.JobID, &pending.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return astroweb.PendingVisualization{}, astroweb.ErrVisualizationNotFound
	}
	return pending, err
}

func (db *PostgreSQL) PendingVisualizations(ctx context.Context, limit int) ([]astroweb.PendingVisualization, error) {
	if limit < 1 || limit > 256 {
		return nil, errors.New("pending visualization query limit is invalid")
	}
	rows, err := db.pool.Query(ctx, `
SELECT id,telegram_user_id,coordinate_key,coalesce(pending_point_name,point_name),latitude,longitude,pending_job_id,pending_created_at
FROM web_astrodome_visualizations
WHERE pending_job_id IS NOT NULL
ORDER BY pending_created_at,id
LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	pending := make([]astroweb.PendingVisualization, 0, limit)
	for rows.Next() {
		var item astroweb.PendingVisualization
		if err := rows.Scan(&item.ID, &item.TelegramUserID, &item.CoordinateKey, &item.Name,
			&item.Latitude, &item.Longitude, &item.JobID, &item.CreatedAt); err != nil {
			return nil, err
		}
		pending = append(pending, item)
	}
	return pending, rows.Err()
}

func (db *PostgreSQL) CompleteVisualization(ctx context.Context, userID int64, completed astroweb.CompletedVisualization) (bool, string, error) {
	tx, err := db.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var previous *string
	err = tx.QueryRow(ctx, `
SELECT dataset_file FROM web_astrodome_visualizations
WHERE telegram_user_id=$1 AND pending_job_id=$2
FOR UPDATE`, userID, completed.JobID).Scan(&previous)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, "", nil
	}
	if err != nil {
		return false, "", err
	}
	tag, err := tx.Exec(ctx, `
UPDATE web_astrodome_visualizations SET
	point_name=coalesce(pending_point_name,point_name),pending_point_name=NULL,
 source_job_id=$3,dataset_file=$4,dataset_bytes=$5,dataset_sha256=$6,etag=$7,
 provider=$8,run_id=$9,grid_profile=$10,geometry_digest=$11,generated_at=$12,expires_at=$13,
 pending_job_id=NULL,pending_created_at=NULL
WHERE telegram_user_id=$1 AND pending_job_id=$2`, userID, completed.JobID, completed.JobID, completed.DatasetFile,
		completed.DatasetBytes, completed.DatasetSHA256, completed.ETag, completed.Provider, completed.RunID,
		completed.GridProfile, completed.GeometryDigest, completed.GeneratedAt, completed.ExpiresAt)
	if err != nil {
		return false, "", err
	}
	if tag.RowsAffected() != 1 {
		return false, "", nil
	}
	if err := tx.Commit(ctx); err != nil {
		return false, "", err
	}
	if previous == nil {
		return true, "", nil
	}
	return true, *previous, nil
}

func (db *PostgreSQL) AdminFixture(ctx context.Context, coordinateKey string) (astroweb.AstrodomeVisualization, error) {
	return db.scanVisualization(db.pool.QueryRow(ctx, `
SELECT id,point_name,latitude,longitude,provider,run_id,grid_profile,geometry_digest,dataset_bytes,
 generated_at,expires_at,admin_fixture,source_job_id,dataset_file,dataset_sha256,etag
FROM web_astrodome_visualizations
WHERE coordinate_key=$1 AND admin_fixture AND dataset_file IS NOT NULL
ORDER BY id
LIMIT 1`, coordinateKey))
}

// ReplaceAdminFixture performs one compare-and-swap of the shared permanent
// row. The catalogue has already compared the exact unavailable-node fraction
// in both immutable files; matching the previous file identity prevents a
// concurrent completion from overwriting a better winner.
func (db *PostgreSQL) ReplaceAdminFixture(
	ctx context.Context,
	coordinateKey string,
	expected astroweb.AstrodomeVisualization,
	replacement astroweb.CompletedVisualization,
) (bool, error) {
	tag, err := db.pool.Exec(ctx, `
UPDATE web_astrodome_visualizations SET
 dataset_file=$6,dataset_bytes=$7,dataset_sha256=$8,etag=$9,
 provider=$10,run_id=$11,grid_profile=$12,geometry_digest=$13,generated_at=$14
WHERE id=$1 AND coordinate_key=$2 AND admin_fixture AND dataset_file=$3 AND dataset_sha256=$4 AND etag=$5`,
		expected.ID, coordinateKey, expected.DatasetFile, expected.DatasetSHA256, expected.ETag,
		replacement.DatasetFile, replacement.DatasetBytes, replacement.DatasetSHA256, replacement.ETag,
		replacement.Provider, replacement.RunID, replacement.GridProfile, replacement.GeometryDigest,
		replacement.GeneratedAt,
	)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (db *PostgreSQL) AbandonVisualization(ctx context.Context, userID int64, jobID string) error {
	tx, err := db.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// A dataset-less admission can be removed directly while the job identity
	// still scopes the row. Do not run a user-wide cleanup here: another point
	// owned by the same user may have an unrelated pending calculation.
	if _, err := tx.Exec(ctx, `
DELETE FROM web_astrodome_visualizations
WHERE telegram_user_id=$1 AND pending_job_id=$2 AND dataset_file IS NULL`, userID, jobID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
UPDATE web_astrodome_visualizations SET pending_job_id=NULL,pending_created_at=NULL,pending_point_name=NULL
WHERE telegram_user_id=$1 AND pending_job_id=$2`, userID, jobID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (db *PostgreSQL) Visualizations(ctx context.Context, userID int64, now time.Time, includeAdminFixtures bool) ([]astroweb.AstrodomeVisualization, error) {
	rows, err := db.pool.Query(ctx, `
SELECT id,point_name,latitude,longitude,provider,run_id,grid_profile,geometry_digest,dataset_bytes,
 generated_at,expires_at,admin_fixture,source_job_id,dataset_file,dataset_sha256,etag
FROM web_astrodome_visualizations
WHERE dataset_file IS NOT NULL AND
	 ((telegram_user_id=$1 AND expires_at>$2 AND NOT admin_fixture) OR ($3 AND admin_fixture))
ORDER BY admin_fixture DESC,generated_at DESC,id`, userID, now, includeAdminFixtures)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	visualizations := make([]astroweb.AstrodomeVisualization, 0)
	for rows.Next() {
		var visualization astroweb.AstrodomeVisualization
		if err := rows.Scan(
			&visualization.ID, &visualization.Name, &visualization.Latitude, &visualization.Longitude,
			&visualization.Provider, &visualization.RunID, &visualization.GridProfile, &visualization.GeometryDigest,
			&visualization.DatasetBytes, &visualization.GeneratedAt, &visualization.ExpiresAt, &visualization.AdminFixture,
			&visualization.SourceJobID, &visualization.DatasetFile, &visualization.DatasetSHA256, &visualization.ETag,
		); err != nil {
			return nil, err
		}
		visualizations = append(visualizations, visualization)
	}
	return visualizations, rows.Err()
}

func (db *PostgreSQL) Visualization(ctx context.Context, userID int64, id string, now time.Time, includeAdminFixtures bool) (astroweb.AstrodomeVisualization, error) {
	return db.scanVisualization(db.pool.QueryRow(ctx, `
SELECT id,point_name,latitude,longitude,provider,run_id,grid_profile,geometry_digest,dataset_bytes,
 generated_at,expires_at,admin_fixture,source_job_id,dataset_file,dataset_sha256,etag
FROM web_astrodome_visualizations
WHERE id=$2 AND dataset_file IS NOT NULL AND
	 ((telegram_user_id=$1 AND expires_at>$3 AND NOT admin_fixture) OR ($4 AND admin_fixture))`, userID, id, now, includeAdminFixtures))
}

func (db *PostgreSQL) VisualizationByJob(ctx context.Context, userID int64, jobID string, now time.Time, includeAdminFixtures bool) (astroweb.AstrodomeVisualization, error) {
	return db.scanVisualization(db.pool.QueryRow(ctx, `
SELECT id,point_name,latitude,longitude,provider,run_id,grid_profile,geometry_digest,dataset_bytes,
	 generated_at,expires_at,admin_fixture,source_job_id,dataset_file,dataset_sha256,etag
FROM web_astrodome_visualizations
WHERE source_job_id=$2 AND dataset_file IS NOT NULL AND
	 ((telegram_user_id=$1 AND expires_at>$3 AND NOT admin_fixture) OR ($4 AND admin_fixture))`,
		userID, jobID, now, includeAdminFixtures))
}

func (db *PostgreSQL) scanVisualization(row pgx.Row) (astroweb.AstrodomeVisualization, error) {
	var visualization astroweb.AstrodomeVisualization
	err := row.Scan(
		&visualization.ID, &visualization.Name, &visualization.Latitude, &visualization.Longitude,
		&visualization.Provider, &visualization.RunID, &visualization.GridProfile, &visualization.GeometryDigest,
		&visualization.DatasetBytes, &visualization.GeneratedAt, &visualization.ExpiresAt, &visualization.AdminFixture,
		&visualization.SourceJobID, &visualization.DatasetFile, &visualization.DatasetSHA256, &visualization.ETag,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return astroweb.AstrodomeVisualization{}, astroweb.ErrVisualizationNotFound
	}
	return visualization, err
}

func (db *PostgreSQL) DeleteExpiredVisualizations(ctx context.Context, now time.Time) ([]string, error) {
	tx, err := db.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
SELECT id,dataset_file,pending_job_id IS NOT NULL
FROM web_astrodome_visualizations
WHERE dataset_file IS NOT NULL AND expires_at<=$1 AND NOT admin_fixture
FOR UPDATE`, now)
	if err != nil {
		return nil, err
	}
	type expiredRow struct {
		id      string
		file    string
		pending bool
	}
	var expired []expiredRow
	for rows.Next() {
		var row expiredRow
		if err := rows.Scan(&row.id, &row.file, &row.pending); err != nil {
			rows.Close()
			return nil, err
		}
		expired = append(expired, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	files := make([]string, 0, len(expired))
	for _, row := range expired {
		files = append(files, row.file)
		if row.pending {
			_, err = tx.Exec(ctx, `UPDATE web_astrodome_visualizations SET
 source_job_id=NULL,dataset_file=NULL,dataset_bytes=NULL,dataset_sha256=NULL,etag=NULL,
 provider=NULL,run_id=NULL,grid_profile=NULL,geometry_digest=NULL,generated_at=NULL,expires_at=NULL
WHERE id=$1`, row.id)
		} else {
			_, err = tx.Exec(ctx, `DELETE FROM web_astrodome_visualizations WHERE id=$1`, row.id)
		}
		if err != nil {
			return nil, err
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM web_astrodome_visualizations WHERE dataset_file IS NULL AND pending_job_id IS NULL`); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return files, nil
}

func (db *PostgreSQL) ReferencedVisualizationFiles(ctx context.Context) ([]string, error) {
	rows, err := db.pool.Query(ctx, `SELECT dataset_file FROM web_astrodome_visualizations WHERE dataset_file IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var files []string
	for rows.Next() {
		var file string
		if err := rows.Scan(&file); err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, rows.Err()
}

func (db *PostgreSQL) Ready(ctx context.Context) error {
	if err := db.pool.Ping(ctx); err != nil {
		return err
	}
	// LIMIT 0 still forces PostgreSQL to resolve every required relation and
	// authorize the dedicated web role without reading user data.
	_, err := db.pool.Exec(ctx, `
SELECT 1
FROM web_auth_transactions AS auth
CROSS JOIN web_sessions AS sessions
CROSS JOIN web_astrodome_visualizations AS visualizations
CROSS JOIN saved_points AS points
CROSS JOIN bot_users AS users
LIMIT 0`)
	return err
}

func (db *PostgreSQL) cleanupWeb(ctx context.Context, logf func(string, ...any)) error {
	transactions, err := db.pool.Exec(ctx, `DELETE FROM web_auth_transactions WHERE expires_at<now()-interval '1 hour'`)
	if err != nil {
		return err
	}
	sessions, err := db.pool.Exec(ctx, `DELETE FROM web_sessions WHERE expires_at<now()-interval '7 days' OR revoked_at<now()-interval '7 days'`)
	if err != nil {
		return err
	}
	if transactions.RowsAffected()+sessions.RowsAffected() > 0 {
		logf("PostgreSQL web maintenance removed %d auth transactions and %d sessions", transactions.RowsAffected(), sessions.RowsAffected())
	}
	return nil
}

var _ astroweb.AuthStore = (*PostgreSQL)(nil)
var _ astroweb.PointStore = (*PostgreSQL)(nil)
var _ astroweb.VisualizationMetadataStore = (*PostgreSQL)(nil)
var _ astroweb.Readiness = (*PostgreSQL)(nil)
