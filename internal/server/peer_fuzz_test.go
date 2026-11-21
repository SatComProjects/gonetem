package server

import (
	"fmt"
	"testing"
)

func FuzzSplitPeer(f *testing.F) {
	for _, seed := range []string{
		"n1.0", "R1.12", "", ".", "n1", "n1.", ".0", "n1.-1",
		"a.b.c", "node.00", "node. 1", "node.+1",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, peer string) {
		name, index, err := splitPeer(peer)
		if err != nil {
			return
		}
		if name == "" || index < 0 {
			t.Fatalf("splitPeer(%q) accepted an invalid result: %q %d", peer, name, index)
		}

		normalized := fmt.Sprintf("%s.%d", name, index)
		name2, index2, err := splitPeer(normalized)
		if err != nil {
			t.Fatalf("splitPeer(%q) rejected its own normalized form: %v", peer, err)
		}
		if name2 != name || index2 != index {
			t.Fatalf("normalization changed %q: %q/%d -> %q/%d",
				peer, name, index, name2, index2)
		}
	})
}
