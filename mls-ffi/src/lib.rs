//! Thin C ABI over `mls-rs` for the Go client (cgo).
//!
//! Design notes:
//! * All MLS logic lives in `mls-rs`; this crate only marshals bytes.
//! * State (group state, prior epochs, key package secrets) is kept in an
//!   in-memory store inside the client handle. Every mutation is also appended
//!   to a change log that the host (Go) drains with `mls_client_take_changes`
//!   and persists (SQLite). On startup the host feeds persisted rows back with
//!   `mls_client_load_group` / `mls_client_load_key_package`.
//! * Errors are returned as a non-zero status code plus a UTF-8 message in the
//!   `out_err` buffer (no thread-locals: goroutines can migrate between OS
//!   threads between two cgo calls).
//! * Every buffer handed out by Rust must be released with `mls_buf_free`.

mod codec;
mod store;

use std::collections::HashMap;
use std::panic::{catch_unwind, AssertUnwindSafe};
use std::sync::{Arc, Mutex};

use mls_rs::client_builder::{
    BaseConfig, IntoConfigOutput, WithCryptoProvider, WithGroupStateStorage, WithIdentityProvider,
    WithKeyPackageRepo,
};
use mls_rs::group::{CommitEffect, Group, ReceivedMessage};
use mls_rs::identity::basic::{BasicCredential, BasicIdentityProvider};
use mls_rs::identity::SigningIdentity;
use mls_rs::{CipherSuite, CipherSuiteProvider, Client, CryptoProvider, MlsMessage};
use mls_rs_core::crypto::{SignaturePublicKey, SignatureSecretKey};
use mls_rs_crypto_rustcrypto::RustCryptoProvider;

use codec::Reader;
use store::{GroupStore, KeyPackageStore, Store};

const CIPHERSUITE: CipherSuite = CipherSuite::CURVE25519_AES128;

type Config = IntoConfigOutput<
    WithCryptoProvider<
        RustCryptoProvider,
        WithIdentityProvider<
            BasicIdentityProvider,
            WithKeyPackageRepo<KeyPackageStore, WithGroupStateStorage<GroupStore, BaseConfig>>,
        >,
    >,
>;

/// Status codes returned by every fallible function.
pub const MLS_OK: i32 = 0;
pub const MLS_ERR: i32 = 1;
pub const MLS_ERR_NOT_FOUND: i32 = 2;
pub const MLS_ERR_PANIC: i32 = 99;

/// Kinds reported by `mls_process_message`.
pub const MLS_KIND_APPLICATION: i32 = 1;
pub const MLS_KIND_COMMIT: i32 = 2;
pub const MLS_KIND_PROPOSAL: i32 = 3;
pub const MLS_KIND_OTHER: i32 = 4;

/// Owned byte buffer allocated by Rust.
#[repr(C)]
pub struct MlsBuf {
    pub ptr: *mut u8,
    pub len: usize,
    pub cap: usize,
}

impl MlsBuf {
    fn from_vec(v: Vec<u8>) -> Self {
        let mut v = std::mem::ManuallyDrop::new(v);
        MlsBuf { ptr: v.as_mut_ptr(), len: v.len(), cap: v.capacity() }
    }
}

#[repr(C)]
pub struct MlsProcessed {
    pub kind: i32,
    /// Plaintext for application messages, empty otherwise.
    pub data: MlsBuf,
    /// Credential identity of the sender / committer (may be empty).
    pub sender: MlsBuf,
    /// Group epoch after processing.
    pub epoch: u64,
    /// 1 if this commit removed the local member from the group.
    pub removed: i32,
}

pub struct MlsClient {
    inner: Mutex<Inner>,
}

struct Inner {
    client: Client<Config>,
    store: Arc<Mutex<Store>>,
    groups: HashMap<Vec<u8>, Group<Config>>,
}

type Res<T> = Result<T, (i32, String)>;

fn err<E: std::fmt::Debug>(e: E) -> (i32, String) {
    (MLS_ERR, format!("{e:?}"))
}

unsafe fn slice<'a>(ptr: *const u8, len: usize) -> &'a [u8] {
    if ptr.is_null() || len == 0 {
        &[]
    } else {
        std::slice::from_raw_parts(ptr, len)
    }
}

unsafe fn put(out: *mut MlsBuf, v: Vec<u8>) {
    if !out.is_null() {
        *out = MlsBuf::from_vec(v);
    }
}

