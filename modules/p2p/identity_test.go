package p2p

import (
	"testing"

	"github.com/libp2p/go-libp2p/core/peer"
)

func TestLoadOrCreateIdentityStable(t *testing.T) {
	dir := t.TempDir()
	k1, err := LoadOrCreateIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	k2, err := LoadOrCreateIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	id1, err := peer.IDFromPrivateKey(k1)
	if err != nil {
		t.Fatal(err)
	}
	id2, err := peer.IDFromPrivateKey(k2)
	if err != nil {
		t.Fatal(err)
	}
	if id1 != id2 {
		t.Fatalf("peer id changed across reload: %s vs %s", id1, id2)
	}
}
