package mediacache

import (
	"bytes"
	"os"
	"testing"
	"time"

	"github.com/kantracity/kantrae2e/pkg/blobstore"
)

func blob(b byte, n int) ([]byte, string) {
	d := bytes.Repeat([]byte{b}, n)
	return d, blobstore.Hash(d)
}

func TestCacheGetPutTrim(t *testing.T) {
	c, err := Open(t.TempDir(), 10_000)
	if err != nil {
		t.Fatal(err)
	}
	a, ha := blob('a', 2000)
	if _, ok := c.Get(ha); ok {
		t.Fatal("hit on empty cache")
	}
	c.Put(ha, a)
	if got, ok := c.Get(ha); !ok || !bytes.Equal(got, a) {
		t.Fatal("miss after put")
	}
	// Wrong content for a hash is refused, a corrupted file is dropped.
	c.Put(ha[:len(ha)-1]+"0", a)
	if _, ok := c.Get(ha[:len(ha)-1] + "0"); ok {
		t.Fatal("stored under a wrong hash")
	}
	os.WriteFile(c.path(ha), []byte("broken"), 0o600)
	if _, ok := c.Get(ha); ok {
		t.Fatal("corrupted file served")
	}

	// Fill above the limit: the least recently used go first.
	var hs []string
	for i := 0; i < 6; i++ {
		d, h := blob(byte('b'+i), 2000)
		c.Put(h, d)
		hs = append(hs, h)
		old := time.Now().Add(time.Duration(i-10) * time.Minute)
		os.Chtimes(c.path(h), old, old)
	}
	c.Get(hs[0]) // used just now
	c.Trim()
	if n, _ := c.Usage(); n > 9000 {
		t.Fatalf("usage %d after trim", n)
	}
	if _, ok := c.Get(hs[0]); !ok {
		t.Fatal("recently used file trimmed")
	}
	if _, ok := c.Get(hs[1]); ok {
		t.Fatal("oldest file kept")
	}
	if err := c.Clear(); err != nil {
		t.Fatal(err)
	}
	if n, f := c.Usage(); n != 0 || f != 0 {
		t.Fatalf("after clear: %d bytes, %d files", n, f)
	}
}