/// Runs `f`, converting errors and panics into a status code + message.
fn guard<F: FnOnce() -> Res<()>>(out_err: *mut MlsBuf, f: F) -> i32 {
    let (code, msg) = match catch_unwind(AssertUnwindSafe(f)) {
        Ok(Ok(())) => return MLS_OK,
        Ok(Err(e)) => e,
        Err(p) => {
            let msg = p
                .downcast_ref::<&str>()
                .map(|s| s.to_string())
                .or_else(|| p.downcast_ref::<String>().cloned())
                .unwrap_or_else(|| "panic".into());
            (MLS_ERR_PANIC, msg)
        }
    };
    unsafe { put(out_err, msg.into_bytes()) };
    code
}

fn with_client<T>(c: *mut MlsClient, f: impl FnOnce(&mut Inner) -> Res<T>) -> Res<T> {
    if c.is_null() {
        return Err((MLS_ERR, "null client".into()));
    }
    let client = unsafe { &*c };
    let mut inner = client.inner.lock().map_err(|_| (MLS_ERR, "poisoned lock".to_string()))?;
    f(&mut inner)
}

impl Inner {
    fn group(&mut self, gid: &[u8]) -> Res<&mut Group<Config>> {
        if !self.groups.contains_key(gid) {
            let g = self.client.load_group(gid).map_err(|e| match e {
                mls_rs::error::MlsError::GroupNotFound => {
                    (MLS_ERR_NOT_FOUND, "group not found".to_string())
                }
                e => err(e),
            })?;
            self.groups.insert(gid.to_vec(), g);
        }
        Ok(self.groups.get_mut(gid).unwrap())
    }
}

fn identity_of(si: &SigningIdentity) -> Vec<u8> {
    si.credential
        .as_basic()
        .map(|b| b.identifier.clone())
        .unwrap_or_default()
}

// ---------------------------------------------------------------------------
// Buffers
// ---------------------------------------------------------------------------

#[no_mangle]
pub extern "C" fn mls_buf_free(buf: MlsBuf) {
    if !buf.ptr.is_null() {
        unsafe { drop(Vec::from_raw_parts(buf.ptr, buf.len, buf.cap)) };
    }
}

// ---------------------------------------------------------------------------
// Client lifecycle and persistence
// ---------------------------------------------------------------------------

/// Generates a new signature key pair for the configured cipher suite.
#[no_mangle]
pub extern "C" fn mls_generate_signature_keypair(
    out_secret: *mut MlsBuf,
    out_public: *mut MlsBuf,
    out_err: *mut MlsBuf,
) -> i32 {
    guard(out_err, || {
        let cs = RustCryptoProvider::default()
            .cipher_suite_provider(CIPHERSUITE)
            .ok_or((MLS_ERR, "unsupported cipher suite".to_string()))?;
        let (sk, pk) = cs.signature_key_generate().map_err(err)?;
        unsafe {
            put(out_secret, sk.as_bytes().to_vec());
            put(out_public, pk.as_bytes().to_vec());
        }
        Ok(())
    })
}

/// Creates a client for one device. `identity` becomes the basic credential.
#[no_mangle]
pub extern "C" fn mls_client_new(
    identity: *const u8,
    identity_len: usize,
    secret: *const u8,
    secret_len: usize,
    public: *const u8,
    public_len: usize,
    out_client: *mut *mut MlsClient,
    out_err: *mut MlsBuf,
) -> i32 {
    guard(out_err, || {
        let (identity, sk, pk) = unsafe {
            (
                slice(identity, identity_len).to_vec(),
                slice(secret, secret_len).to_vec(),
                slice(public, public_len).to_vec(),
            )
        };
        let store = Arc::new(Mutex::new(Store::default()));
        let signing_identity = SigningIdentity::new(
            BasicCredential::new(identity).into_credential(),
            SignaturePublicKey::new(pk),
        );
        let client = Client::builder()
            .group_state_storage(GroupStore(store.clone()))
            .key_package_repo(KeyPackageStore(store.clone()))
            .identity_provider(BasicIdentityProvider)
            .crypto_provider(RustCryptoProvider::default())
            .signing_identity(signing_identity, SignatureSecretKey::new(sk), CIPHERSUITE)
            .build();
        let handle = Box::new(MlsClient {
            inner: Mutex::new(Inner { client, store, groups: HashMap::new() }),
        });
        unsafe { *out_client = Box::into_raw(handle) };
        Ok(())
    })
}

