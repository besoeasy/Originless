package p2p

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math"
)

var (
	ErrInvalidBloomData = errors.New("invalid bloom filter data")
)

const bloomMagic = 0x424C4F4D // "BLOM"

// BloomFilter is a thread-safe, space-efficient probabilistic data structure
// used for rapid set reconciliation over event IDs and blob hashes.
type BloomFilter struct {
	M     uint64 // Total number of bits in the filter
	K     uint32 // Number of hash functions
	Count uint64 // Number of elements added
	Salt  uint64 // Salt/nonce preventing persistent false-positive traps across reconciliation rounds
	Bits  []byte // Raw bitset
}

// NewBloomFilter creates a filter sized optimally for expectedItems with the desired false-positive rate,
// initialized with a random salt to ensure false positives do not persist across reconciliation cycles.
func NewBloomFilter(expectedItems int, fpRate float64) *BloomFilter {
	var b [8]byte
	_, _ = rand.Read(b[:])
	salt := binary.BigEndian.Uint64(b[:])
	return NewBloomFilterWithSalt(expectedItems, fpRate, salt)
}

// NewBloomFilterWithSalt creates a filter with an explicit salt (useful for deterministic tests).
func NewBloomFilterWithSalt(expectedItems int, fpRate float64, salt uint64) *BloomFilter {
	if expectedItems < 64 {
		expectedItems = 64
	}
	if fpRate <= 0 || fpRate >= 1.0 {
		fpRate = 0.01 // 1% false positive default
	}

	n := float64(expectedItems)
	// m = - (n * ln(p)) / (ln(2)^2)
	m := math.Ceil(-n * math.Log(fpRate) / (math.Ln2 * math.Ln2))
	if m < 64 {
		m = 64
	}
	// k = (m / n) * ln(2)
	k := math.Round((m / n) * math.Ln2)
	if k < 1 {
		k = 1
	} else if k > 30 {
		k = 30
	}

	mUint := uint64(m)
	kUint := uint32(k)
	byteLen := (mUint + 7) / 8

	return &BloomFilter{
		M:     mUint,
		K:     kUint,
		Count: 0,
		Salt:  salt,
		Bits:  make([]byte, byteLen),
	}
}

// hashes returns two 64-bit hash values from sha256 to implement Kirsch-Mitzenmacher double hashing.
func hashes(data []byte, salt uint64) (uint64, uint64) {
	var saltBuf [8]byte
	binary.BigEndian.PutUint64(saltBuf[:], salt)

	h := sha256.New()
	h.Write(saltBuf[:])
	h.Write(data)
	sum := h.Sum(nil)

	h1 := binary.BigEndian.Uint64(sum[0:8])
	h2 := binary.BigEndian.Uint64(sum[8:16])
	if h2 == 0 {
		h2 = 1 // Ensure step is non-zero
	}
	return h1, h2
}

// Add inserts an arbitrary byte slice into the filter.
func (bf *BloomFilter) Add(data []byte) {
	if bf.M == 0 || len(bf.Bits) == 0 {
		return
	}
	h1, h2 := hashes(data, bf.Salt)
	for i := uint32(0); i < bf.K; i++ {
		idx := (h1 + uint64(i)*h2) % bf.M
		bf.Bits[idx/8] |= 1 << (idx % 8)
	}
	bf.Count++
}

// AddString inserts a string (such as a 64-hex Event ID or Blob SHA-256 hash).
func (bf *BloomFilter) AddString(s string) {
	bf.Add([]byte(s))
}

// Contains checks whether the item might be in the set.
// If it returns false, the item is definitely NOT in the set (zero false negatives).
// If it returns true, the item is probably in the set (with target false-positive rate).
func (bf *BloomFilter) Contains(data []byte) bool {
	if bf.M == 0 || len(bf.Bits) == 0 {
		return false
	}
	h1, h2 := hashes(data, bf.Salt)
	for i := uint32(0); i < bf.K; i++ {
		idx := (h1 + uint64(i)*h2) % bf.M
		if (bf.Bits[idx/8] & (1 << (idx % 8))) == 0 {
			return false
		}
	}
	return true
}

// ContainsString checks whether a string might be in the set.
func (bf *BloomFilter) ContainsString(s string) bool {
	return bf.Contains([]byte(s))
}

// Bytes serializes the Bloom filter to a compact binary slice.
// Wire Format:
// [4 bytes Magic (0x424C4F4D)]
// [8 bytes M (uint64 BigEndian)]
// [4 bytes K (uint32 BigEndian)]
// [8 bytes Count (uint64 BigEndian)]
// [8 bytes Salt (uint64 BigEndian)]
// [remaining bytes: Bitset]
func (bf *BloomFilter) Bytes() []byte {
	out := make([]byte, 32+len(bf.Bits))
	binary.BigEndian.PutUint32(out[0:4], bloomMagic)
	binary.BigEndian.PutUint64(out[4:12], bf.M)
	binary.BigEndian.PutUint32(out[12:16], bf.K)
	binary.BigEndian.PutUint64(out[16:24], bf.Count)
	binary.BigEndian.PutUint64(out[24:32], bf.Salt)
	copy(out[32:], bf.Bits)
	return out
}

// DeserializeBloomFilter decodes a binary slice back into a BloomFilter.
// Supports both new 32-byte header with Salt and legacy 24-byte header without Salt.
func DeserializeBloomFilter(data []byte) (*BloomFilter, error) {
	if len(data) < 24 {
		return nil, ErrInvalidBloomData
	}
	magic := binary.BigEndian.Uint32(data[0:4])
	if magic != bloomMagic {
		return nil, ErrInvalidBloomData
	}

	m := binary.BigEndian.Uint64(data[4:12])
	k := binary.BigEndian.Uint32(data[12:16])
	count := binary.BigEndian.Uint64(data[16:24])

	expectedBytes := (m + 7) / 8
	var salt uint64
	var bitset []byte

	if uint64(len(data)) == 32+expectedBytes {
		salt = binary.BigEndian.Uint64(data[24:32])
		bitset = data[32:]
	} else if uint64(len(data)) == 24+expectedBytes {
		salt = 0
		bitset = data[24:]
	} else {
		return nil, ErrInvalidBloomData
	}

	bitsCopy := make([]byte, len(bitset))
	copy(bitsCopy, bitset)

	return &BloomFilter{
		M:     m,
		K:     k,
		Count: count,
		Salt:  salt,
		Bits:  bitsCopy,
	}, nil
}
