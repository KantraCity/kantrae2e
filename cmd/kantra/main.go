// Command kantra is the CLI frontend of the messenger client.
package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/kantracity/kantrae2e/client/core"
	"github.com/kantracity/kantrae2e/client/store"
)

const usage = `kantra — E2EE messenger CLI (MLS via mls-rs)

Usage: kantra [flags] <command> [args]

Account:
  register <username> [device-name]   create account + this device, prints seed phrase
  login <username> [device-name]      add this client as a new device (asks for seed phrase)
  relogin                             renew the session of this device
  whoami                              show account

Groups:
  create <name>                       create a group
  groups                              list groups
  invite <group> <username>...        add all devices of users
  remove <group> <username>           remove a user (all devices)
  members <group>                     list members

Messages:
  send <group> <text>...              send a text message
  sendfile <group> <path>             send an encrypted file
  download <group> <seq> [out-path]   download a received file
  history <group> [n]                 show last n messages (default 30)
  sync                                fetch pending messages once
  listen                              stay online and print incoming messages
  chat <group>                        interactive chat (lines from stdin)

History backup:
  backup                              upload new messages (encrypted with the seed key)
  restore                             restore history from the backup

<group> is a group name, id or id prefix.

Flags:
`

func main() {
	fs := flag.NewFlagSet("kantra", flag.ExitOnError)
	server := fs.String("server", os.Getenv("KANTRA_SERVER"), "gateway URL (env KANTRA_SERVER), e.g. https://localhost")
	dbPath := fs.String("db", envOr("KANTRA_DB", defaultDB()), "local database (env KANTRA_DB)")
	caFile := fs.String("ca", os.Getenv("KANTRA_CA"), "extra CA certificate (PEM) to trust, e.g. Caddy's local CA")
	insecure := fs.Bool("insecure", false, "skip TLS verification (development only)")
	verbose := fs.Bool("v", false, "verbose diagnostics")
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage); fs.PrintDefaults() }
	_ = fs.Parse(os.Args[1:])
	args := fs.Args()
	if len(args) == 0 {
		fs.Usage()
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	hc, err := httpClient(*caFile, *insecure)
	if err != nil {
		fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(*dbPath), 0o700); err != nil {
		fatal(err)
	}
	a := &app{out: os.Stdout}
	opts := core.Options{ServerURL: *server, DBPath: *dbPath, HTTPClient: hc, OnEvent: a.onEvent}
	if *verbose {
		opts.Logf = func(f string, args ...any) { fmt.Fprintf(os.Stderr, "· "+f+"\n", args...) }
	}
	a.c, err = core.Open(ctx, opts)
	if err != nil {
		fatal(err)
	}
	defer a.c.Close()
	if err := a.run(ctx, args[0], args[1:]); err != nil {
		fatal(err)
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func defaultDB() string {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "kantra", "kantra.db")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".kantra", "kantra.db")
}

func httpClient(caFile string, insecure bool) (*http.Client, error) {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.ForceAttemptHTTP2 = true
	cfg := &tls.Config{InsecureSkipVerify: insecure} //nolint:gosec // explicit dev flag
	if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, err
		}
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("no certificates in " + caFile)
		}
		cfg.RootCAs = pool
	}
	tr.TLSClientConfig = cfg
	return &http.Client{Transport: tr, Timeout: 60 * time.Second}, nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

type app struct {
	c   *core.Client
	out *os.File
	// live is set by listen/chat: only then incoming events are printed.
	live bool
	// chat mode: only print messages of this group
	focus string
}

func need(args []string, n int, what string) error {
	if len(args) < n {
		return fmt.Errorf("usage: kantra %s", what)
	}
	return nil
}

func readSecret(prompt string) (string, error) {
	if v := os.Getenv("KANTRA_PASSWORD"); v != "" && strings.Contains(prompt, "Password") {
		return v, nil
	}
	fmt.Fprint(os.Stderr, prompt)
	if term.IsTerminal(int(os.Stdin.Fd())) {
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		return string(b), err
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.TrimSpace(line), err
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "cli"
	}
	return h
}