#[no_mangle]
pub extern "C" fn mls_client_free(c: *mut MlsClient) {
    if !c.is_null() {
        unsafe { drop(Box::from_raw(c)) };
    }
}

/// Loads a persisted group. `epochs` is encoded as
/// `u32 count, (u64 epoch_id, bytes data)*` (see codec.rs).
#[no_mangle]
pub extern "C" fn mls_client_load_group(
    c: *mut MlsClient,
    gid: *const u8,
    gid_len: usize,
    state: *const u8,
    state_len: usize,
    epochs: *const u8,
    epochs_len: usize,
    out_err: *mut MlsBuf,
) -> i32 {
    guard(out_err, || {
        let (gid, state, epochs) =
            unsafe { (slice(gid, gid_len), slice(state, state_len), slice(epochs, epochs_len)) };
        let mut r = Reader::new(epochs);
        let n = if epochs.is_empty() { 0 } else { r.u32().map_err(err)? };
        let mut list = Vec::with_capacity(n as usize);
        for _ in 0..n {
            list.push((r.u64().map_err(err)?, r.bytes().map_err(err)?.to_vec()));
        }
        with_client(c, |inner| {
            inner.groups.remove(gid);
            inner.store.lock().unwrap().load_group(gid.to_vec(), state.to_vec(), list);
            Ok(())
        })
    })
}

#[no_mangle]
pub extern "C" fn mls_client_load_key_package(
    c: *mut MlsClient,
    id: *const u8,
    id_len: usize,
    data: *const u8,
    data_len: usize,
    out_err: *mut MlsBuf,
) -> i32 {
    guard(out_err, || {
        let (id, data) = unsafe { (slice(id, id_len), slice(data, data_len)) };
        with_client(c, |inner| {
            inner.store.lock().unwrap().load_key_package(id.to_vec(), data.to_vec());
            Ok(())
        })
    })
}

/// Drains the change log accumulated since the previous call.
#[no_mangle]
pub extern "C" fn mls_client_take_changes(
    c: *mut MlsClient,
    out: *mut MlsBuf,
    out_err: *mut MlsBuf,
) -> i32 {
    guard(out_err, || {
        with_client(c, |inner| {
            let data = inner.store.lock().unwrap().take_changes();
            unsafe { put(out, data) };
            Ok(())
        })
    })
}

// ---------------------------------------------------------------------------
// MLS operations
// ---------------------------------------------------------------------------

/// Generates one KeyPackage (MLSMessage encoding). Its secrets are stored.
#[no_mangle]
pub extern "C" fn mls_generate_key_package(
    c: *mut MlsClient,
    out: *mut MlsBuf,
    out_err: *mut MlsBuf,
) -> i32 {
    guard(out_err, || {
        with_client(c, |inner| {
            let kp = inner
                .client
                .generate_key_package_message(Default::default(), Default::default(), None)
                .map_err(err)?;
            unsafe { put(out, kp.to_bytes().map_err(err)?) };
            Ok(())
        })
    })
}

/// Creates a new group (epoch 0) with the given id, containing only us.
#[no_mangle]
pub extern "C" fn mls_create_group(
    c: *mut MlsClient,
    gid: *const u8,
    gid_len: usize,
    out_err: *mut MlsBuf,
) -> i32 {
    guard(out_err, || {
        let gid = unsafe { slice(gid, gid_len) }.to_vec();
        with_client(c, |inner| {
            let mut g = inner
                .client
                .create_group_with_id(gid.clone(), Default::default(), Default::default(), None)
                .map_err(err)?;
            g.write_to_storage().map_err(err)?;
            inner.groups.insert(gid, g);
            Ok(())
        })
    })
}

