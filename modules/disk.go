package modules

import (
	"errors"
	"io"
	"strings"
	"sync"
	"syscall"

	sqlite "modernc.org/sqlite"
)

// sqliteFullCode is SQLITE_FULL (13): SQLite's "database or disk is full".
// Compared against sqlite.Error.Code(); extended IOERR codes are
// deliberately NOT treated as disk-full (a sick disk must not trigger
// mass eviction).
const sqliteFullCode = 13

// Disk tuning. All values are hardcoded to keep operation simple and predictable.
const (
	// DiskCeilingPct is the hard admission ceiling: this node's own writes
	// never let disk usage cross it. Breaching writes trigger one emergency
	// eviction pass, then 507 Insufficient Storage.
	DiskCeilingPct = 90
	// DiskSoftPct triggers opportunistic janitor sweeps before pressure:
	// expired records and eligible orphans are purged early, before any pressure.
	DiskSoftPct = 85
	// MaxBlobBytes caps a single blob upload. Besides abuse control this
	// kills the single-write overshoot adversary: no one request can jump
	// the ceiling in one streaming write.
	MaxBlobBytes = 1 << 30
	// EmergencyMaxItems caps deletions per emergency pass.
	EmergencyMaxItems = 1000
	// EmergencyCooldownSecs is the minimum gap between emergency passes —
	// concurrent failing writers back off instead of stampeding.
	EmergencyCooldownSecs = 5 * 60
	// EmergencyIncludeLive allows the last-resort tier: when expired records
	// and orphans didn't free enough, evict unexpired events (soonest-expiry first)
	// and their orphaned blobs.
	EmergencyIncludeLive = true
	// EmergencyTargetFreePct stops a pass early once this much disk is free.
	EmergencyTargetFreePct = 5
)

// DiskStats is a point-in-time view of the filesystem backing dataDir.
type DiskStats struct {
	Path       string  `json:"path"`
	TotalBytes int64   `json:"total_bytes"`
	FreeBytes  int64   `json:"free_bytes"`
	UsedPct    float64 `json:"used_pct"`
	InodesFree int64   `json:"inodes_free"`
}

// StatDisk reports space on the filesystem containing path. Both byte and
// inode counts matter: a content-addressed store can exhaust inodes while
// bytes remain.
func StatDisk(path string) (DiskStats, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return DiskStats{}, err
	}
	total := int64(st.Blocks) * int64(st.Bsize)
	free := int64(st.Bavail) * int64(st.Bsize)
	usedPct := 0.0
	if total > 0 {
		usedPct = 100.0 * float64(total-free) / float64(total)
	}
	return DiskStats{
		Path:       path,
		TotalBytes: total,
		FreeBytes:  free,
		UsedPct:    usedPct,
		InodesFree: int64(st.Ffree),
	}, nil
}

// sqliteCoder is satisfied by modernc.org/sqlite.Error (Code() int) and any
// test double. database/sql returns driver errors unwrapped, so errors.As
// finds it through the call chain.
type sqliteCoder interface{ Code() int }

// IsDiskFull reports whether err is a disk-full signal. It is deliberately
// narrow: ENOSPC from the OS, SQLITE_FULL from the driver, or an explicit
// no-space message. Anything else (corruption, permissions, readonly
// mounts, generic IOERR) must NEVER trigger eviction.
func IsDiskFull(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.ENOSPC) {
		return true
	}
	var sc sqliteCoder
	if errors.As(err, &sc) && sc.Code() == sqliteFullCode {
		return true
	}
	// Reference the driver type so a future driver swap fails loudly at
	// compile time if Error/Code semantics change.
	_ = (*sqlite.Error)(nil)
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no space left on device") ||
		strings.Contains(msg, "disk full") ||
		strings.Contains(msg, "database or disk is full")
}

// errDiskCapExceeded aborts streaming staging past MAX_BLOB_BYTES.
var errDiskCapExceeded = errors.New("blob exceeds MAX_BLOB_BYTES")

// errDiskCeilingExceeded aborts streaming staging that would cross the
// admission ceiling mid-write (the overshoot adversary for unknown sizes).
var errDiskCeilingExceeded = errors.New("disk ceiling exceeded")

