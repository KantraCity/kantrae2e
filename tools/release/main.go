// release prepares a GitHub release of the apps (used by CI): it hashes
// the files, writes latest.json and signs it with the update key, and
// writes the AltStore / SideStore source for the iPhone app.
//
//	UPDATE_SIGNING_KEY=<base64 seed or PEM> go run ./tools/release \
//	  -dir dist -version 1.0.42 -build 42 -repo owner/name
//	UPDATE_SIGNING_KEY=... go run ./tools/release -pubkey   # prints the public key
package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Platform keys as the apps ask for them (desktop/update.go) → file names.
var assets = map[string]string{
	"windows-amd64": "Kantra-windows-amd64.exe",
	"linux-amd64":   "Kantra-linux-amd64",
	"darwin-arm64":  "Kantra-macos-arm64.zip",
	"android-arm64": "Kantra-android-arm64.apk",
	"ios":           "Kantra.ipa",
}

type asset struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

func main() {
	dir := flag.String("dir", "dist", "release files")
	version := flag.String("version", "", "e.g. 1.0.42")
	build := flag.Int("build", 0, "build number (increases with every release)")
	repo := flag.String("repo", "", "owner/name")
	pubOnly := flag.Bool("pubkey", false, "print the public key of UPDATE_SIGNING_KEY")
	flag.Parse()

	key, err := signingKey(os.Getenv("UPDATE_SIGNING_KEY"))
	if err != nil {
		log.Fatal(err)
	}
	pub := base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey))
	if *pubOnly {
		fmt.Println(pub)
		return
	}
	if *version == "" || *build == 0 || *repo == "" {
		log.Fatal("-version, -build and -repo are required")
	}
	if want := os.Getenv("UPDATE_PUBLIC_KEY"); want != "" && want != pub {
		log.Fatal("UPDATE_SIGNING_KEY does not belong to UPDATE_PUBLIC_KEY (the key the apps trust)")
	}
	man := struct {
		Version string           `json:"version"`
		Build   int              `json:"build"`
		Date    string           `json:"date"`
		Assets  map[string]asset `json:"assets"`
	}{*version, *build, time.Now().UTC().Format(time.RFC3339), map[string]asset{}}
	for platform, name := range assets {
		a, err := hashFile(filepath.Join(*dir, name))
		if errors.Is(err, os.ErrNotExist) {
			log.Printf("%s: not in this release", name)
			continue
		}
		if err != nil {
			log.Fatal(err)
		}
		a.Name = name
		man.Assets[platform] = a
	}
	body, _ := json.MarshalIndent(man, "", "  ")
	must(os.WriteFile(filepath.Join(*dir, "latest.json"), body, 0o644))
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(key, body))
	must(os.WriteFile(filepath.Join(*dir, "latest.json.sig"), []byte(sig+"\n"), 0o644))

	// AltStore / SideStore source: add this URL there once, then it offers
	// every new build (and re-signs the app).
	if ipa, ok := man.Assets["ios"]; ok {
		src := map[string]any{
			"name":       "Kantra",
			"identifier": "city.kantra.source",
			"sourceURL":  "https://github.com/" + *repo + "/releases/latest/download/altstore.json",
			"apps": []map[string]any{{
				"name":                 "Kantra",
				"bundleIdentifier":     "city.kantra.app",
				"developerName":        "Kantra",
				"subtitle":             "Мессенджер со сквозным шифрованием",
				"localizedDescription": "Kantra — мессенджер со сквозным шифрованием (MLS) на вашем сервере.",
				"iconURL":              "https://raw.githubusercontent.com/" + *repo + "/HEAD/desktop/build/appicon.png",
				"versions": []map[string]any{{
					"version":      *version,
					"buildVersion": *version, // = CFBundleVersion (.github/preset-version.sh)
					"date":         man.Date,
					"downloadURL":  "https://github.com/" + *repo + "/releases/download/v" + *version + "/" + ipa.Name,
					"size":         ipa.Size,
				}},
			}},
		}
		b, _ := json.MarshalIndent(src, "", "  ")
		must(os.WriteFile(filepath.Join(*dir, "altstore.json"), b, 0o644))
	}
	fmt.Printf("release %s (build %d): %d apps, signed\n", *version, *build, len(man.Assets))
}

// signingKey reads a base64 Ed25519 seed (32 bytes) or a PKCS#8 PEM key.
func signingKey(s string) (ed25519.PrivateKey, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errors.New("UPDATE_SIGNING_KEY is not set")
	}
	if blk, _ := pem.Decode([]byte(s)); blk != nil {
		k, err := x509.ParsePKCS8PrivateKey(blk.Bytes)
		if err != nil {
			return nil, err
		}
		if ek, ok := k.(ed25519.PrivateKey); ok {
			return ek, nil
		}
		return nil, errors.New("not an Ed25519 key")
	}
	seed, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, errors.New("UPDATE_SIGNING_KEY: expected a base64 32-byte seed or a PEM key")
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

func hashFile(path string) (asset, error) {
	f, err := os.Open(path)
	if err != nil {
		return asset{}, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return asset{SHA256: hex.EncodeToString(h.Sum(nil)), Size: n}, err
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
