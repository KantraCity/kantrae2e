package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/kantracity/kantrae2e/client/core"
)

// errSaveUnsupported: the UI downloads the file through the browser instead.
var errSaveUnsupported = errors.New("save: use the browser download")

// errSaveShared: the system share sheet took the file (iPhone: "Сохранить
// изображение", "Сохранить в Файлы"); the UI shows nothing more.
var errSaveShared = errors.New("save: handed to the share sheet")

// saveResult maps platformSaveMedia's answer to what the UI expects: a place
// to show in a toast, "share" (the share sheet is open) or "" (let the
// browser download it).
func saveResult(where string, err error) (string, error) {
	switch {
	case errors.Is(err, errSaveUnsupported):
		return "", nil
	case errors.Is(err, errSaveShared):
		return "share", nil
	}
	return where, err
}

// SaveBytes saves a file made by the UI (the recovery key as .txt) the same
// way as SaveMedia.
func (m *Messenger) SaveBytes(name, mime string, data []byte) (string, error) {
	return saveResult(platformSaveMedia(safeName(name, mime), mime, data))
}

// OpenURL opens a web link outside the app (the system browser), so the
// app's own page never navigates away. Web mode: the browser opens it.
func (m *Messenger) OpenURL(link string) error {
	u, err := url.Parse(link)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http" && u.Scheme != "mailto" && u.Scheme != "tel") {
		return errors.New("ссылка не поддерживается")
	}
	return platformOpenURL(u.String())
}

// SaveMedia saves a message's file to the gallery (phones) or the Downloads
// folder (desktop). It returns where it went; "" means the UI should let
// the browser download it (web mode).
func (m *Messenger) SaveMedia(chatID, id string) (string, error) {
	c, err := m.loggedIn()
	if err != nil {
		return "", err
	}
	ctx := context.Background()
	s, err := c.Message(ctx, chatID, id)
	if err != nil {
		return "", err
	}
	ref, err := core.ParseMedia(s)
	if err != nil {
		return "", errors.New("в сообщении нет файла")
	}
	_, data, err := c.DownloadMedia(ctx, s)
	if err != nil {
		return "", friendly(err)
	}
	name := safeName(ref.Name, ref.Mime)
	return saveResult(platformSaveMedia(name, ref.Mime, data))
}

func safeName(name, mime string) string {
	name = filepath.Base(strings.TrimSpace(name))
	name = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`<>:"/\|?*`, r) || r < 32 {
			return '_'
		}
		return r
	}, name)
	if name == "" || name == "." || name == ".." {
		ext := ".bin"
		switch {
		case strings.HasPrefix(mime, "image/jpeg"):
			ext = ".jpg"
		case strings.HasPrefix(mime, "image/png"):
			ext = ".png"
		case strings.HasPrefix(mime, "image/gif"):
			ext = ".gif"
		case strings.HasPrefix(mime, "video/"):
			ext = ".mp4"
		case strings.HasPrefix(mime, "audio/"):
			ext = ".ogg"
		}
		name = "kantra" + ext
	}
	return name
}

// saveToDownloads writes a file into ~/Downloads/Kantra without replacing
// an existing one ("name (1).ext").
func saveToDownloads(name string, data []byte) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, "Downloads", "Kantra")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	path := filepath.Join(dir, name)
	for i := 1; ; i++ {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, os.ErrExist) {
			path = filepath.Join(dir, fmt.Sprintf("%s (%d)%s", base, i, ext))
			continue
		}
		if err != nil {
			return "", err
		}
		if _, err := f.Write(data); err != nil {
			f.Close()
			return "", err
		}
		return "Сохранено: " + path, f.Close()
	}
}
