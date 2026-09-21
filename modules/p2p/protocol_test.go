package p2p

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestProtocolFrameRoundTrip(t *testing.T) {
	buf := new(bytes.Buffer)

	// 1. Write Hello
	hello := HelloPayload{
		NodeID:     "node-123456",
		NetworkID:  "my-swarm",
		Version:    "dev",
		EventCount: 42,
		BlobCount:  10,
		ListenPort: 3232,
	}
	helloJSON, _ := json.Marshal(hello)
	if err := WriteFrame(buf, MsgHello, helloJSON); err != nil {
		t.Fatalf("write hello: %v", err)
	}

	// 2. Write Ping (empty payload)
	if err := WriteFrame(buf, MsgPing, nil); err != nil {
		t.Fatalf("write ping: %v", err)
	}

	// Read Hello back
	msgType1, payload1, err := ReadFrame(buf)
	if err != nil {
		t.Fatalf("read hello: %v", err)
	}
	if msgType1 != MsgHello {
		t.Fatalf("expected MsgHello, got 0x%02x", msgType1)
	}
	var readHello HelloPayload
	if err := json.Unmarshal(payload1, &readHello); err != nil {
		t.Fatalf("unmarshal hello: %v", err)
	}
	if readHello.NodeID != hello.NodeID || readHello.NetworkID != hello.NetworkID {
		t.Fatalf("mismatch in hello payload: %+v", readHello)
	}

	// Read Ping back
	msgType2, payload2, err := ReadFrame(buf)
	if err != nil {
		t.Fatalf("read ping: %v", err)
	}
	if msgType2 != MsgPing {
		t.Fatalf("expected MsgPing, got 0x%02x", msgType2)
	}
	if len(payload2) != 0 {
		t.Fatalf("expected empty payload for ping, got %d bytes", len(payload2))
	}
}

func TestProtocolOversizedFrame(t *testing.T) {
	buf := new(bytes.Buffer)
	oversized := make([]byte, MaxFramePayloadSize+1)
	err := WriteFrame(buf, MsgBlobData, oversized)
	if err != ErrFrameTooLarge {
		t.Fatalf("expected ErrFrameTooLarge, got: %v", err)
	}
}
