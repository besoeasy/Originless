package modules

import (
	"errors"
	"fmt"
	"io"
	"syscall"
	"testing"
)

// fakeCoder exercises the sqlite-Code() branch without the driver.
type fakeCoder struct{ code int }

func (f *fakeCoder) Error() string { return fmt.Sprintf("sqlite error %d", f.code) }
func (f *fakeCoder) Code() int     { return f.code }

func TestIsDiskFull(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"enospc direct", syscall.ENOSPC, true},
		{"enospc wrapped", fmt.Errorf("write tmp: %w", syscall.ENOSPC), true},
		{"sqlite full code", &fakeCoder{code: 13}, true},
		{"sqlite full wrapped", fmt.Errorf("insert: %w", &fakeCoder{code: 13}), true},
		{"sqlite corrupt must not evict", &fakeCoder{code: 11}, false},
		{"sqlite busy must not evict", &fakeCoder{code: 5}, false},
		{"permission must not evict", syscall.EACCES, false},
		{"generic must not evict", errors.New("connection reset"), false},
		{"message fallback", errors.New("write failed: no space left on device"), true},
		{"message sqlite full", errors.New("database or disk is full"), true},
	}
	for _, tc := range cases {
		if got := IsDiskFull(tc.err); got != tc.want {
			t.Errorf("%s: IsDiskFull = %v, want %v", tc.name, got, tc.want)
		}
	}
	if IsStorageExhausted(nil) {
		t.Error("IsStorageExhausted(nil) must be false")
	}
	if !IsStorageExhausted(errDiskCeilingExceeded) {
		t.Error("ceiling aborts must count as storage-exhausted")
	}
	if IsStorageExhausted(errors.New("boom")) {
		t.Error("generic errors must not count as storage-exhausted")
	}
}

func fixedStats(total, free int64) func(string) (DiskStats, error) {
	return func(string) (DiskStats, error) {
		used := 100.0 * float64(total-free) / float64(total)
		return DiskStats{Path: "test", TotalBytes: total, FreeBytes: free, UsedPct: used}, nil
	}
}

func TestDiskGuardAdmitRelease(t *testing.T) {
	g := &DiskGuard{dataDir: "test", statFn: fixedStats(1000, 200)} // used 800, ceiling 900
	if !g.Admit(50) {
		t.Fatal("Admit(50) should succeed under ceiling")
	}
	if g.Admit(100) {
		t.Fatal("Admit(100) must fail: 800+50+100 > 900")
	}
	g.Release(50)
	if !g.Admit(100) {
		t.Fatal("Admit(100) should succeed after release")
	}
	g.Release(100)
	g.Release(999) // over-release clamps, never negative
	if !g.Admit(100) {
		t.Fatal("ledger must recover after over-release")
	}
}

func TestDiskGuardNilSafe(t *testing.T) {
	var g *DiskGuard
	if !g.Admit(1 << 40) {
		t.Error("nil guard must fail open")
	}
	g.Release(1 << 40) // must not panic
	if g.OverSoftMark() {
		t.Error("nil guard must report no pressure")
	}
}

func TestDiskGuardConcurrent(t *testing.T) {
	g := &DiskGuard{dataDir: "test", statFn: fixedStats(100000, 50000)}
	done := make(chan bool, 64)
	for i := 0; i < 64; i++ {
		go func() {
			for j := 0; j < 50; j++ {
				if g.Admit(10) {
					g.Release(10)
				}
			}
			done <- true
		}()
	}
	for i := 0; i < 64; i++ {
		<-done
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.reserved != 0 {
		t.Errorf("ledger leaked: reserved=%d", g.reserved)
	}
}

func TestOverSoftMark(t *testing.T) {
	over := &DiskGuard{dataDir: "t", statFn: fixedStats(1000, 100)} // 90% used
	if !over.OverSoftMark() {
		t.Error("90% used must trip the 85% soft mark")
	}
	calm := &DiskGuard{dataDir: "t", statFn: fixedStats(1000, 500)}
	if calm.OverSoftMark() {
		t.Error("50% used must not trip the soft mark")
	}
}

func TestStatDiskTempDir(t *testing.T) {
	st, err := StatDisk(t.TempDir())
	if err != nil {
		t.Fatalf("StatDisk(tempdir): %v", err)
	}
	if st.TotalBytes <= 0 {
		t.Error("expected positive total bytes")
	}
}

func TestCeilingWriterCap(t *testing.T) {
	w := &ceilingWriter{w: io.Discard, maxBytes: 10}
	if _, err := w.Write([]byte("12345")); err != nil {
		t.Fatalf("small write: %v", err)
	}
	if _, err := w.Write([]byte("123456")); !errors.Is(err, errDiskCapExceeded) {
		t.Fatalf("expected cap error, got %v", err)
	}
}
