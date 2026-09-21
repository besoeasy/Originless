package modules

import (
	"math"
	"time"
)

// Retention bounds derived from the policy constants.
var (
	blobMinAge = time.Duration(BlobMinAgeDays) * 24 * time.Hour
	blobMaxAge = time.Duration(BlobMaxAgeDays) * 24 * time.Hour
)

// BlobRetentionForSize returns how long a blob of the given size is
// protected from janitor eviction:
//
//	retention = min_age + (min_age - max_age) * (size/max_size - 1)^3
//
// Size 0 retains for max_age (1 year); size >= max_size retains for
// min_age (30 days); anything in between decays cubically.
func BlobRetentionForSize(sizeBytes int64) time.Duration {
	if sizeBytes <= 0 {
		return blobMaxAge
	}
	if sizeBytes >= BlobMaxSizeBytes {
		return blobMinAge
	}
	r := float64(sizeBytes)/float64(BlobMaxSizeBytes) - 1
	retention := float64(blobMinAge) + float64(blobMinAge-blobMaxAge)*math.Pow(r, 3)
	// Clamp float noise to the [min_age, max_age] window.
	if retention < float64(blobMinAge) {
		return blobMinAge
	}
	if retention > float64(blobMaxAge) {
		return blobMaxAge
	}
	return time.Duration(retention)
}

// BlobRetainedUntil returns the wall-clock time a blob created at
// createdAt stops being protected from eviction.
func BlobRetainedUntil(createdAt time.Time, sizeBytes int64) time.Time {
	return createdAt.Add(BlobRetentionForSize(sizeBytes))
}

// BlobRetentionExpired reports whether a blob of the given size created
// at createdAt has outlived its retention guarantee as of now.
func BlobRetentionExpired(createdAt time.Time, sizeBytes int64, now time.Time) bool {
	return !now.Before(BlobRetainedUntil(createdAt, sizeBytes))
}