func (a *app) run(ctx context.Context, cmd string, args []string) error {
	switch cmd {
	case "register":
		if err := need(args, 1, "register <username> [device-name]"); err != nil {
			return err
		}
		pw, err := readSecret("Password: ")
		if err != nil {
			return err
		}
		phrase, err := a.c.Register(ctx, args[0], pw, deviceName(args))
		if err != nil {
			return err
		}
		fmt.Fprintf(a.out, "Registered %s.\n\nSEED PHRASE — write it down, it is shown only once.\n"+
			"It is the only way to restore your message history on a new device:\n\n  %s\n\n", args[0], phrase)
		return nil

	case "login":
		if err := need(args, 1, "login <username> [device-name]"); err != nil {
			return err
		}
		pw, err := readSecret("Password: ")
		if err != nil {
			return err
		}
		phrase, err := readSecret("Seed phrase (empty to skip history): ")
		if err != nil {
			return err
		}
		if err := a.c.Login(ctx, args[0], pw, deviceName(args), phrase); err != nil {
			return err
		}
		fmt.Fprintln(a.out, "Logged in as a new device. Ask a group member to invite you again to rejoin groups.")
		if phrase != "" {
			fmt.Fprintln(a.out, "Run `kantra restore` to restore your history.")
		}
		return nil

	case "relogin":
		pw, err := readSecret("Password: ")
		if err != nil {
			return err
		}
		return a.c.Relogin(ctx, pw)

	case "whoami":
		acc := a.c.Account()
		if acc == nil {
			return core.ErrNotLoggedIn
		}
		fmt.Fprintf(a.out, "user:   %s (%s)\ndevice: %s\n", acc.Username, acc.UserID, acc.DeviceID)
		return nil

	case "create":
		if err := need(args, 1, "create <name>"); err != nil {
			return err
		}
		id, err := a.c.CreateGroup(ctx, strings.Join(args, " "))
		if err != nil {
			return err
		}
		fmt.Fprintln(a.out, "created group", id)
		return nil

	case "groups":
		if err := a.c.Sync(ctx); err != nil {
			fmt.Fprintln(os.Stderr, "warning: sync failed:", err)
		}
		gs, err := a.c.Groups(ctx)
		if err != nil {
			return err
		}
		for _, g := range gs {
			state := ""
			if !g.Active {
				state = " (inactive)"
			}
			fmt.Fprintf(a.out, "%s  %-20s epoch %d%s\n", g.ID[:8], g.Name, g.Epoch, state)
		}
		return nil

	case "invite":
		if err := need(args, 2, "invite <group> <username>..."); err != nil {
			return err
		}
		g, err := a.group(ctx, args[0])
		if err != nil {
			return err
		}
		return a.c.Invite(ctx, g.ID, args[1:]...)

	case "remove":
		if err := need(args, 2, "remove <group> <username>"); err != nil {
			return err
		}
		g, err := a.group(ctx, args[0])
		if err != nil {
			return err
		}
		return a.c.Remove(ctx, g.ID, args[1])

	case "members":
		if err := need(args, 1, "members <group>"); err != nil {
			return err
		}
		g, err := a.group(ctx, args[0])
		if err != nil {
			return err
		}
		ms, err := a.c.Members(ctx, g.ID)
		if err != nil {
			return err
		}
		for _, m := range ms {
			fmt.Fprintf(a.out, "%-20s device %s\n", m.Username, m.DeviceID)
		}
		return nil

	case "send":
		if err := need(args, 2, "send <group> <text>..."); err != nil {
			return err
		}
		g, err := a.group(ctx, args[0])
		if err != nil {
			return err
		}
		_, err = a.c.SendText(ctx, g.ID, strings.Join(args[1:], " "))
		return err

	case "sendfile":
		if err := need(args, 2, "sendfile <group> <path>"); err != nil {
			return err
		}
		g, err := a.group(ctx, args[0])
		if err != nil {
			return err
		}
		data, err := os.ReadFile(args[1])
		if err != nil {
			return err
		}
		_, err = a.c.SendFile(ctx, g.ID, filepath.Base(args[1]), data)
		return err

	case "download":
		if err := need(args, 2, "download <group> <seq> [out-path]"); err != nil {
			return err
		}
		g, err := a.group(ctx, args[0])
		if err != nil {
			return err
		}
		seq, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil {
			return err
		}
		ms, err := a.c.Messages(ctx, g.ID, 1_000_000)
		if err != nil {
			return err
		}
		for i := range ms {
			if ms[i].Seq != seq {
				continue
			}
			name, data, err := a.c.DownloadMedia(ctx, &ms[i])
			if err != nil {
				return err
			}
			out := filepath.Base(name)
			if len(args) > 2 {
				out = args[2]
			}
			if err := os.WriteFile(out, data, 0o600); err != nil {
				return err
			}
			fmt.Fprintf(a.out, "saved %s (%d bytes)\n", out, len(data))
			return nil
		}
		return fmt.Errorf("no message #%d", seq)

	case "history":
		if err := need(args, 1, "history <group> [n]"); err != nil {
			return err
		}
		n := 30
		if len(args) > 1 {
			if v, err := strconv.Atoi(args[1]); err == nil {
				n = v
			}
		}
		if err := a.c.Sync(ctx); err != nil {
			fmt.Fprintln(os.Stderr, "warning: sync failed:", err)
		}
		g, err := a.group(ctx, args[0])
		if err != nil {
			return err
		}
		ms, err := a.c.Messages(ctx, g.ID, n)
		if err != nil {
			return err
		}
		for i := range ms {
			a.print(ctx, &ms[i])
		}
		return nil

	case "sync":
		return a.c.Sync(ctx)

	case "listen":
		a.live = true
		fmt.Fprintln(os.Stderr, "listening, Ctrl-C to stop")
		return ignoreCancel(a.c.Listen(ctx))

	case "chat":
		if err := need(args, 1, "chat <group>"); err != nil {
			return err
		}
		g, err := a.group(ctx, args[0])
		if err != nil {
			return err
		}
		a.live, a.focus = true, g.ID
		ms, _ := a.c.Messages(ctx, g.ID, 20)
		for i := range ms {
			a.print(ctx, &ms[i])
		}
		go func() { _ = a.c.Listen(ctx) }()
		fmt.Fprintf(os.Stderr, "chatting in %q, type and press Enter; Ctrl-D to quit\n", g.Name)
		sc := bufio.NewScanner(os.Stdin)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			if _, err := a.c.SendText(ctx, g.ID, line); err != nil {
				fmt.Fprintln(os.Stderr, "send failed:", err)
			}
		}
		return nil

	case "backup":
		n, err := a.c.BackupHistory(ctx)
		if err != nil {
			return err
		}
		fmt.Fprintf(a.out, "backed up %d messages\n", n)
		return nil

	case "restore":
		n, err := a.c.RestoreHistory(ctx)
		if err != nil {
			return err
		}
		fmt.Fprintf(a.out, "restored %d messages\n", n)
		return nil
	}
	return fmt.Errorf("unknown command %q (see kantra -h)", cmd)
}

