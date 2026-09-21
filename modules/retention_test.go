package modules

import (
	"testing"
	"time"
)

func TestBlobRetentionBoundaries(t *testing.T) {
	minAge := time.Duration(BlobMinAgeDays) * 24 * time.Hour
	maxAge := time.Duration(BlobMaxAgeDays) * 24 * time.Hour

	if got := BlobRetentionForSize(0); got != maxAge {
		t.Fatalf("size 0 = %s, want %s", got, maxAge)
	}
	if got := BlobRetentionForSize(-1); got != maxAge {
		t.Fatalf("negative size = %s, want %s", got, maxAge)
	}
	if got := BlobRetentionForSize(BlobMaxSizeBytes); got != minAge {
		t.Fatalf("size max = %s, want %s", got, minAge)
	}
	if got := BlobRetentionForSize(BlobMaxSizeBytes + 1); got != minAge {
		t.Fatalf("oversize = %s, want %s", got, minAge)
	}
}

func TestBlobRetentionMidpoint(t *testing.T) {
	// size = max/2 -> retention = min + (min-max)*(-0.5)^3 = 71.875 days.
	want := 71.875 * 24 * time.Hour
	got := BlobRetentionForSize(BlobMaxSizeBytes / 2)
	if diff := got - want; diff < -time.Second || diff > time.Second {
		t.Fatalf("half max = %s, want %s", got, want)
	}
}

func TestBlobRetentionMonotonic(t *testing.T) {
	sizes := []int64{1, 1024, 1024 * 1024, 256 * 1024 * 1024, 511 * 1024 * 1024}
	prev := BlobRetentionForSize(0)
	for _, s := range sizes {
		got := BlobRetentionForSize(s)
		if got >= prev {
			t.Fatalf("retention not decreasing: size %d -> %s, prev %s", s, got, prev)
		}
		if got <= blobMinAge || got >= blobMaxAge {
			t.Fatalf("size %d retention %s outside (min, max)", s, got)
		}
		prev = got
	}
}

func TestBlobRetentionExpired(t *testing.T) {
	now := time.Now()
	// Max-size blob created 31 days ago: 30-day retention -> expired.
	if !BlobRetentionExpired(now.Add(-31*24*time.Hour), BlobMaxSizeBytes, now) {
		t.Fatal("max-size blob past 30d should be expired")
	}
	// Tiny blob created 31 days ago: ~1-year retention -> protected.
	if BlobRetentionExpired(now.Add(-31*24*time.Hour), 9, now) {
		t.Fatal("tiny blob at 31d should still be protected")
	}
	// Fresh max-size blob: protected.
	if BlobRetentionExpired(now, BlobMaxSizeBytes, now) {
		t.Fatal("fresh blob should be protected")
	}
}
