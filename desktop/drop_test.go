package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestDroppedFileServedOnce(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "Фото 1.jpg")
	if err := os.WriteFile(p, []byte("jpeg-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	files := registerDropped([]string{p, dir, filepath.Join(dir, "missing")})
	if len(files) != 1 || files[0].Name != "Фото 1.jpg" || files[0].Mime != "image/jpeg" || files[0].Size != 10 {
		t.Fatalf("files = %+v", files)
	}
	h := mediaMiddleware(nil)(nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", files[0].URL, nil))
	if rec.Code != 200 || rec.Body.String() != "jpeg-bytes" {
		t.Fatalf("first fetch: %d %q", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", files[0].URL, nil))
	if rec.Code != 404 {
		t.Fatalf("second fetch: %d, want 404", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", droppedPrefix+"guess", nil))
	if rec.Code != 404 {
		t.Fatalf("unknown token: %d", rec.Code)
	}
}
