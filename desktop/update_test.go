//go:build !android && !ios && !server

package main

import (
	"archive/zip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestVerifyManifest(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	body := []byte(`{"version":"1.0.7","build":7}`)
	sig := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, body)) + "\n")
	key := base64.StdEncoding.EncodeToString(pub)
	if err := verifyManifest(body, sig, key); err != nil {
		t.Fatal(err)
	}
	if verifyManifest([]byte(`{"version":"6.6.6","build":666}`), sig, key) == nil {
		t.Fatal("tampered manifest accepted")
	}
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	if verifyManifest(body, sig, base64.StdEncoding.EncodeToString(other)) == nil {
		t.Fatal("manifest signed by another key accepted")
	}
}

func TestStageAndInstall(t *testing.T) {
	dir := t.TempDir()
	if runtime.GOOS == "darwin" {
		target := filepath.Join(dir, "Kantra.app")
		os.MkdirAll(filepath.Join(target, "Contents", "MacOS"), 0o755)
		os.WriteFile(filepath.Join(target, "Contents", "MacOS", "kantra"), []byte("old"), 0o755)
		zp := filepath.Join(dir, "new.zip")
		f, _ := os.Create(zp)
		zw := zip.NewWriter(f)
		w, _ := zw.Create("Kantra.app/Contents/MacOS/kantra")
		w.Write([]byte("new"))
		zw.Close()
		f.Close()
		if err := stageAt(target, zp, "1.0.2"); err != nil {
			t.Fatal(err)
		}
		if !installStagedAt(target) {
			t.Fatal("not installed")
		}
		if b, _ := os.ReadFile(filepath.Join(target, "Contents", "MacOS", "kantra")); string(b) != "new" {
			t.Fatalf("got %q", b)
		}
		return
	}
	target := filepath.Join(dir, "kantra")
	os.WriteFile(target, []byte("old"), 0o755)
	dl := filepath.Join(dir, "download")
	os.WriteFile(dl, []byte("new"), 0o644)
	if installStagedAt(target) {
		t.Fatal("installed without a staged update")
	}
	if err := stageAt(target, dl, "1.0.2"); err != nil {
		t.Fatal(err)
	}
	if !installStagedAt(target) {
		t.Fatal("not installed")
	}
	if b, _ := os.ReadFile(target); string(b) != "new" {
		t.Fatalf("got %q", b)
	}
	if b, _ := os.ReadFile(target + ".old"); string(b) != "old" {
		t.Fatalf("old version not kept aside: %q", b)
	}
	if st, _ := os.Stat(target); st.Mode().Perm()&0o100 == 0 {
		t.Fatal("not executable")
	}
	if installStagedAt(target) {
		t.Fatal("installed twice")
	}
}

func TestUnzipRejectsEscapes(t *testing.T) {
	dir := t.TempDir()
	zp := filepath.Join(dir, "evil.zip")
	f, _ := os.Create(zp)
	zw := zip.NewWriter(f)
	w, _ := zw.Create("../../evil")
	w.Write([]byte("x"))
	zw.Close()
	f.Close()
	if unzipApp(zp, filepath.Join(dir, "out")) == nil {
		t.Fatal("path escape accepted")
	}
}

// The whole path: a signed release → checked, downloaded, staged; a
// tampered file or manifest is refused.
func TestCheckUpdateEndToEnd(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	files := map[string][]byte{}
	newApp := []byte("the new version of the app")
	sum := sha256.Sum256(newApp)
	plat := updatePlatform()
	man, _ := json.Marshal(UpdateManifest{Version: "1.0.9", Build: 9, Assets: map[string]UpdateAsset{
		plat: {Name: "Kantra-new", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(newApp))}}})
	files["latest.json"] = man
	files["latest.json.sig"] = []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, man)))
	files["Kantra-new"] = newApp
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := files[strings.TrimPrefix(r.URL.Path, "/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(b)
	}))
	defer srv.Close()
	t.Setenv("KANTRA_UPDATE_URL", srv.URL)

	oldBuild, oldKey := appBuild, updatePublicKey
	defer func() { appBuild, updatePublicKey = oldBuild, oldKey }()
	appBuild, updatePublicKey = 8, base64.StdEncoding.EncodeToString(pub)

	// Staging replaces the test binary itself: stage into a temp "app".
	target := filepath.Join(t.TempDir(), "kantra")
	os.WriteFile(target, []byte("old"), 0o755)
	m := &Messenger{hc: srv.Client(), dir: t.TempDir()}
	path, err := func() (string, error) {
		mf, err := m.fetchManifest(context.Background())
		if err != nil {
			return "", err
		}
		return m.downloadAsset(context.Background(), mf.Assets[plat])
	}()
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "darwin" {
		if err := stageAt(target, path, "1.0.9"); err != nil {
			t.Fatal(err)
		}
		if !installStagedAt(target) {
			t.Fatal("not installed")
		}
		if b, _ := os.ReadFile(target); string(b) != string(newApp) {
			t.Fatalf("got %q", b)
		}
	}

	// Tampered file: the hash in the signed manifest does not match.
	files["Kantra-new"] = []byte("malware!! same length as the..")[:len(newApp)]
	if _, err := m.downloadAsset(context.Background(), UpdateAsset{Name: "Kantra-new", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(newApp))}); err == nil {
		t.Fatal("tampered download accepted")
	}
	// Tampered manifest (a higher build, same signature): refused.
	files["latest.json"] = []byte(strings.Replace(string(man), `"build":9`, `"build":99`, 1))
	if _, err := m.fetchManifest(context.Background()); err == nil {
		t.Fatal("tampered manifest accepted")
	}
	// No update offered when the release is not newer.
	files["latest.json"] = man
	appBuild = 9
	m2 := &Messenger{hc: srv.Client(), dir: t.TempDir()}
	if err := m2.checkUpdate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st := m2.GetUpdate(); st.State != "none" {
		t.Fatalf("state %q", st.State)
	}
}
