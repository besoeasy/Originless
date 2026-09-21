package modules

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"sync"
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

	// blobSize caches SUM(blobs.size) for a short window. The publish
	// path, /status, /metrics and /blobs all read it;
	// a TTL keeps this O(1) instead of scanning the whole blob table.
	blobSizeMu        sync.Mutex
	blobSize          int64
	blobSizeCheckedAt time.Time
}

const blobSizeCacheTTL = 2 * time.Second

func NewStore(dbPath string) (*Store, error) {
	sqlDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)

	// Enforce ON DELETE CASCADE for record_labels and bound blocking time.
	if _, err := sqlDB.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("enable foreign_keys: %w", err)
	}
	if _, err := sqlDB.Exec(`PRAGMA busy_timeout = 5000`); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("set busy_timeout: %w", err)
	}
	// Incremental auto-vacuum so expired-record purges reclaim space
	// without a blocking full VACUUM on every janitor tick.
	if _, err := sqlDB.Exec(`PRAGMA auto_vacuum = INCREMENTAL`); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("set auto_vacuum: %w", err)
	}
	// WAL + synchronous=NORMAL replace the default rollback journal with
	// one full fsync per commit by at least two fsyncs (journal + db),
	// which dominates publish latency. WAL keeps readers non-blocking and
	// NORMAL stays crash-safe: recent commits may be lost on power loss but
	// the database never corrupts.
	if _, err := sqlDB.Exec(`PRAGMA journal_mode = WAL`); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("set journal_mode: %w", err)
	}
	if _, err := sqlDB.Exec(`PRAGMA synchronous = NORMAL`); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("set synchronous: %w", err)
	}

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

// InsertRecord stores a verified record + labels. Duplicate IDs are idempotent:
// created=false means the same ID already existed. storedAt is the DB-assigned
// timestamp read inside the same transaction so publishes stay one round trip.
func (s *Store) InsertRecord(r *Record) (created bool, storedAt string, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, "", err
	}
	defer tx.Rollback()

	res, err := tx.Exec(
		`INSERT OR IGNORE INTO records (id, owner, collection, created_at, expires_at, data, sig, size) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.Owner, r.Collection, r.CreatedAt, r.ExpiresAt, string(r.Data), r.Sig, r.Size,
	)
	if err != nil {
		return false, "", err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		_ = tx.Commit()
		return false, "", nil // duplicate — already stored
	}

	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO record_labels (record_id, label) VALUES (?, ?)`)
	if err != nil {
		return false, "", err
	}
	defer stmt.Close()
	for _, l := range r.Labels {
		if _, err := stmt.Exec(r.ID, l); err != nil {
			return false, "", err
		}
	}
	if err := tx.QueryRow(`SELECT stored_at FROM records WHERE id = ?`, r.ID).Scan(&storedAt); err != nil {
		return false, "", err
	}
	if err := tx.Commit(); err != nil {
		return false, "", err
	}
	return true, storedAt, nil
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
	// Keyset pagination: fetch records older than (AfterCreated, AfterID)
	// in DESC (created_at, id) order. When set it is used instead of Offset
	// so deep pages stay O(page) instead of O(page+offset).
	AfterCreated int64
	AfterID      string
	Now          int64
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
	if f.AfterCreated > 0 {
		// Keyset seek on the (created_at, id) DESC composite ordering.
		q += ` AND (created_at < ? OR (created_at = ? AND id < ?))`
		args = append(args, f.AfterCreated, f.AfterCreated, f.AfterID)
	}
	q += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	args = append(args, f.Limit)
	if f.AfterCreated <= 0 {
		q += ` OFFSET ?`
		args = append(args, f.Offset)
	}

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Record
	for rows.Next() {
		var r Record
		var dataStr, storedAt string
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
	if out == nil {
		return []Record{}, nil
	}
	// Batched label fetch: one query for the whole page instead of N+1.
	ids := make([]any, len(out))
	placeholders := make([]string, len(out))
	for i := range out {
		ids[i] = out[i].ID
		placeholders[i] = "?"
	}
	labelRows, err := s.db.Query(
		`SELECT record_id, label FROM record_labels WHERE record_id IN (`+joinPlaceholders(placeholders)+`) ORDER BY record_id ASC, label ASC`,
		ids...,
	)
	if err != nil {
		return nil, err
	}
	defer labelRows.Close()
	byID := make(map[string][]string, len(out))
	for labelRows.Next() {
		var rid, l string
		if err := labelRows.Scan(&rid, &l); err != nil {
			return nil, err
		}
		byID[rid] = append(byID[rid], l)
	}
	if err := labelRows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if ls, ok := byID[out[i].ID]; ok {
			out[i].Labels = ls
		} else {
			out[i].Labels = []string{}
		}
	}
	return out, nil
}

func joinPlaceholders(ph []string) string {
	out := ""
	for i, p := range ph {
		if i > 0 {
			out += ","
		}
		out += p
	}
	return out
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

// GetBlobSize sums tracked blob bytes. The result is cached for a short TTL
// because hot paths (/status, /metrics, /blobs) read it far more often than
// the blob table changes.
func (s *Store) GetBlobSize() (int64, error) {
	s.blobSizeMu.Lock()
	defer s.blobSizeMu.Unlock()
	if time.Since(s.blobSizeCheckedAt) < blobSizeCacheTTL {
		return s.blobSize, nil
	}
	var total sql.NullInt64
	if err := s.db.QueryRow(`SELECT SUM(size) FROM blobs`).Scan(&total); err != nil {
		return 0, err
	}
	if total.Valid {
		s.blobSize = total.Int64
	} else {
		s.blobSize = 0
	}
	s.blobSizeCheckedAt = time.Now()
	return s.blobSize, nil
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

// GetReferencedBlobHashes returns the set of blob hashes linked via
// "_blob" from live (unexpired) records. The janitor exempts these from
// eviction so a stored record never dangles off an evicted blob.
func (s *Store) GetReferencedBlobHashes(nowUnix int64) (map[string]bool, error) {
	rows, err := s.db.Query(`SELECT data FROM records WHERE expires_at > ?`, nowUnix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]bool)
	for rows.Next() {
		var dataStr string
		if err := rows.Scan(&dataStr); err != nil {
			return nil, err
		}
		if h, err := RecordBlobHash(json.RawMessage(dataStr)); err == nil && h != "" {
			out[h] = true
		}
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

// ListRecordIDs returns event IDs from SQLite (unexpired only or all).
func (s *Store) ListRecordIDs(unexpiredOnly bool) ([]string, error) {
	q := `SELECT id FROM records`
	var args []any
	if unexpiredOnly {
		q += ` WHERE expires_at > ?`
		args = append(args, time.Now().Unix())
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// DeleteExpiredRecords removes records with expires_at <= nowUnix.
// record_labels rows cascade via FK (PRAGMA foreign_keys=ON); a defensive
// orphan cleanup runs first for DBs created before the pragma was set.
// Returns the number of records removed.
func (s *Store) DeleteExpiredRecords(nowUnix int64) (int64, error) {
	// Defensive: drop orphan labels left by pre-FK databases.
	if _, err := s.db.Exec(`DELETE FROM record_labels WHERE record_id IN (SELECT record_id FROM record_labels LEFT JOIN records ON records.id = record_labels.record_id WHERE records.id IS NULL)`); err != nil {
		return 0, err
	}
	res, err := s.db.Exec(`DELETE FROM records WHERE expires_at <= ?`, nowUnix)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		// Reclaim freelist pages without a blocking full VACUUM.
		_, _ = s.db.Exec(`PRAGMA incremental_vacuum`)
	}
	return n, nil
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
