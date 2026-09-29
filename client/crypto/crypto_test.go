package crypto

import (
	"bytes"
	"testing"
)

func TestHistoryKey(t *testing.T) {
	p, err := NewSeedPhrase()
	if err != nil || len(bytesFields(p)) != 24 {
		t.Fatalf("phrase %q %v", p, err)
	}
	k1, err := HistoryKey(p, "u1")
	if err != nil {
		t.Fatal(err)
	}
	k2, _ := HistoryKey("  "+string(bytes.ToUpper([]byte(p)))+" ", "u1")
	k3, _ := HistoryKey(p, "u2")
	if !bytes.Equal(k1, k2) || bytes.Equal(k1, k3) {
		t.Fatal("derivation not deterministic / not user-bound")
	}
	if _, err := HistoryKey("not a valid phrase", "u1"); err != ErrBadPhrase {
		t.Fatalf("bad phrase: %v", err)
	}
}

func TestSealOpen(t *testing.T) {
	k := NewKey()
	ct, _ := Seal(k, []byte("hello"), []byte("ad"))
	if pt, err := Open(k, ct, []byte("ad")); err != nil || string(pt) != "hello" {
		t.Fatal(err)
	}
	if _, err := Open(k, ct, []byte("other")); err == nil {
		t.Fatal("ad not authenticated")
	}
	if _, err := Open(NewKey(), ct, []byte("ad")); err == nil {
		t.Fatal("wrong key accepted")
	}
}

func bytesFields(s string) [][]byte { return bytes.Fields([]byte(s)) }
