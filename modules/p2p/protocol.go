package p2p

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Message Type Constants
const (
	MsgHello            byte = 0x01
	MsgBloomEventsReq   byte = 0x02
	MsgBloomEventsResp  byte = 0x03
	MsgEventPush        byte = 0x04
	MsgEventBroadcast   byte = 0x05
	MsgBloomBlobsReq    byte = 0x06
	MsgBloomBlobsResp   byte = 0x07
	MsgBlobPull         byte = 0x08
	MsgBlobData         byte = 0x09
	MsgBlobBroadcast    byte = 0x0A
	MsgPing             byte = 0x0B
	MsgPong             byte = 0x0C
)

const (
	// MaxFramePayloadSize is 64 MiB (safe upper bound for large blob transfers)
	MaxFramePayloadSize = 64 * 1024 * 1024
)

var (
	ErrFrameTooLarge   = errors.New("frame payload exceeds maximum size")
	ErrInvalidFrameLen = errors.New("invalid frame length")
)

// HelloPayload is exchanged upon connection establishment to verify Network ID and exchange metadata.
type HelloPayload struct {
	NodeID     string `json:"node_id"`
	NetworkID  string `json:"network_id"`
	Version    string `json:"version"`
	EventCount int64  `json:"event_count"`
	BlobCount  int           `json:"blob_count"`
	ListenPort int           `json:"listen_port"`
	Peers      []PeerHint    `json:"peers,omitempty"`
}

// PeerHint is a libp2p peer identity advertised over PEX (not a raw socket address).
type PeerHint struct {
	ID    string   `json:"id"`
	Addrs []string `json:"addrs,omitempty"`
}

// MissingEventsPayload contains event IDs that a peer needs to receive.
type MissingEventsPayload struct {
	MissingIDs []string `json:"missing_ids"`
}

// MissingBlobsPayload contains blob SHA-256 hashes that a peer needs to receive.
type MissingBlobsPayload struct {
	MissingHashes []string `json:"missing_hashes"`
}

// BlobPullPayload requests a specific blob by hash.
type BlobPullPayload struct {
	Hash string `json:"hash"`
}

// BlobBroadcastPayload announces a newly uploaded blob to peers in real time.
type BlobBroadcastPayload struct {
	Hash string `json:"hash"`
	Size int64  `json:"size"`
}

// WriteFrame writes a length-prefixed frame to the writer:
// [4 bytes length (uint32 big-endian)] [1 byte msgType] [payload bytes]
func WriteFrame(w io.Writer, msgType byte, payload []byte) error {
	payloadLen := len(payload)
	if payloadLen > MaxFramePayloadSize {
		return ErrFrameTooLarge
	}

	frameLen := uint32(1 + payloadLen)
	header := make([]byte, 5)
	binary.BigEndian.PutUint32(header[0:4], frameLen)
	header[4] = msgType

	if _, err := w.Write(header); err != nil {
		return fmt.Errorf("write frame header: %w", err)
	}
	if payloadLen > 0 {
		if _, err := w.Write(payload); err != nil {
			return fmt.Errorf("write frame payload: %w", err)
		}
	}
	return nil
}

// ReadFrame reads a single frame from the reader:
// [4 bytes length (uint32 big-endian)] [1 byte msgType] [payload bytes]
func ReadFrame(r io.Reader) (byte, []byte, error) {
	header := make([]byte, 5)
	if _, err := io.ReadFull(r, header); err != nil {
		return 0, nil, err
	}

	frameLen := binary.BigEndian.Uint32(header[0:4])
	if frameLen < 1 {
		return 0, nil, ErrInvalidFrameLen
	}
	if frameLen-1 > MaxFramePayloadSize {
		return 0, nil, ErrFrameTooLarge
	}

	msgType := header[4]
	payloadLen := frameLen - 1
	payload := make([]byte, payloadLen)

	if payloadLen > 0 {
		if _, err := io.ReadFull(r, payload); err != nil {
			return 0, nil, fmt.Errorf("read frame payload: %w", err)
		}
	}

	return msgType, payload, nil
}
