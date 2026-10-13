package main

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiagLogRotatesAndReports(t *testing.T) {
	d := &diagLog{}
	l := log.New(d, "", 0)
	l.Print("before open")
	dir := t.TempDir()
	d.open(dir)
	line := strings.Repeat("x", 1000)
	for i := 0; i < 1500; i++ { // ~1.5 MB: one rotation
		l.Print(line)
	}
	l.Print("last line")
	if _, err := os.Stat(filepath.Join(dir, "logs", "kantra.log.1")); err != nil {
		t.Fatalf("no rotated log: %v", err)
	}
	tail := d.tail(reportTail)
	if !strings.HasSuffix(tail, "last line\n") || len(tail) > reportTail {
		t.Fatalf("tail: %d bytes, ends %q", len(tail), tail[max(0, len(tail)-20):])
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "logs", "kantra.log.1")); !strings.Contains(string(b), "before open") {
		t.Fatal("early lines lost")
	}
}
