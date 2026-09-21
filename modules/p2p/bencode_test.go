package p2p

import (
	"reflect"
	"testing"
)

func TestBencodePrimitives(t *testing.T) {
	// String
	strData, err := BencodeEncode("hello world")
	if err != nil {
		t.Fatalf("encode string: %v", err)
	}
	if string(strData) != "11:hello world" {
		t.Fatalf("expected 11:hello world, got %s", strData)
	}
	strDecoded, err := BencodeDecode(strData)
	if err != nil || strDecoded != "hello world" {
		t.Fatalf("decode string: got %v, err %v", strDecoded, err)
	}

	// Integer
	intData, err := BencodeEncode(42)
	if err != nil {
		t.Fatalf("encode int: %v", err)
	}
	if string(intData) != "i42e" {
		t.Fatalf("expected i42e, got %s", intData)
	}
	intDecoded, err := BencodeDecode(intData)
	if err != nil || intDecoded != int64(42) {
		t.Fatalf("decode int: got %v, err %v", intDecoded, err)
	}
}

func TestBencodeKRPCQuery(t *testing.T) {
	query := map[string]any{
		"t": "aa",
		"y": "q",
		"q": "ping",
		"a": map[string]any{
			"id": "abcdefghij0123456789",
		},
	}

	encoded, err := BencodeEncode(query)
	if err != nil {
		t.Fatalf("encode KRPC query: %v", err)
	}

	// Verify keys are sorted: d...a...q...t...y...e
	expected := "d1:ad2:id20:abcdefghij0123456789e1:q4:ping1:t2:aa1:y1:qe"
	if string(encoded) != expected {
		t.Fatalf("bencode KRPC mismatch:\ngot:  %s\nwant: %s", string(encoded), expected)
	}

	decoded, err := BencodeDecode(encoded)
	if err != nil {
		t.Fatalf("decode KRPC query: %v", err)
	}

	dict, ok := decoded.(map[string]any)
	if !ok {
		t.Fatalf("expected dictionary, got %T", decoded)
	}
	if dict["q"] != "ping" || dict["t"] != "aa" || dict["y"] != "q" {
		t.Fatalf("dict fields mismatch: %+v", dict)
	}

	args, ok := dict["a"].(map[string]any)
	if !ok || !reflect.DeepEqual(args["id"], "abcdefghij0123456789") {
		t.Fatalf("args mismatch: %+v", args)
	}
}