/// Builds a Commit adding `add_key_packages` and removing members whose
/// credential identity is in `remove_identities` (both length-prefixed lists).
///
/// The commit stays *pending*: call `mls_apply_pending_commit` once the
/// delivery service accepted it, or `mls_clear_pending_commit` on conflict.
/// `out_welcome` is empty when nobody is added.
#[no_mangle]
pub extern "C" fn mls_create_commit(
    c: *mut MlsClient,
    gid: *const u8,
    gid_len: usize,
    add_key_packages: *const u8,
    add_len: usize,
    remove_identities: *const u8,
    remove_len: usize,
    out_commit: *mut MlsBuf,
    out_welcome: *mut MlsBuf,
    out_err: *mut MlsBuf,
) -> i32 {
    guard(out_err, || {
        let (gid, adds, removes) = unsafe {
            (
                slice(gid, gid_len),
                codec::decode_list(slice(add_key_packages, add_len)).map_err(err)?,
                codec::decode_list(slice(remove_identities, remove_len)).map_err(err)?,
            )
        };
        with_client(c, |inner| {
            let g = inner.group(gid)?;
            let mut remove_idx = Vec::new();
            for id in &removes {
                let m = g
                    .roster()
                    .members_iter()
                    .find(|m| identity_of(&m.signing_identity) == *id)
                    .ok_or_else(|| (MLS_ERR_NOT_FOUND, "member to remove not in group".to_string()))?;
                remove_idx.push(m.index);
            }
            let mut b = g.commit_builder();
            for kp in adds {
                b = b.add_member(MlsMessage::from_bytes(&kp).map_err(err)?).map_err(err)?;
            }
            for idx in remove_idx {
                b = b.remove_member(idx).map_err(err)?;
            }
            let out = b.build().map_err(err)?;
            let welcome = match out.welcome_messages.first() {
                Some(w) => w.to_bytes().map_err(err)?,
                None => Vec::new(),
            };
            unsafe {
                put(out_commit, out.commit_message.to_bytes().map_err(err)?);
                put(out_welcome, welcome);
            }
            Ok(())
        })
    })
}

#[no_mangle]
pub extern "C" fn mls_apply_pending_commit(
    c: *mut MlsClient,
    gid: *const u8,
    gid_len: usize,
    out_err: *mut MlsBuf,
) -> i32 {
    guard(out_err, || {
        let gid = unsafe { slice(gid, gid_len) };
        with_client(c, |inner| {
            let g = inner.group(gid)?;
            g.apply_pending_commit().map_err(err)?;
            g.write_to_storage().map_err(err)?;
            Ok(())
        })
    })
}

#[no_mangle]
pub extern "C" fn mls_clear_pending_commit(
    c: *mut MlsClient,
    gid: *const u8,
    gid_len: usize,
    out_err: *mut MlsBuf,
) -> i32 {
    guard(out_err, || {
        let gid = unsafe { slice(gid, gid_len) };
        with_client(c, |inner| {
            inner.group(gid)?.clear_pending_commit();
            Ok(())
        })
    })
}

/// Joins a group from a Welcome message; returns the group id.
#[no_mangle]
pub extern "C" fn mls_join_group(
    c: *mut MlsClient,
    welcome: *const u8,
    welcome_len: usize,
    out_gid: *mut MlsBuf,
    out_err: *mut MlsBuf,
) -> i32 {
    guard(out_err, || {
        let welcome = MlsMessage::from_bytes(unsafe { slice(welcome, welcome_len) }).map_err(err)?;
        with_client(c, |inner| {
            let (mut g, _) = inner.client.join_group(None, &welcome, None).map_err(err)?;
            g.write_to_storage().map_err(err)?;
            let gid = g.group_id().to_vec();
            inner.groups.insert(gid.clone(), g);
            unsafe { put(out_gid, gid) };
            Ok(())
        })
    })
}

#[no_mangle]
pub extern "C" fn mls_encrypt_application_message(
    c: *mut MlsClient,
    gid: *const u8,
    gid_len: usize,
    plaintext: *const u8,
    plaintext_len: usize,
    out: *mut MlsBuf,
    out_err: *mut MlsBuf,
) -> i32 {
    guard(out_err, || {
        let (gid, pt) = unsafe { (slice(gid, gid_len), slice(plaintext, plaintext_len)) };
        with_client(c, |inner| {
            let g = inner.group(gid)?;
            let msg = g.encrypt_application_message(pt, Vec::new()).map_err(err)?;
            // Persist the advanced secret tree so keys are never reused.
            g.write_to_storage().map_err(err)?;
            unsafe { put(out, msg.to_bytes().map_err(err)?) };
            Ok(())
        })
    })
}

