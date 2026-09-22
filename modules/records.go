package modules

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	// MaxRecordSize is the max total JSON size in bytes (8 KB).
	MaxRecordSize = 8192
	// MaxRecordTTL is the max expires_at - created_at (1 year).
	MaxRecordTTL int64 = 365 * 24 * 3600 // 31536000
	// MaxRecordLabels caps indexable labels per record.
	MaxRecordLabels = 10
	// MaxCreatedDrift allows 15 min future clock skew on created_at.
	MaxCreatedDrift int64 = 900
)

var (
	collectionPattern = regexp.MustCompile(`^[a-z0-9/_-]{1,32}$`)
	labelPattern      = regexp.MustCompile(`^[A-Za-z0-9:_\-/\.\+]{1,64}$`)
)

// Record is the stored signed object. ID is server-computed, never client-sent.
type Record struct {
	ID         string          `json:"id"`
	Owner      string          `json:"owner"`
	Collection string          `json:"collection"`
	CreatedAt  int64           `json:"created_at"`
	ExpiresAt  int64           `json:"expires_at"`
	Data       json.RawMessage `json:"data"`
	Labels     []string        `json:"labels"`
	Sig        string          `json:"sig"`
	StoredAt   string          `json:"stored_at,omitempty"`
	Size       int64           `json:"size,omitempty"`
}

// recordInput is the client payload (no id, no stored_at).
type recordInput struct {
	Owner      *string         `json:"owner"`
	Collection *string         `json:"collection"`
	CreatedAt  *int64          `json:"created_at"`
	ExpiresAt  *int64          `json:"expires_at"`
	Data       json.RawMessage `json:"data"`
	Labels     []string        `json:"labels"`
	Sig        *string         `json:"sig"`
}

// ValidateRecordBody checks size, schema, TTL, recomputes ID and verifies ed25519.
// nowUnix is time.Now().Unix() — injected for deterministic tests.
func ValidateRecordBody(raw []byte, nowUnix int64) (*Record, error) {
	if len(raw) > MaxRecordSize {
		return nil, fmt.Errorf("record too large: %d > %d", len(raw), MaxRecordSize)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, fmt.Errorf("empty body")
	}

	// Reject client-sent id (server-computed).
	var extra map[string]json.RawMessage
	if err := json.Unmarshal(raw, &extra); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	if _, ok := extra["id"]; ok {
		return nil, fmt.Errorf("id is server-computed, do not send it")
	}

	var in recordInput
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&in); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}

	if in.Owner == nil || *in.Owner == "" {
		return nil, fmt.Errorf("missing owner")
	}
	if in.Collection == nil || *in.Collection == "" {
		return nil, fmt.Errorf("missing collection")
	}
	if in.CreatedAt == nil {
		return nil, fmt.Errorf("missing created_at")
	}
	if in.ExpiresAt == nil {
		return nil, fmt.Errorf("missing expires_at")
	}
	if in.Sig == nil || *in.Sig == "" {
		return nil, fmt.Errorf("missing sig")
	}
	if in.Labels == nil {
		return nil, fmt.Errorf("missing labels (send [] for none)")
	}
	if len(in.Data) == 0 || string(bytes.TrimSpace(in.Data)) == "null" {
		return nil, fmt.Errorf("missing data (must be a JSON object)")
	}

	owner := *in.Owner
	collection := *in.Collection
	created := *in.CreatedAt
	expires := *in.ExpiresAt
	sigHex := strings.ToLower(strings.TrimSpace(*in.Sig))

	if !collectionPattern.MatchString(collection) {
		return nil, fmt.Errorf("invalid collection: must match ^[a-z0-9/_-]{1,32}$")
	}
	if len(in.Labels) > MaxRecordLabels {
		return nil, fmt.Errorf("too many labels: %d > %d", len(in.Labels), MaxRecordLabels)
	}
	seen := make(map[string]bool, len(in.Labels))
	for _, l := range in.Labels {
		if !labelPattern.MatchString(l) {
			return nil, fmt.Errorf("invalid label %q", l)
		}
		if seen[l] {
			return nil, fmt.Errorf("duplicate label %q", l)
		}
		seen[l] = true
	}

	if created <= 0 {
		return nil, fmt.Errorf("invalid created_at")
	}
	if expires <= created {
		return nil, fmt.Errorf("expires_at must be > created_at")
	}
	if expires-created > MaxRecordTTL {
		return nil, fmt.Errorf("expires_at too far: max +1y (31536000s)")
	}
	if created > nowUnix+MaxCreatedDrift {
		return nil, fmt.Errorf("created_at too far in the future")
	}

	pub, err := parseOwnerPubkey(owner)
	if err != nil {
		return nil, err
	}
	sig, err := hex.DecodeString(sigHex)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return nil, fmt.Errorf("invalid sig: must be 128 hex chars (ed25519)")
	}

	canonical, err := canonicalJSON(in.Data)
	if err != nil {
		return nil, fmt.Errorf("invalid data: %w", err)
	}
	// Data must be a JSON object (not array/string/number) — keeps app semantics clean.
	var objCheck map[string]any
	if err := json.Unmarshal(canonical, &objCheck); err != nil || objCheck == nil {
		return nil, fmt.Errorf("data must be a JSON object")
	}

	// Optional blob linkage: data may carry "_blob": "<sha256 hex>" to
	// attach this record to a stored binary blob. The hash is covered by
	// the signature (it is part of the canonical data in the record ID),
	// so the link is tamper-proof. Existence is enforced by the caller
	// against the store; here we only check the shape.
	if _, err := RecordBlobHash(canonical); err != nil {
		return nil, err
	}

	idHex, idBytes := computeRecordID(owner, collection, created, expires, canonical, in.Labels)
	if !ed25519.Verify(pub, idBytes, sig) {
		return nil, fmt.Errorf("bad sig")
	}

	return &Record{
		ID:         idHex,
		Owner:      owner,
		Collection: collection,
		CreatedAt:  created,
		ExpiresAt:  expires,
		Data:       json.RawMessage(canonical),
		Labels:     append([]string{}, in.Labels...),
		Sig:        sigHex,
		Size:       int64(len(raw)),
	}, nil
}

