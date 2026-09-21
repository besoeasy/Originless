package modules

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"time"

	_ "modernc.org/sqlite"
)

func jsonRaw(s string) json.RawMessage {
	if s == "" {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(s)
}

type Store struct {
	db *sql.DB
}

func NewStore(dbPath string) (*Store, error) {
	sqlDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)

	if err := migrate(sqlDB); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	log.Printf("[db] database ready at %s", dbPath)
	return &Store{db: sqlDB}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func migrate(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS records (
			id          TEXT PRIMARY KEY,
			owner       TEXT NOT NULL,
			collection  TEXT NOT NULL,
			created_at  INTEGER NOT NULL,
			expires_at  INTEGER NOT NULL,
			data        TEXT NOT NULL,
			sig         TEXT NOT NULL,
			size        INTEGER NOT NULL,
			stored_at   DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_records_owner ON records(owner);
		CREATE INDEX IF NOT EXISTS idx_records_collection ON records(collection);
		CREATE INDEX IF NOT EXISTS idx_records_created ON records(created_at);
		CREATE INDEX IF NOT EXISTS idx_records_expires ON records(expires_at);
		CREATE TABLE IF NOT EXISTS record_labels (
			record_id TEXT NOT NULL REFERENCES records(id) ON DELETE CASCADE,
			label     TEXT NOT NULL,
			PRIMARY KEY (record_id, label)
		);
		CREATE INDEX IF NOT EXISTS idx_record_labels_label ON record_labels(label);
		CREATE TABLE IF NOT EXISTS blobs (
			hash         TEXT PRIMARY KEY,
			size         INTEGER NOT NULL,
			created_at   DATETIME DEFAULT CURRENT_TIMESTAMP,
			last_access  DATETIME DEFAULT CURRENT_TIMESTAMP,
			access_count INTEGER NOT NULL DEFAULT 0
		);
		CREATE INDEX IF NOT EXISTS idx_blobs_last_access ON blobs(last_access);
		CREATE INDEX IF NOT EXISTS idx_blobs_created ON blobs(created_at);
	`)
	return err
}

// InsertRecord stores a verified record + labels. Duplicate IDs are idempotent.
func (s *Store) InsertRecord(r *Record) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	res, err := tx.Exec(
		`INSERT OR IGNORE INTO records (id, owner, collection, created_at, expires_at, data, sig, size) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.Owner, r.Collection, r.CreatedAt, r.ExpiresAt, string(r.Data), r.Sig, r.Size,
	)
	if err != nil {
		return false, err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		_ = tx.Commit()
		return false, nil // duplicate — already stored
	}

	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO record_labels (record_id, label) VALUES (?, ?)`)
	if err != nil {
		return false, err
	}
	defer stmt.Close()
	for _, l := range r.Labels {
		if _, err := stmt.Exec(r.ID, l); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) GetRecord(id string) (*Record, error) {
	row := s.db.QueryRow(
		`SELECT id, owner, collection, created_at, expires_at, data, sig, size, stored_at FROM records WHERE id = ?`,
		id,
	)
	r, err := scanRecord(row)
	if err != nil {
		return nil, err
	}
	labels, err := s.getLabels(id)
	if err != nil {
		return nil, err
	}
	r.Labels = labels
	return r, nil
}

func (s *Store) getLabels(id string) ([]string, error) {
	rows, err := s.db.Query(`SELECT label FROM record_labels WHERE record_id = ? ORDER BY label ASC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var l string
		if err := rows.Scan(&l); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	if out == nil {
		out = []string{}
	}
	return out, rows.Err()
}

type RecordFilter struct {
	Owner          string
	Collection     string
	Label          string
	Since          int64
	Until          int64
	Search         string
	Limit          int
	Offset         int
	IncludeExpired bool
	Now            int64
}

// QueryRecords returns newest-first, expired hidden unless IncludeExpired.
func (s *Store) QueryRecords(f RecordFilter) ([]Record, error) {
	q := `SELECT id, owner, collection, created_at, expires_at, data, sig, size, stored_at FROM records WHERE 1=1`
	var args []any
	if !f.IncludeExpired {
		q += ` AND expires_at > ?`
		args = append(args, f.Now)
	}
	if f.Owner != "" {
		q += ` AND owner = ?`
		args = append(args, f.Owner)
	}
	if f.Collection != "" {
		q += ` AND collection = ?`
		args = append(args, f.Collection)
	}
	if f.Label != "" {
		q += ` AND EXISTS (SELECT 1 FROM record_labels WHERE record_id = records.id AND label = ?)`
		args = append(args, f.Label)
	}
	if f.Since > 0 {
		q += ` AND created_at >= ?`
		args = append(args, f.Since)
	}
	if f.Until > 0 {
		q += ` AND created_at <= ?`
		args = append(args, f.Until)
	}
	if f.Search != "" {
		q += ` AND data LIKE ?`
		args = append(args, "%"+f.Search+"%")
	}
	q += ` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`
	args = append(args, f.Limit, f.Offset)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Record
	for rows.Next() {
		var r Record
		var dataStr, storedAt string
		var labels []string
		_ = labels
		if err := rows.Scan(&r.ID, &r.Owner, &r.Collection, &r.CreatedAt, &r.ExpiresAt, &dataStr, &r.Sig, &r.Size, &storedAt); err != nil {
			return nil, err
		}
		r.Data = jsonRaw(dataStr)
		r.StoredAt = storedAt
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		labels, err := s.getLabels(out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Labels = labels
	}
	if out == nil {
		out = []Record{}
	}
	return out, nil
}

func scanRecord(row *sql.Row) (*Record, error) {
	var r Record
	var dataStr, storedAt string
	if err := row.Scan(&r.ID, &r.Owner, &r.Collection, &r.CreatedAt, &r.ExpiresAt, &dataStr, &r.Sig, &r.Size, &storedAt); err != nil {
		return nil, err
	}
	r.Data = jsonRaw(dataStr)
	r.StoredAt = storedAt
	r.Labels = []string{}
	return &r, nil
}

// BlobMeta tracks a content-addressed .bin blob for LRU accounting.
// RetentionSecs/RetainedUntil/Protected are computed from the
// size-weighted retention policy (see retention.go), not stored.
type BlobMeta struct {
	Hash          string    `json:"hash"`
	Size          int64     `json:"size"`
	CreatedAt     time.Time `json:"created_at"`
	LastAccess    time.Time `json:"last_access"`
	AccessCount   int64     `json:"access_count"`
	RetentionSecs int64     `json:"retention_secs"`
	RetainedUntil time.Time `json:"retained_until"`
	Protected     bool      `json:"protected"`
}

// fillBlobRetention populates the computed retention fields of m as of now.
func fillBlobRetention(m *BlobMeta, now time.Time) {
	if m == nil {
		return
	}
	m.RetentionSecs = int64(BlobRetentionForSize(m.Size) / time.Second)
	m.RetainedUntil = BlobRetainedUntil(m.CreatedAt, m.Size)
	m.Protected = now.Before(m.RetainedUntil)
}

// UpsertBlob inserts a new blob row or touches the existing one.
// Returns true when the row was newly created.
func (s *Store) UpsertBlob(hash string, size int64) (bool, error) {
	res, err := s.db.Exec(
		`INSERT OR IGNORE INTO blobs (hash, size) VALUES (?, ?)`,
		hash, size,
	)
	if err != nil {
		return false, err
	}
	affected, _ := res.RowsAffected()
	if affected == 1 {
		return true, nil
	}
	// Dedupe hit: refresh LRU clock, keep original size/created_at.
	_, err = s.db.Exec(
		`UPDATE blobs SET last_access = CURRENT_TIMESTAMP, access_count = access_count + 1 WHERE hash = ?`,
		hash,
	)
	return false, err
}

// GetBlob returns metadata or sql.ErrNoRows when unknown.
func (s *Store) GetBlob(hash string) (*BlobMeta, error) {
	var m BlobMeta
	err := s.db.QueryRow(
		`SELECT hash, size, created_at, last_access, access_count FROM blobs WHERE hash = ?`,
		hash,
	).Scan(&m.Hash, &m.Size, &m.CreatedAt, &m.LastAccess, &m.AccessCount)
	if err != nil {
		return nil, err
	}
	fillBlobRetention(&m, time.Now())
	return &m, nil
}

// TouchBlob bumps a blob for LRU on every successful /down.
func (s *Store) TouchBlob(hash string) error {
	_, err := s.db.Exec(
		`UPDATE blobs SET last_access = CURRENT_TIMESTAMP, access_count = access_count + 1 WHERE hash = ?`,
		hash,
	)
	return err
}

// DeleteBlob removes one accounting row.
func (s *Store) DeleteBlob(hash string) error {
	_, err := s.db.Exec(`DELETE FROM blobs WHERE hash = ?`, hash)
	return err
}

// GetBlobSize sums tracked blob bytes.
func (s *Store) GetBlobSize() (int64, error) {
	var total sql.NullInt64
	if err := s.db.QueryRow(`SELECT SUM(size) FROM blobs`).Scan(&total); err != nil {
		return 0, err
	}
	if total.Valid {
		return total.Int64, nil
	}
	return 0, nil
}

// GetBlobCount counts tracked blobs.
func (s *Store) GetBlobCount() (int64, error) {
	var count int64
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM blobs`).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

// BlobRow is an eviction candidate ordered by LRU.
type BlobRow struct {
	Hash       string
	Size       int64
	CreatedAt  time.Time
	LastAccess time.Time
}

// GetBlobsByLRU returns blobs oldest-access-first for eviction scans.
// Retention filtering happens in the janitor (per-blob, size-weighted),
// so this returns every tracked blob in pages.
func (s *Store) GetBlobsByLRU(limit, offset int) ([]BlobRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.db.Query(
		`SELECT hash, size, created_at, last_access FROM blobs
		 ORDER BY last_access ASC LIMIT ? OFFSET ?`,
		limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BlobRow
	for rows.Next() {
		var b BlobRow
		if err := rows.Scan(&b.Hash, &b.Size, &b.CreatedAt, &b.LastAccess); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// ListBlobHashes returns every tracked hash (for startup reconciliation).
func (s *Store) ListBlobHashes() ([]string, error) {
	rows, err := s.db.Query(`SELECT hash FROM blobs`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// GetRecordCount returns the count of non-expired records.
func (s *Store) GetRecordCount() (int64, error) {
	var count int64
	err := s.db.QueryRow(`SELECT COUNT(*) FROM records WHERE expires_at > ?`, time.Now().Unix()).Scan(&count)
	return count, err
}

// ListBlobs returns newest blobs first.
func (s *Store) ListBlobs(limit, offset int) ([]BlobMeta, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.db.Query(
		`SELECT hash, size, created_at, last_access, access_count FROM blobs
		 ORDER BY created_at DESC LIMIT ? OFFSET ?`,
		limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BlobMeta
	now := time.Now()
	for rows.Next() {
		var m BlobMeta
		if err := rows.Scan(&m.Hash, &m.Size, &m.CreatedAt, &m.LastAccess, &m.AccessCount); err != nil {
			return nil, err
		}
		fillBlobRetention(&m, now)
		out = append(out, m)
	}
	if out == nil {
		out = []BlobMeta{}
	}
	return out, rows.Err()
}