/// Processes any incoming group message: decrypts application messages,
/// applies commits, caches proposals.
#[no_mangle]
pub extern "C" fn mls_process_message(
    c: *mut MlsClient,
    gid: *const u8,
    gid_len: usize,
    msg: *const u8,
    msg_len: usize,
    out: *mut MlsProcessed,
    out_err: *mut MlsBuf,
) -> i32 {
    guard(out_err, || {
        let (gid, msg) = unsafe { (slice(gid, gid_len), slice(msg, msg_len)) };
        let msg = MlsMessage::from_bytes(msg).map_err(err)?;
        with_client(c, |inner| {
            let g = inner.group(gid)?;
            let received = g.process_incoming_message(msg).map_err(err)?;
            let sender_id = |g: &Group<Config>, idx: u32| {
                g.member_at_index(idx).map(|m| identity_of(&m.signing_identity)).unwrap_or_default()
            };
            let (kind, data, sender, removed) = match received {
                ReceivedMessage::ApplicationMessage(m) => {
                    (MLS_KIND_APPLICATION, m.data().to_vec(), sender_id(g, m.sender_index), 0)
                }
                ReceivedMessage::Commit(cd) => {
                    let removed = matches!(cd.effect, CommitEffect::Removed { .. }) as i32;
                    (MLS_KIND_COMMIT, Vec::new(), sender_id(g, cd.committer), removed)
                }
                ReceivedMessage::Proposal(_) => (MLS_KIND_PROPOSAL, Vec::new(), Vec::new(), 0),
                _ => (MLS_KIND_OTHER, Vec::new(), Vec::new(), 0),
            };
            g.write_to_storage().map_err(err)?;
            let epoch = g.current_epoch();
            unsafe {
                if !out.is_null() {
                    *out = MlsProcessed {
                        kind,
                        data: MlsBuf::from_vec(data),
                        sender: MlsBuf::from_vec(sender),
                        epoch,
                        removed,
                    };
                }
            }
            Ok(())
        })
    })
}

/// Current epoch and member identities (length-prefixed list) of a group.
#[no_mangle]
pub extern "C" fn mls_group_info(
    c: *mut MlsClient,
    gid: *const u8,
    gid_len: usize,
    out_epoch: *mut u64,
    out_members: *mut MlsBuf,
    out_err: *mut MlsBuf,
) -> i32 {
    guard(out_err, || {
        let gid = unsafe { slice(gid, gid_len) };
        with_client(c, |inner| {
            let g = inner.group(gid)?;
            let members: Vec<Vec<u8>> =
                g.roster().members_iter().map(|m| identity_of(&m.signing_identity)).collect();
            unsafe {
                if !out_epoch.is_null() {
                    *out_epoch = g.current_epoch();
                }
                put(out_members, codec::encode_list(&members));
            }
            Ok(())
        })
    })
}

/// Forgets a group in memory (the host deletes persisted rows itself).
#[no_mangle]
pub extern "C" fn mls_forget_group(
    c: *mut MlsClient,
    gid: *const u8,
    gid_len: usize,
    out_err: *mut MlsBuf,
) -> i32 {
    guard(out_err, || {
        let gid = unsafe { slice(gid, gid_len) };
        with_client(c, |inner| {
            inner.groups.remove(gid);
            inner.store.lock().unwrap().forget_group(gid);
            Ok(())
        })
    })
}

/// Stateless inspection of an MLS message (for routing on the client).
/// kind: 1 application/private, 2 commit/public, 4 welcome, 5 key package, 0 other.
#[no_mangle]
pub extern "C" fn mls_message_info(
    msg: *const u8,
    msg_len: usize,
    out_kind: *mut i32,
    out_epoch: *mut u64,
    out_gid: *mut MlsBuf,
    out_err: *mut MlsBuf,
) -> i32 {
    guard(out_err, || {
        let m = MlsMessage::from_bytes(unsafe { slice(msg, msg_len) }).map_err(err)?;
        let kind = match m.wire_format() {
            mls_rs::WireFormat::PrivateMessage => 1,
            mls_rs::WireFormat::PublicMessage => 2,
            mls_rs::WireFormat::Welcome => 4,
            mls_rs::WireFormat::KeyPackage => 5,
            _ => 0,
        };
        unsafe {
            if !out_kind.is_null() {
                *out_kind = kind;
            }
            if !out_epoch.is_null() {
                *out_epoch = m.epoch().unwrap_or(0);
            }
            put(out_gid, m.group_id().map(|g| g.to_vec()).unwrap_or_default());
        }
        Ok(())
    })
}

#[cfg(test)]
mod tests;
