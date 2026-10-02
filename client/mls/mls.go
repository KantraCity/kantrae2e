// Package mls is the cgo bridge to the Rust mls_ffi crate (mls-rs).
//
// Build the static library first: `make mls-ffi` (Linux/macOS:
// cargo build --release) or `make mls-ffi-windows` (Windows, GNU toolchain:
// cargo build --release --target x86_64-pc-windows-gnu). All MLS protocol
// logic stays in mls-rs; this package only marshals bytes and exposes a
// Go-friendly API.
package mls

/*
#cgo CFLAGS: -I${SRCDIR}/../../mls-ffi/include
#cgo linux LDFLAGS: ${SRCDIR}/../../mls-ffi/target/release/libmls_ffi.a -lm -ldl -lpthread
#cgo darwin LDFLAGS: ${SRCDIR}/../../mls-ffi/target/release/libmls_ffi.a -lm
#cgo windows LDFLAGS: ${SRCDIR}/../../mls-ffi/target/x86_64-pc-windows-gnu/release/libmls_ffi.a -lbcrypt -ladvapi32 -lkernel32 -lntdll -luserenv -lws2_32 -ldbghelp
#include <stdlib.h>
#include "mls_ffi.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"unsafe"
)

// ErrNotFound is returned when a group (or member) does not exist.
var ErrNotFound = errors.New("mls: not found")

// Message kinds returned by Process.
const (
	KindApplication = int(C.MLS_KIND_APPLICATION)
	KindCommit      = int(C.MLS_KIND_COMMIT)
	KindProposal    = int(C.MLS_KIND_PROPOSAL)
	KindOther       = int(C.MLS_KIND_OTHER)
)

// Wire formats returned by Inspect.
const (
	WirePrivate    = 1
	WirePublic     = 2
	WireWelcome    = 4
	WireKeyPackage = 5
)

type Error struct {
	Code int
	Msg  string
}

func (e *Error) Error() string { return fmt.Sprintf("mls: %s (code %d)", e.Msg, e.Code) }

func (e *Error) Is(target error) bool {
	return target == ErrNotFound && e.Code == int(C.MLS_ERR_NOT_FOUND)
}

func takeBuf(b C.MlsBuf) []byte {
	if b.ptr == nil {
		return nil
	}
	out := C.GoBytes(unsafe.Pointer(b.ptr), C.int(b.len))
	C.mls_buf_free(b)
	return out
}

func check(code C.int32_t, e C.MlsBuf) error {
	msg := takeBuf(e)
	if code == C.MLS_OK {
		return nil
	}
	return &Error{Code: int(code), Msg: string(msg)}
}

// cbytes copies b into C memory; the caller must free the result.
func cbytes(b []byte) (*C.uint8_t, C.size_t) {
	if len(b) == 0 {
		return nil, 0
	}
	return (*C.uint8_t)(C.CBytes(b)), C.size_t(len(b))
}

func free(p *C.uint8_t) {
	if p != nil {
		C.free(unsafe.Pointer(p))
	}
}

// GenerateSignatureKeyPair creates a device identity key pair.
func GenerateSignatureKeyPair() (secret, public []byte, err error) {
	var sk, pk, e C.MlsBuf
	code := C.mls_generate_signature_keypair(&sk, &pk, &e)
	secret, public = takeBuf(sk), takeBuf(pk)
	return secret, public, check(code, e)
}

// Inspect parses a message without any group state.
func Inspect(msg []byte) (wire int, epoch uint64, groupID []byte, err error) {
	p, n := cbytes(msg)
	defer free(p)
	var kind C.int32_t
	var ep C.uint64_t
	var gid, e C.MlsBuf
	code := C.mls_message_info(p, n, &kind, &ep, &gid, &e)
	groupID = takeBuf(gid)
	return int(kind), uint64(ep), groupID, check(code, e)
}

// Client is one device's MLS state. It is safe for concurrent use.
type Client struct {
	mu sync.Mutex
	c  *C.MlsClient
}

// NewClient creates an MLS client with a basic credential `identity`.
func NewClient(identity, secret, public []byte) (*Client, error) {
	ip, in := cbytes(identity)
	sp, sn := cbytes(secret)
	pp, pn := cbytes(public)
	defer free(ip)
	defer free(sp)
	defer free(pp)
	var c *C.MlsClient
	var e C.MlsBuf
	if err := check(C.mls_client_new(ip, in, sp, sn, pp, pn, &c, &e), e); err != nil {
		return nil, err
	}
	cl := &Client{c: c}
	runtime.SetFinalizer(cl, (*Client).Close)
	return cl, nil
}

func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.c != nil {
		C.mls_client_free(c.c)
		c.c = nil
	}
}

// LoadGroup restores a persisted group (see Changes for the source of rows).
func (c *Client) LoadGroup(groupID, state []byte, epochs []Epoch) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	gp, gn := cbytes(groupID)
	sp, sn := cbytes(state)
	ep, en := cbytes(encodeEpochs(epochs))
	defer free(gp)
	defer free(sp)
	defer free(ep)
	var e C.MlsBuf
	return check(C.mls_client_load_group(c.c, gp, gn, sp, sn, ep, en, &e), e)
}

// LoadKeyPackage restores the secrets of a not-yet-used KeyPackage.
func (c *Client) LoadKeyPackage(id, data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	ip, in := cbytes(id)
	dp, dn := cbytes(data)
	defer free(ip)
	defer free(dp)
	var e C.MlsBuf
	return check(C.mls_client_load_key_package(c.c, ip, in, dp, dn, &e), e)
}

// TakeChanges drains state mutations accumulated since the last call.
// Persist them atomically to survive restarts.
func (c *Client) TakeChanges() ([]Change, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out, e C.MlsBuf
	code := C.mls_client_take_changes(c.c, &out, &e)
	raw := takeBuf(out)
	if err := check(code, e); err != nil {
		return nil, err
	}
	return decodeChanges(raw)
}

// GenerateKeyPackage returns a serialized KeyPackage MLSMessage.
func (c *Client) GenerateKeyPackage() ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out, e C.MlsBuf
	code := C.mls_generate_key_package(c.c, &out, &e)
	kp := takeBuf(out)
	return kp, check(code, e)
}

// CreateGroup creates a one-member group at epoch 0.
func (c *Client) CreateGroup(groupID []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	gp, gn := cbytes(groupID)
	defer free(gp)
	var e C.MlsBuf
	return check(C.mls_create_group(c.c, gp, gn, &e), e)
}

// CreateCommit builds a pending Commit adding keyPackages and removing the
// members with the given credential identities. welcome is nil if nobody
// is added; groupInfo is the GroupInfo of the resulting epoch (allows
// External Commits). Call ApplyPendingCommit after the server accepted the
// commit or ClearPendingCommit if it was rejected.
func (c *Client) CreateCommit(groupID []byte, keyPackages, removeIdentities [][]byte) (commit, welcome, groupInfo []byte, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	gp, gn := cbytes(groupID)
	ap, an := cbytes(encodeList(keyPackages))
	rp, rn := cbytes(encodeList(removeIdentities))
	defer free(gp)
	defer free(ap)
	defer free(rp)
	var cm, wl, gi, e C.MlsBuf
	code := C.mls_create_commit(c.c, gp, gn, ap, an, rp, rn, &cm, &wl, &gi, &e)
	commit, welcome, groupInfo = takeBuf(cm), takeBuf(wl), takeBuf(gi)
	return commit, welcome, groupInfo, check(code, e)
}

// GroupInfoMessage returns the GroupInfo of the current epoch, allowing
// External Commits.
func (c *Client) GroupInfoMessage(groupID []byte) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	gp, gn := cbytes(groupID)
	defer free(gp)
	var out, e C.MlsBuf
	code := C.mls_group_info_message(c.c, gp, gn, &out, &e)
	gi := takeBuf(out)
	return gi, check(code, e)
}

// ExternalJoin joins a group by itself with an External Commit built from
// groupInfo. The group is already in the new epoch locally; if the server
// rejects the commit, call ForgetGroup and drop its persisted state.
func (c *Client) ExternalJoin(groupInfo []byte) (groupID, commit, newGroupInfo []byte, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ip, in := cbytes(groupInfo)
	defer free(ip)
	var g, cm, gi, e C.MlsBuf
	code := C.mls_external_join(c.c, ip, in, &g, &cm, &gi, &e)
	groupID, commit, newGroupInfo = takeBuf(g), takeBuf(cm), takeBuf(gi)
	return groupID, commit, newGroupInfo, check(code, e)
}

func (c *Client) ApplyPendingCommit(groupID []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	gp, gn := cbytes(groupID)
	defer free(gp)
	var e C.MlsBuf
	return check(C.mls_apply_pending_commit(c.c, gp, gn, &e), e)
}

func (c *Client) ClearPendingCommit(groupID []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	gp, gn := cbytes(groupID)
	defer free(gp)
	var e C.MlsBuf
	return check(C.mls_clear_pending_commit(c.c, gp, gn, &e), e)
}

// JoinGroup joins via a Welcome and returns the group id.
func (c *Client) JoinGroup(welcome []byte) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	wp, wn := cbytes(welcome)
	defer free(wp)
	var gid, e C.MlsBuf
	code := C.mls_join_group(c.c, wp, wn, &gid, &e)
	g := takeBuf(gid)
	return g, check(code, e)
}

// Encrypt produces an application message for the current epoch.
func (c *Client) Encrypt(groupID, plaintext []byte) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	gp, gn := cbytes(groupID)
	pp, pn := cbytes(plaintext)
	defer free(gp)
	defer free(pp)
	var out, e C.MlsBuf
	code := C.mls_encrypt_application_message(c.c, gp, gn, pp, pn, &out, &e)
	ct := takeBuf(out)
	return ct, check(code, e)
}

// Processed is the result of Process.
type Processed struct {
	Kind      int
	Plaintext []byte // application messages only
	Sender    []byte // credential identity of sender / committer
	Epoch     uint64 // group epoch after processing
	Removed   bool   // this commit removed us from the group
	External  bool   // External Commit: the sender joined by itself
}

// Process handles an incoming Commit, Proposal or application message.
func (c *Client) Process(groupID, msg []byte) (*Processed, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	gp, gn := cbytes(groupID)
	mp, mn := cbytes(msg)
	defer free(gp)
	defer free(mp)
	var out C.MlsProcessed
	var e C.MlsBuf
	code := C.mls_process_message(c.c, gp, gn, mp, mn, &out, &e)
	if err := check(code, e); err != nil {
		return nil, err
	}
	return &Processed{
		Kind:      int(out.kind),
		Plaintext: takeBuf(out.data),
		Sender:    takeBuf(out.sender),
		Epoch:     uint64(out.epoch),
		Removed:   out.removed != 0,
		External:  out.external != 0,
	}, nil
}

// GroupInfo returns the current epoch and member identities.
func (c *Client) GroupInfo(groupID []byte) (epoch uint64, members [][]byte, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	gp, gn := cbytes(groupID)
	defer free(gp)
	var ep C.uint64_t
	var m, e C.MlsBuf
	code := C.mls_group_info(c.c, gp, gn, &ep, &m, &e)
	raw := takeBuf(m)
	if err := check(code, e); err != nil {
		return 0, nil, err
	}
	members, err = decodeList(raw)
	return uint64(ep), members, err
}

// ForgetGroup drops a group from memory.
func (c *Client) ForgetGroup(groupID []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	gp, gn := cbytes(groupID)
	defer free(gp)
	var e C.MlsBuf
	return check(C.mls_forget_group(c.c, gp, gn, &e), e)
}

// KeyPackageIdentity returns the credential identity inside a KeyPackage.
func KeyPackageIdentity(kp []byte) ([]byte, error) {
	p, n := cbytes(kp)
	defer free(p)
	var id, e C.MlsBuf
	code := C.mls_key_package_identity(p, n, &id, &e)
	out := takeBuf(id)
	return out, check(code, e)
}

// Member is a group member with its signature public key.
type Member struct {
	Identity     []byte
	SignatureKey []byte
}

// Members lists the group members with their signature keys.
func (c *Client) Members(groupID []byte) ([]Member, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	gp, gn := cbytes(groupID)
	defer free(gp)
	var out, e C.MlsBuf
	code := C.mls_group_members(c.c, gp, gn, &out, &e)
	raw := takeBuf(out)
	if err := check(code, e); err != nil {
		return nil, err
	}
	items, err := decodeList(raw)
	if err != nil || len(items)%2 != 0 {
		return nil, errTruncated
	}
	ms := make([]Member, 0, len(items)/2)
	for i := 0; i < len(items); i += 2 {
		ms = append(ms, Member{Identity: items[i], SignatureKey: items[i+1]})
	}
	return ms, nil
}

// KeyPackageInfo returns the identity and signature key inside a KeyPackage.
func KeyPackageInfo(kp []byte) (identity, signatureKey []byte, err error) {
	p, n := cbytes(kp)
	defer free(p)
	var id, key, e C.MlsBuf
	code := C.mls_key_package_info(p, n, &id, &key, &e)
	identity, signatureKey = takeBuf(id), takeBuf(key)
	return identity, signatureKey, check(code, e)
}
