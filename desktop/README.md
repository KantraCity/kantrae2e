# Kantra desktop / web client

Wails v3 app (preset `svelte`, TypeScript) over the shared Go client core
(`client/core`: MLS via cgo → mls-rs, SQLite, history backup). One codebase,
two builds:

| Build | Command | Result |
|---|---|---|
| Desktop (window, WebKitGTK / WebView2 / WKWebView) | `wails3 task build` | `bin/kantra-desktop` |
| Web (browser, Wails server mode, `-tags server`) | `wails3 task build:server` | `bin/kantra-desktop-server` → http://localhost:8090 |

The frontend does not know which one it runs in: the generated bindings
(`frontend/bindings`) call the Go service `Messenger` either over the
webview IPC or over HTTP/WebSocket in server mode.

**Web mode holds your keys.** The server-mode process is your MLS client: it
stores this device's keys and decrypted messages. It listens on `localhost`
only; do not expose it to other people (that would break end-to-end
encryption). Each user runs their own.

## Requirements

- Go (see `go.mod`), Node 20+, `wails3` CLI: `go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.26`
- The Rust MLS library: `make mls-ffi` in the repository root
- Linux desktop build: `libgtk-4-dev libwebkitgtk-6.0-dev` (not needed for the web build)

## Run

```bash
make -C .. mls-ffi
wails3 task build:server
KANTRA_SERVER=https://localhost KANTRA_CA=../deploy/caddy-root.crt ./bin/kantra-desktop-server
# open http://localhost:8090

wails3 dev          # desktop with hot reload
```

Environment:

| Variable | Meaning |
|---|---|
| `KANTRA_SERVER` | default gateway URL shown on the login screen (`https://localhost`) |
| `KANTRA_DB` | profile database (default: user config dir `kantra/kantra.db`) |
| `KANTRA_CA` | extra root CA to trust (e.g. Caddy's dev CA from `make dev-ca`) |
| `KANTRA_INSECURE=1` | skip TLS verification (development only) |
| `WAILS_SERVER_PORT` / `WAILS_SERVER_HOST` | web mode listen address (default `localhost:8090`) |

## Windows

The MLS library is linked through cgo, so Windows builds use the Rust GNU
target and MinGW gcc. The resulting `kantra-desktop.exe` only depends on
system DLLs; the UI uses the WebView2 runtime that ships with Windows 10/11.

**Ready-made exe:** every CI run uploads `kantra-windows-amd64`
(GitHub → Actions → run → Artifacts).

**Cross-compile on Linux:**

```bash
sudo apt install gcc-mingw-w64-x86-64
rustup target add x86_64-pc-windows-gnu
make desktop-windows        # -> desktop/bin/kantra-desktop.exe
```

**Build on Windows** (PowerShell, then an MSYS2 "MINGW64" shell):

```powershell
winget install GoLang.Go Rustlang.Rustup OpenJS.NodeJS.LTS MSYS2.MSYS2 Git.Git
rustup target add x86_64-pc-windows-gnu
```

```bash
# MSYS2 MINGW64 shell
pacman -S --needed mingw-w64-x86_64-gcc make
export PATH="$PATH:/c/Program Files/Go/bin:$HOME/go/bin:$USERPROFILE/.cargo/bin"
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.26
cd kantrae2e/mls-ffi && cargo build --release --target x86_64-pc-windows-gnu
cd ../desktop && CGO_ENABLED=1 wails3 task build CGO_ENABLED=1   # -> bin/kantra-desktop.exe
```

**Run against a local dev server** (Caddy's self-signed CA): either trust it
once — `certutil -addstore -user Root caddy-root.crt` — or start with
`$env:KANTRA_CA="C:\path\caddy-root.crt"; .\kantra-desktop.exe`. A server with
a real domain (Let's Encrypt) needs neither: just enter its address on the
login screen. The profile lives in `%AppData%\kantra\kantra.db`.

## Structure

- `messenger.go` — the bound service (typed methods + `kantra` event stream)
- `frontend/src/lib/app.svelte.ts` — UI state (Svelte 5 runes), event handling
- `frontend/src/components/` — Telegram-dark-style UI: auth + seed phrase,
  chat list, chat with bubbles/files/drag&drop, group info with key
  verification status, fingerprint comparison, settings (devices, backup)
