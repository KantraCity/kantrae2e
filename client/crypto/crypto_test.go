package crypto

import (
	"bytes"
	"testing"
)

func TestRecoveryKeys(t *testing.T) {
	p, err := NewRecoveryPhrase()
	if err != nil || len(bytesFields(p)) != 12 {
		t.Fatalf("phrase %q %v", p, err)
	}
	w1, v1, err := RecoveryKeys(p, "u1")
	if err != nil {
		t.Fatal(err)
	}
	w2, v2, _ := RecoveryKeys("  "+string(bytes.ToUpper([]byte(p)))+" ", "u1")
	w3, _, _ := RecoveryKeys(p, "u2")
	if !bytes.Equal(w1, w2) || !bytes.Equal(v1, v2) || bytes.Equal(w1, w3) || bytes.Equal(w1, v1) {
		t.Fatal("derivation not deterministic / not user-bound / keys not independent")
	}
	if _, _, err := RecoveryKeys("not a valid phrase", "u1"); err != ErrBadPhrase {
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