func deviceName(args []string) string {
	if len(args) > 1 {
		return strings.Join(args[1:], " ")
	}
	return hostname()
}

func ignoreCancel(err error) error {
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func (a *app) group(ctx context.Context, ref string) (*store.Group, error) {
	return a.c.ResolveGroup(ctx, ref)
}

func (a *app) print(ctx context.Context, m *store.Message) {
	who := "me"
	if !m.Outgoing {
		who = a.c.Username(ctx, m.SenderUser)
	}
	ts := time.UnixMilli(m.SentAt).Format("01-02 15:04")
	body := m.Body
	if ref, err := core.ParseMedia(m); err == nil {
		body = fmt.Sprintf("[file %s, %d bytes — kantra download <group> %d]", ref.Name, ref.Size, m.Seq)
	}
	fmt.Fprintf(a.out, "#%-4d %s  %-12s %s\n", m.Seq, ts, who, body)
}

func (a *app) onEvent(e core.Event) {
	if !a.live {
		return
	}
	ctx := context.Background()
	switch e.Type {
	case core.EventMessage:
		if a.focus == "" || a.focus == e.GroupID {
			if a.focus == "" {
				if g, err := a.c.ResolveGroup(ctx, e.GroupID); err == nil && g.Name != "" {
					fmt.Fprintf(a.out, "[%s] ", g.Name)
				}
			}
			// Re-read to get the local sequence number.
			if ms, err := a.c.Messages(ctx, e.GroupID, 50); err == nil {
				for i := len(ms) - 1; i >= 0; i-- {
					if ms[i].UID == e.Message.UID {
						a.print(ctx, &ms[i])
						return
					}
				}
			}
			a.print(ctx, e.Message)
		}
	case core.EventGroupJoined:
		fmt.Fprintf(os.Stderr, "* joined group %s\n", e.GroupID)
	case core.EventRemoved:
		fmt.Fprintf(os.Stderr, "* removed from group %s\n", e.GroupID)
	case core.EventDisconnected:
		fmt.Fprintf(os.Stderr, "* disconnected: %v\n", e.Err)
	}
}