// IsStorageExhausted covers every signal that must trigger the emergency
// path: kernel/drive full plus our own ceiling aborts.
func IsStorageExhausted(err error) bool {
	return IsDiskFull(err) || errors.Is(err, errDiskCeilingExceeded)
}

// ceilingCheckBytes is the streaming granularity for ceiling re-checks.
const ceilingCheckBytes = 8 << 20

// ceilingWriter wraps a staging file: it enforces the per-blob cap and
// re-checks free space every few megabytes so an unknown-size stream can
// never cross the ceiling mid-write. A nil guard skips ceiling checks
// (cap still enforced).
type ceilingWriter struct {
	w          io.Writer
	g          *DiskGuard
	maxBytes   int64
	written    int64
	sinceCheck int64
}

func (c *ceilingWriter) Write(p []byte) (int, error) {
	if c.maxBytes > 0 && c.written+int64(len(p)) > c.maxBytes {
		return 0, errDiskCapExceeded
	}
	n, err := c.w.Write(p)
	c.written += int64(n)
	c.sinceCheck += int64(n)
	if err == nil && c.sinceCheck >= ceilingCheckBytes && c.g != nil {
		c.sinceCheck = 0
		st, serr := c.g.Stats()
		if serr == nil && st.TotalBytes > 0 &&
			st.TotalBytes-st.FreeBytes >= st.TotalBytes*int64(DiskCeilingPct)/100 {
			return n, errDiskCeilingExceeded
		}
	}
	return n, err
}

// DiskGuard is the admission ledger: in-memory reservations that close the
// check-then-act race between concurrent writers. All own-writes (HTTP and
// P2P sync) admit here before touching disk.
type DiskGuard struct {
	mu       sync.Mutex
	reserved int64
	dataDir  string
	// statFn is injectable for tests.
	statFn func(string) (DiskStats, error)
}

// NewDiskGuard creates an admission ledger for dataDir.
func NewDiskGuard(dataDir string) *DiskGuard {
	return &DiskGuard{dataDir: dataDir, statFn: StatDisk}
}

// Admit reserves n bytes if used+reserved+n stays under the ceiling.
// Unknown-size streams reserve 0 and rely on streaming ceiling checks;
// see CopyWithCeiling.
func (g *DiskGuard) Admit(n int64) bool {
	if g == nil {
		return true
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	st, err := g.statNow()
	if err != nil {
		// Stat failure is not a disk-full signal: fail open so a wedged
		// statfs can't wedge writes. Real ENOSPC still surfaces per-op.
		return true
	}
	ceiling := st.TotalBytes * int64(DiskCeilingPct) / 100
	used := st.TotalBytes - st.FreeBytes
	if used+g.reserved+n > ceiling {
		return false
	}
	g.reserved += n
	return true
}

// Release frees a prior reservation.
func (g *DiskGuard) Release(n int64) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.reserved -= n
	if g.reserved < 0 {
		g.reserved = 0
	}
}

// statNow runs the configured stat function for the guard's data dir.
func (g *DiskGuard) statNow() (DiskStats, error) {
	fn := StatDisk
	path := "/data"
	if g != nil {
		if g.statFn != nil {
			fn = g.statFn
		}
		if g.dataDir != "" {
			path = g.dataDir
		}
	}
	return fn(path)
}

// Stats reports current disk state (unreserved; raw filesystem view).
func (g *DiskGuard) Stats() (DiskStats, error) {
	if g == nil {
		return StatDisk("/data")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.statNow()
}

// OverSoftMark reports whether usage crossed the background-sweep mark.
func (g *DiskGuard) OverSoftMark() bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	st, err := g.statNow()
	if err != nil || st.TotalBytes <= 0 {
		return false
	}
	used := st.TotalBytes - st.FreeBytes
	return used+g.reserved > st.TotalBytes*int64(DiskSoftPct)/100
}

// TargetFreeBytes converts the emergency free-space target to bytes.
func TargetFreeBytes(st DiskStats) int64 {
	if st.TotalBytes <= 0 {
		return 0
	}
	return st.TotalBytes * int64(EmergencyTargetFreePct) / 100
}
