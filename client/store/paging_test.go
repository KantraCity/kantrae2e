package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
)

func TestMessagesBeforePages(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// 25 messages, several sharing a timestamp (order falls back to seq).
	if err := s.Update(ctx, func(tx *Tx) error {
		for i := 0; i < 25; i++ {
			if err := tx.InsertMessage(Message{UID: fmt.Sprint("m", i), GroupID: "g", SenderUser: "u", SenderDevice: "d",
				Kind: "text", Body: fmt.Sprint(i), SentAt: int64(1000 + i/3)}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	page, _ := s.Messages(ctx, "g", 10)
	var got []string
	for len(page) > 0 {
		for i := len(page) - 1; i >= 0; i-- {
			got = append(got, page[i].Body)
		}
		page, err = s.MessagesBefore(ctx, "g", page[0].SentAt, page[0].Seq, 10)
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(got) != 25 {
		t.Fatalf("got %d messages: %v", len(got), got)
	}
	for i, b := range got {
		if b != fmt.Sprint(24-i) {
			t.Fatalf("order broken at %d: %v", i, got)
		}
	}
}
