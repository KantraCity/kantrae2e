package core

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	_ "image/gif" // decoders for preview images
	"image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// FetchLinkPreview loads a page's title, description and image (Open Graph
// tags) for the preview card of a message. It runs on the sender's device
// only; the page sees this device's IP address, as with any visit.
func FetchLinkPreview(ctx context.Context, raw string) (*LinkPreview, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("not a web link")
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	hc := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 4 {
			return errors.New("too many redirects")
		}
		return nil
	}}
	page, final, err := get(ctx, hc, u.String(), 1<<20, "text/html")
	if err != nil {
		return nil, err
	}
	lp := &LinkPreview{URL: u.String(), Site: final.Hostname()}
	meta, title := parseMeta(page)
	lp.Title = clip(first(meta["og:title"], meta["twitter:title"], title), 200)
	lp.Desc = clip(first(meta["og:description"], meta["twitter:description"], meta["description"]), 300)
	if s := meta["og:site_name"]; s != "" {
		lp.Site = clip(s, 80)
	}
	if img := first(meta["og:image"], meta["og:image:url"], meta["twitter:image"]); img != "" {
		if iu, err := final.Parse(img); err == nil && (iu.Scheme == "http" || iu.Scheme == "https") {
			if data, _, err := get(ctx, hc, iu.String(), 4<<20, "image/"); err == nil {
				lp.Image = previewJPEG(data)
			}
		}
	}
	if lp.Title == "" && lp.Desc == "" && lp.Image == nil {
		return nil, errors.New("nothing to preview")
	}
	return lp, nil
}

func get(ctx context.Context, hc *http.Client, u string, limit int64, wantType string) ([]byte, *url.URL, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; KantraLinkPreview/1.0)")
	req.Header.Set("Accept-Language", "ru,en;q=0.8")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Content-Type"), wantType) {
		return nil, nil, errors.New("unexpected response")
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	return b, resp.Request.URL, err
}

var spaces = regexp.MustCompile(`\s+`)

// parseMeta collects <meta property/name=… content=…> and the <title>.
func parseMeta(page []byte) (map[string]string, string) {
	meta := map[string]string{}
	title := ""
	z := html.NewTokenizer(bytes.NewReader(page))
	inTitle := false
	for {
		switch z.Next() {
		case html.ErrorToken:
			return meta, title
		case html.StartTagToken, html.SelfClosingTagToken:
			t := z.Token()
			switch t.Data {
			case "meta":
				var key, content string
				for _, a := range t.Attr {
					switch strings.ToLower(a.Key) {
					case "property", "name":
						key = strings.ToLower(a.Val)
					case "content":
						content = a.Val
					}
				}
				if key != "" && meta[key] == "" {
					meta[key] = strings.TrimSpace(spaces.ReplaceAllString(content, " "))
				}
			case "title":
				inTitle = title == ""
			case "body":
				return meta, title // the head is enough
			}
		case html.TextToken:
			if inTitle {
				title = strings.TrimSpace(spaces.ReplaceAllString(string(z.Text()), " "))
				inTitle = false
			}
		}
	}
}

func first(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// previewJPEG scales an image to fit 320×320 and encodes it as a small JPEG.
func previewJPEG(data []byte) []byte {
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return nil
	}
	scale := min(1, 320/float64(max(w, h)))
	nw, nh := max(1, int(float64(w)*scale)), max(1, int(float64(h)*scale))
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	// Box filter: average the source pixels of each destination pixel.
	for y := 0; y < nh; y++ {
		y0, y1 := b.Min.Y+y*h/nh, b.Min.Y+max((y+1)*h/nh, y*h/nh+1)
		for x := 0; x < nw; x++ {
			x0, x1 := b.Min.X+x*w/nw, b.Min.X+max((x+1)*w/nw, x*w/nw+1)
			var r, g, bl, n uint32
			for sy := y0; sy < y1; sy += max(1, (y1-y0)/4) {
				for sx := x0; sx < x1; sx += max(1, (x1-x0)/4) {
					cr, cg, cb, _ := src.At(sx, sy).RGBA()
					r, g, bl, n = r+cr, g+cg, bl+cb, n+1
				}
			}
			dst.Set(x, y, color.RGBA{uint8(r / n >> 8), uint8(g / n >> 8), uint8(bl / n >> 8), 255})
		}
	}
	for q := 75; q >= 35; q -= 20 {
		var buf bytes.Buffer
		if jpeg.Encode(&buf, dst, &jpeg.Options{Quality: q}) == nil && buf.Len() <= MaxThumb {
			return buf.Bytes()
		}
	}
	return nil
}
