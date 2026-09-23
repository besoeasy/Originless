package p2p

import (
	"fmt"
	"testing"
)

func TestBloomFilterZeroFalseNegatives(t *testing.T) {
	const n = 1000
	bf := NewBloomFilter(n, 0.01)

	var items []string
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("event-id-%08d-abcdef1234567890", i)
		items = append(items, id)
		bf.AddString(id)
	}

	if bf.Count != n {
		t.Fatalf("expected count=%d, got %d", n, bf.Count)
	}

	// Zero false negatives: every added item MUST be reported as present
	for _, item := range items {
		if !bf.ContainsString(item) {
			t.Fatalf("false negative detected for item: %s", item)
		}
	}
}

func TestBloomFilterFalsePositiveRate(t *testing.T) {
	const n = 2000
	const targetFp = 0.01 // 1%
	bf := NewBloomFilter(n, targetFp)

	for i := 0; i < n; i++ {
		bf.AddString(fmt.Sprintf("item-%d", i))
	}

	// Test 5000 items that were NOT added
	falsePositives := 0
	const testCount = 5000
	for i := 0; i < testCount; i++ {
		notAdded := fmt.Sprintf("unadded-test-item-%d", i)
		if bf.ContainsString(notAdded) {
			falsePositives++
		}
	}

	actualFp := float64(falsePositives) / float64(testCount)
	t.Logf("tested %d unadded items, false positives: %d (%.4f%%, target was %.2f%%)", testCount, falsePositives, actualFp*100, targetFp*100)

	// Allow small statistical margin (e.g. up to 2.5%)
	if actualFp > 0.025 {
		t.Fatalf("false positive rate too high: got %.4f, expected <= 0.025", actualFp)
	}
}

func TestBloomFilterSerializationRoundTrip(t *testing.T) {
	bf := NewBloomFilter(500, 0.01)
	for i := 0; i < 500; i++ {
		bf.AddString(fmt.Sprintf("hash-%d", i))
	}

	encoded := bf.Bytes()
	if len(encoded) < 24 {
		t.Fatalf("encoded bytes too short: %d", len(encoded))
	}

	decoded, err := DeserializeBloomFilter(encoded)
	if err != nil {
		t.Fatalf("deserialize failed: %v", err)
	}

	if decoded.M != bf.M || decoded.K != bf.K || decoded.Count != bf.Count {
		t.Fatalf("mismatch in decoded metadata: got M=%d, K=%d, Count=%d; want M=%d, K=%d, Count=%d",
			decoded.M, decoded.K, decoded.Count, bf.M, bf.K, bf.Count)
	}

	for i := 0; i < 500; i++ {
		item := fmt.Sprintf("hash-%d", i)
		if !decoded.ContainsString(item) {
			t.Fatalf("decoded filter missing item: %s", item)
		}
	}

	if decoded.ContainsString("definitely-not-there") {
		t.Fatalf("decoded filter reported true for unadded item")
	}
}

func TestBloomFilterCorruptedData(t *testing.T) {
	_, err := DeserializeBloomFilter([]byte{1, 2, 3})
	if err == nil {
		t.Fatalf("expected error on truncated data")
	}

	bf := NewBloomFilter(100, 0.01)
	encoded := bf.Bytes()
	// Corrupt magic
	encoded[0] = 0xFF
	_, err = DeserializeBloomFilter(encoded)
	if err == nil {
		t.Fatalf("expected error on bad magic")
	}
}

func TestBloomFilterSaltDifferentiation(t *testing.T) {
	bf1 := NewBloomFilterWithSalt(100, 0.01, 1111)
	bf2 := NewBloomFilterWithSalt(100, 0.01, 2222)

	for i := 0; i < 50; i++ {
		item := fmt.Sprintf("event-%d", i)
		bf1.AddString(item)
		bf2.AddString(item)
	}

	// Verify both contain their items
	for i := 0; i < 50; i++ {
		item := fmt.Sprintf("event-%d", i)
		if !bf1.ContainsString(item) || !bf2.ContainsString(item) {
			t.Fatalf("expected both filters to contain added item")
		}
	}

	// Bits should differ because salts are different
	same := true
	for i := range bf1.Bits {
		if bf1.Bits[i] != bf2.Bits[i] {
			same = false
			break
		}
	}
	if same {
		t.Fatalf("expected bit patterns to differ between different salts")
	}
}

func TestBloomFilterLegacy24ByteDeserialization(t *testing.T) {
	bf := NewBloomFilterWithSalt(100, 0.01, 0)
	for i := 0; i < 50; i++ {
		bf.AddString(fmt.Sprintf("item-%d", i))
	}

	// Craft legacy 24-byte header:
	// [4 magic][8 M][4 K][8 Count][bitset]
	legacy := make([]byte, 24+len(bf.Bits))
	copy(legacy[0:4], []byte{0x42, 0x4C, 0x4F, 0x4D}) // BLOM
	// M
	legacy[4] = byte(bf.M >> 56)
	legacy[5] = byte(bf.M >> 48)
	legacy[6] = byte(bf.M >> 40)
	legacy[7] = byte(bf.M >> 32)
	legacy[8] = byte(bf.M >> 24)
	legacy[9] = byte(bf.M >> 16)
	legacy[10] = byte(bf.M >> 8)
	legacy[11] = byte(bf.M)
	// K
	legacy[12] = byte(bf.K >> 24)
	legacy[13] = byte(bf.K >> 16)
	legacy[14] = byte(bf.K >> 8)
	legacy[15] = byte(bf.K)
	// Count
	legacy[16] = byte(bf.Count >> 56)
	legacy[17] = byte(bf.Count >> 48)
	legacy[18] = byte(bf.Count >> 40)
	legacy[19] = byte(bf.Count >> 32)
	legacy[20] = byte(bf.Count >> 24)
	legacy[21] = byte(bf.Count >> 16)
	legacy[22] = byte(bf.Count >> 8)
	legacy[23] = byte(bf.Count)
	copy(legacy[24:], bf.Bits)

	decoded, err := DeserializeBloomFilter(legacy)
	if err != nil {
		t.Fatalf("failed to deserialize legacy 24-byte bloom: %v", err)
	}
	if decoded.Salt != 0 {
		t.Fatalf("expected legacy salt=0, got %d", decoded.Salt)
	}
	if !decoded.ContainsString("item-10") {
		t.Fatalf("legacy decoded filter missing item")
	}
}