// RecordBlobHash extracts the optional blob linkage from record data.
// The reserved root key "bin" holds a sha256 hex string: a signed event
// pins a content-addressed blob by naming its hash. A present-but-
// malformed value is an error (the server refuses to guess a hash that
// was signed), so "bin" cannot be repurposed client-side.
func RecordBlobHash(data json.RawMessage) (string, error) {
	return blobHashFromObject([]byte(data), "bin")
}

// blobHashFromObject reads key from a JSON object and validates it as a
// sha256 hex string. Returns "" for absent/null values; errors for malformed
// ones. Non-object JSON yields "" with no error (shape is validated elsewhere).
func blobHashFromObject(data []byte, key string) (string, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return "", nil // shape validated elsewhere; nothing to extract
	}
	raw, ok := obj[key]
	if !ok || string(bytes.TrimSpace(raw)) == "null" {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", fmt.Errorf("invalid %s: must be a sha256 hex string", key)
	}
	h, err := NormalizeBlobHash(s)
	if err != nil {
		return "", fmt.Errorf("invalid %s: %v", key, err)
	}
	return h, nil
}

func parseOwnerPubkey(owner string) (ed25519.PublicKey, error) {
	const prefix = "ed25519:"
	if !strings.HasPrefix(owner, prefix) {
		return nil, fmt.Errorf("invalid owner: must be ed25519:<64 hex>")
	}
	hexPart := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(owner, prefix)))
	raw, err := hex.DecodeString(hexPart)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("invalid owner: must be ed25519:<64 hex>")
	}
	return ed25519.PublicKey(raw), nil
}

func computeRecordID(owner, collection string, created, expires int64, canonicalData []byte, labels []string) (string, []byte) {
	var sb strings.Builder
	sb.WriteString(owner)
	sb.WriteByte(':')
	sb.WriteString(collection)
	sb.WriteByte(':')
	sb.WriteString(strconv.FormatInt(created, 10))
	sb.WriteByte(':')
	sb.WriteString(strconv.FormatInt(expires, 10))
	sb.WriteByte(':')
	sb.Write(canonicalData)
	sb.WriteByte(':')
	sb.WriteString(strings.Join(labels, ","))
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:]), sum[:]
}

// canonicalJSON returns sorted-keys, whitespace-free JSON for stable hashing.
func canonicalJSON(raw json.RawMessage) ([]byte, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	return canonicalMarshal(v), nil
}

func canonicalMarshal(v any) []byte {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var buf bytes.Buffer
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			kb, _ := json.Marshal(k)
			buf.Write(kb)
			buf.WriteByte(':')
			buf.Write(canonicalMarshal(t[k]))
		}
		buf.WriteByte('}')
		return buf.Bytes()
	case []any:
		var buf bytes.Buffer
		buf.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				buf.WriteByte(',')
			}
			buf.Write(canonicalMarshal(e))
		}
		buf.WriteByte(']')
		return buf.Bytes()
	default:
		b, _ := json.Marshal(t)
		return b
	}
}
