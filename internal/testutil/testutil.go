// Package testutil holds helpers shared by the events and server tests.
package testutil

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func MakeSignedEvent(t *testing.T, createdAt, expiresAt int64, collection string, data any, labels []string, blob string) ([]byte, string) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate signing key: %v", err)
	}
	owner := "ed25519:" + hex.EncodeToString(publicKey)
	dataJSON, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal event data: %v", err)
	}
	canonicalData, err := canonicalJSON(dataJSON)
	if err != nil {
		t.Fatalf("canonicalize event data: %v", err)
	}
	message := signingMessage(owner, collection, createdAt, expiresAt, canonicalData, blob, labels)
	digest := sha256.Sum256(message)
	signature := ed25519.Sign(privateKey, digest[:])

	payload := map[string]any{
		"owner":      owner,
		"collection": collection,
		"created_at": createdAt,
		"expires_at": expiresAt,
		"data":       json.RawMessage(dataJSON),
		"labels":     labels,
		"sig":        hex.EncodeToString(signature),
	}
	if blob != "" {
		payload["blob"] = blob
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal signed event: %v", err)
	}
	return raw, owner
}

func PublishTestEvent(t *testing.T, router http.Handler, raw []byte) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/events", bytes.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func signingMessage(owner, collection string, createdAt, expiresAt int64, canonicalData []byte, blob string, labels []string) []byte {
	var message strings.Builder
	message.WriteString(owner)
	message.WriteByte(':')
	message.WriteString(collection)
	message.WriteByte(':')
	message.WriteString(strconv.FormatInt(createdAt, 10))
	message.WriteByte(':')
	message.WriteString(strconv.FormatInt(expiresAt, 10))
	message.WriteByte(':')
	message.Write(canonicalData)
	message.WriteByte(':')
	message.WriteString(blob)
	message.WriteByte(':')
	message.WriteString(strings.Join(labels, ","))
	return []byte(message.String())
}

func canonicalJSON(raw []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("multiple JSON values")
	}
	return marshalCanonicalJSON(value), nil
}

func marshalCanonicalJSON(value any) []byte {
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		var output bytes.Buffer
		output.WriteByte('{')
		for index, key := range keys {
			if index > 0 {
				output.WriteByte(',')
			}
			encodedKey, _ := json.Marshal(key)
			output.Write(encodedKey)
			output.WriteByte(':')
			output.Write(marshalCanonicalJSON(typed[key]))
		}
		output.WriteByte('}')
		return output.Bytes()
	case []any:
		var output bytes.Buffer
		output.WriteByte('[')
		for index, element := range typed {
			if index > 0 {
				output.WriteByte(',')
			}
			output.Write(marshalCanonicalJSON(element))
		}
		output.WriteByte(']')
		return output.Bytes()
	default:
		encoded, _ := json.Marshal(typed)
		return encoded
	}
}
