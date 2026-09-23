package modules

import (
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
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
	db     *sql.DB
	dbPath string

	// blobSize caches SUM(blobs.size) for a short window. The publish
	// path, /status, /metrics and /blobs all read it;
	// a TTL keeps this O(1) instead of scanning the whole blob table.
	blobSizeMu        sync.Mutex
	blobSize          int64
	blobSizeCheckedAt time.Time

	// evState caches the unexpired events commutative XOR state root
	evStateMu        sync.Mutex
	evStateCount     int64
	evStateRoot      string
	evStateCheckedAt time.Time

	// blobState caches the blobs commutative XOR state root
	blobStateMu        sync.Mutex
	blobStateCount     int64
	blobStateRoot      string
	blobStateCheckedAt time.Time
}

const (
	blobSizeCacheTTL  = 2 * time.Second
	stateRootCacheTTL = 3 * time.Second
)

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
	return &Store{db: sqlDB, dbPath: dbPath}, nil
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
			blob_hash   TEXT NOT NULL DEFAULT '',
			sig         TEXT NOT NULL,
			size        INTEGER NOT NULL,
			stored_at   DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_records_owner ON records(owner);
		CREATE INDEX IF NOT EXISTS idx_records_collection ON records(collection);
		CREATE INDEX IF NOT EXISTS idx_records_created ON records(created_at);
		CREATE INDEX IF NOT EXISTS idx_records_expires ON records(expires_at);
		CREATE INDEX IF NOT EXISTS idx_records_blob ON records(blob_hash);
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
		`INSERT OR IGNORE INTO records (id, owner, collection, created_at, expires_at, data, blob_hash, sig, size) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.Owner, r.Collection, r.CreatedAt, r.ExpiresAt, string(r.Data), r.Blob, r.Sig, r.Size,
	)
	if err != nil {
		return false, "", err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		_ = tx.Commit()
		return false, "", nil // duplicate — already stored
	}

	s.evStateMu.Lock()
	s.evStateCheckedAt = time.Time{}
	s.evStateMu.Unlock()

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
		`SELECT id, owner, collection, created_at, expires_at, data, blob_hash, sig, size, stored_at FROM records WHERE id = ?`,
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
	Blob           string
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
	q := `SELECT id, owner, collection, created_at, expires_at, data, blob_hash, sig, size, stored_at FROM records WHERE 1=1`
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
	if f.Blob != "" {
		q += ` AND blob_hash = ?`
		args = append(args, f.Blob)
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
		if err := rows.Scan(&r.ID, &r.Owner, &r.Collection, &r.CreatedAt, &r.ExpiresAt, &dataStr, &r.Blob, &r.Sig, &r.Size, &storedAt); err != nil {
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
	if err := row.Scan(&r.ID, &r.Owner, &r.Collection, &r.CreatedAt, &r.ExpiresAt, &dataStr, &r.Blob, &r.Sig, &r.Size, &storedAt); err != nil {
		return nil, err
	}
	r.Data = jsonRaw(dataStr)
	r.StoredAt = storedAt
	r.Labels = []string{}
	return &r, nil
}

// BlobMeta tracks a content-addressed blob for lifecycle accounting.
// RetentionSecs/RetainedUntil/Protected are computed from the blob's
// referencing records (or orphan grace when unreferenced), not stored.
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

// orphanGrace maps BlobOrphanGraceDays to a duration once.
var orphanGrace = time.Duration(BlobOrphanGraceDays) * 24 * time.Hour

// fillBlobLifecycle populates the computed lifecycle fields of m as of now.
// deadlineUnix is the latest expires_at of a live referencing record (0 when
// the blob is an orphan). A blob is protected while referenced by a live
// record or until orphan grace elapses since upload.
func fillBlobLifecycle(m *BlobMeta, now time.Time, deadlineUnix int64) {
	if m == nil {
		return
	}
	var retained time.Time
	if deadlineUnix > 0 {
		refDeadline := time.Unix(deadlineUnix, 0)
		orphanUntil := m.CreatedAt.Add(orphanGrace)
		if refDeadline.After(orphanUntil) {
			retained = refDeadline
		} else {
			retained = orphanUntil
		}
	} else {
		retained = m.CreatedAt.Add(orphanGrace)
	}
	m.RetainedUntil = retained
	m.Protected = now.Before(retained)
	if secs := int64(retained.Sub(now) / time.Second); secs > 0 {
		m.RetentionSecs = secs
	} else {
		m.RetentionSecs = 0
	}
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
		s.blobStateMu.Lock()
		s.blobStateCheckedAt = time.Time{}
		s.blobStateMu.Unlock()
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
	deadline, err := s.getBlobDeadline(m.Hash, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	fillBlobLifecycle(&m, time.Now(), deadline)
	return &m, nil
}

// TouchBlob bumps a blob for LRU on every successful /blob/{hash} hit.
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
	if err == nil {
		s.blobStateMu.Lock()
		s.blobStateCheckedAt = time.Time{}
		s.blobStateMu.Unlock()
	}
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
// Retention filtering happens in the janitor (reference-driven orphan
// grace), so this returns every tracked blob in pages.
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

// GetReferencedBlobDeadlines returns blob_hash -> max(expires_at) across all
// live (unexpired) records that reference it. The janitor uses this as the
// single source of truth for blob protection: a blob survives while any live
// record pins it. Served by the indexed records.blob_hash column.
func (s *Store) GetReferencedBlobDeadlines(nowUnix int64) (map[string]int64, error) {
	rows, err := s.db.Query(
		`SELECT blob_hash, MAX(expires_at)
		   FROM records
		  WHERE expires_at > ? AND blob_hash != ''
		  GROUP BY blob_hash`,
		nowUnix,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]int64)
	for rows.Next() {
		var h string
		var deadline int64
		if err := rows.Scan(&h, &deadline); err != nil {
			return nil, err
		}
		out[h] = deadline
	}
	return out, rows.Err()
}

// GetReferencedBlobHashes returns the set of blob hashes referenced by at
// least one live record. The janitor exempts these from eviction.
func (s *Store) GetReferencedBlobHashes(nowUnix int64) (map[string]bool, error) {
	deadlines, err := s.GetReferencedBlobDeadlines(nowUnix)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(deadlines))
	for h := range deadlines {
		out[h] = true
	}
	return out, nil
}

// getBlobDeadline returns the max referencing-record expiry for one hash, or 0
// when no live record references it.
func (s *Store) getBlobDeadline(hash string, nowUnix int64) (int64, error) {
	var deadline sql.NullInt64
	if err := s.db.QueryRow(
		`SELECT MAX(expires_at)
		   FROM records
		  WHERE blob_hash = ? AND expires_at > ?`,
		hash, nowUnix,
	).Scan(&deadline); err != nil {
		return 0, err
	}
	if !deadline.Valid {
		return 0, nil
	}
	return deadline.Int64, nil
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

// EventsStateRoot returns the total count and commutative XOR-sum state root of all unexpired records.
func (s *Store) EventsStateRoot() (int64, string, error) {
	s.evStateMu.Lock()
	if time.Since(s.evStateCheckedAt) < stateRootCacheTTL && !s.evStateCheckedAt.IsZero() {
		cnt, rt := s.evStateCount, s.evStateRoot
		s.evStateMu.Unlock()
		return cnt, rt, nil
	}
	s.evStateMu.Unlock()

	rows, err := s.db.Query(`SELECT id FROM records WHERE expires_at > ?`, time.Now().Unix())
	if err != nil {
		return 0, "", err
	}
	defer rows.Close()

	var count int64
	var xor [32]byte
	var buf [32]byte

	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return 0, "", err
		}
		if len(id) == 64 {
			if n, err := hex.Decode(buf[:], []byte(id)); err == nil && n == 32 {
				for i := 0; i < 32; i++ {
					xor[i] ^= buf[i]
				}
				count++
			}
		}
	}
	if err := rows.Err(); err != nil {
		return 0, "", err
	}

	root := hex.EncodeToString(xor[:])

	s.evStateMu.Lock()
	s.evStateCount = count
	s.evStateRoot = root
	s.evStateCheckedAt = time.Now()
	s.evStateMu.Unlock()

	return count, root, nil
}

// BlobsStateRoot returns the total count and commutative XOR-sum state root of all tracked blobs.
func (s *Store) BlobsStateRoot() (int64, string, error) {
	s.blobStateMu.Lock()
	if time.Since(s.blobStateCheckedAt) < stateRootCacheTTL && !s.blobStateCheckedAt.IsZero() {
		cnt, rt := s.blobStateCount, s.blobStateRoot
		s.blobStateMu.Unlock()
		return cnt, rt, nil
	}
	s.blobStateMu.Unlock()

	rows, err := s.db.Query(`SELECT hash FROM blobs`)
	if err != nil {
		return 0, "", err
	}
	defer rows.Close()

	var count int64
	var xor [32]byte
	var buf [32]byte

	for rows.Next() {
		var hash string
		if err := rows.Scan(&hash); err != nil {
			return 0, "", err
		}
		if len(hash) == 64 {
			if n, err := hex.Decode(buf[:], []byte(hash)); err == nil && n == 32 {
				for i := 0; i < 32; i++ {
					xor[i] ^= buf[i]
				}
				count++
			}
		}
	}
	if err := rows.Err(); err != nil {
		return 0, "", err
	}

	root := hex.EncodeToString(xor[:])

	s.blobStateMu.Lock()
	s.blobStateCount = count
	s.blobStateRoot = root
	s.blobStateCheckedAt = time.Now()
	s.blobStateMu.Unlock()

	return count, root, nil
}

// DeleteExpiredRecords removes records with expires_at <= nowUnix.
// record_labels rows cascade via FK (PRAGMA foreign_keys=ON); a defensive
// orphan cleanup runs first for DBs created before the pragma was set.
// Blob linkage needs no cleanup: it lives on records.blob_hash and dies
// with the row. Returns the number of records removed.
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
		s.evStateMu.Lock()
		s.evStateCheckedAt = time.Time{}
		s.evStateMu.Unlock()
	}
	return n, nil
}

// EvictRecord is an event selected for emergency eviction.
type EvictRecord struct {
	ID       string
	Size     int64
	BlobHash string
}

// BiggestExpiredRecords returns expired events largest-first. Deleting the
// biggest dead rows first frees the most bytes per deletion.
func (s *Store) BiggestExpiredRecords(nowUnix int64, limit int) ([]EvictRecord, error) {
	if limit <= 0 {
		limit = 100
	} else if limit > 1000 {
		limit = 1000
	}
	rows, err := s.db.Query(
		`SELECT id, size, blob_hash FROM records
		  WHERE expires_at <= ? ORDER BY size DESC LIMIT ?`,
		nowUnix, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EvictRecord
	for rows.Next() {
		var r EvictRecord
		if err := rows.Scan(&r.ID, &r.Size, &r.BlobHash); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// BiggestBlobs returns tracked blobs largest-first. Callers filter live
// references (GetReferencedBlobHashes) before deleting.
func (s *Store) BiggestBlobs(limit int) ([]BlobRow, error) {
	if limit <= 0 {
		limit = 100
	} else if limit > 1000 {
		limit = 1000
	}
	rows, err := s.db.Query(
		`SELECT hash, size, created_at, last_access FROM blobs
		 ORDER BY size DESC LIMIT ?`,
		limit,
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

// EarliestLiveRecords returns unexpired events, soonest-expiry first with
// biggest-size tiebreak: the last-resort tier destroys the data closest to
// worthless already.
func (s *Store) EarliestLiveRecords(nowUnix int64, limit int) ([]EvictRecord, error) {
	if limit <= 0 {
		limit = 100
	} else if limit > 1000 {
		limit = 1000
	}
	rows, err := s.db.Query(
		`SELECT id, size, blob_hash FROM records
		  WHERE expires_at > ? ORDER BY expires_at ASC, size DESC LIMIT ?`,
		nowUnix, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EvictRecord
	for rows.Next() {
		var r EvictRecord
		if err := rows.Scan(&r.ID, &r.Size, &r.BlobHash); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteRecords removes events by ID. record_labels rows cascade via FK;
// blob linkage lives on records.blob_hash and dies with the row, turning
// solely-referenced blobs into orphans for the sweep. Returns rows removed.
func (s *Store) DeleteRecords(ids []string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	ph := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		ph[i] = "?"
		args[i] = id
	}
	res, err := s.db.Exec(`DELETE FROM records WHERE id IN (`+joinPlaceholders(ph)+`)`, args...)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		_, _ = s.db.Exec(`PRAGMA incremental_vacuum`)
		s.evStateMu.Lock()
		s.evStateCheckedAt = time.Time{}
		s.evStateMu.Unlock()
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
	// Fetch reference deadlines before opening the blob scan: the store
	// runs on a single DB connection (MaxOpenConns=1), so a second query
	// while rows are open would deadlock.
	now := time.Now()
	deadlines, derr := s.GetReferencedBlobDeadlines(now.Unix())
	if derr != nil {
		return nil, derr
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
	for rows.Next() {
		var m BlobMeta
		if err := rows.Scan(&m.Hash, &m.Size, &m.CreatedAt, &m.LastAccess, &m.AccessCount); err != nil {
			return nil, err
		}
		fillBlobLifecycle(&m, now, deadlines[m.Hash])
		out = append(out, m)
	}
	if out == nil {
		out = []BlobMeta{}
	}
	return out, rows.Err()
}

// CollectionStat represents an aggregated count of active records per collection.
type CollectionStat struct {
	Name  string `json:"name"`
	Count int64  `json:"count"`
}

// GetTopCollections returns collections with highest count of unexpired records.
func (s *Store) GetTopCollections(limit int) ([]CollectionStat, error) {
	if s == nil || s.db == nil {
		return []CollectionStat{}, nil
	}
	if limit <= 0 {
		limit = 10
	}
	nowUnix := time.Now().Unix()
	rows, err := s.db.Query(`
		SELECT collection, COUNT(*) as cnt
		FROM records
		WHERE expires_at > ?
		GROUP BY collection
		ORDER BY cnt DESC
		LIMIT ?`, nowUnix, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	stats := make([]CollectionStat, 0)
	for rows.Next() {
		var cs CollectionStat
		if err := rows.Scan(&cs.Name, &cs.Count); err != nil {
			return nil, err
		}
		stats = append(stats, cs)
	}
	return stats, rows.Err()
}

// GetDBFileSize returns the size in bytes of the SQLite database file and WAL on disk.
func (s *Store) GetDBFileSize() int64 {
	if s == nil || s.dbPath == "" {
		return 0
	}
	var total int64
	if fi, err := os.Stat(s.dbPath); err == nil {
		total += fi.Size()
	}
	if fi, err := os.Stat(s.dbPath + "-wal"); err == nil {
		total += fi.Size()
	}
	return total
}
