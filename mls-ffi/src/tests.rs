use super::*;
use std::ptr::null_mut;

fn empty() -> MlsBuf {
    MlsBuf { ptr: null_mut(), len: 0, cap: 0 }
}

fn take(b: MlsBuf) -> Vec<u8> {
    let v = unsafe { slice(b.ptr, b.len) }.to_vec();
    unsafe { mls_buf_free(b) };
    v
}

fn ok(code: i32, e: MlsBuf) {
    if code != MLS_OK {
        panic!("code {code}: {}", String::from_utf8_lossy(&take(e)));
    }
}

unsafe fn new_client(name: &str) -> *mut MlsClient {
    let (mut sk, mut pk, mut e) = (empty(), empty(), empty());
    ok(mls_generate_signature_keypair(&mut sk, &mut pk, &mut e), e);
    let (sk, pk) = (take(sk), take(pk));
    let mut c = null_mut();
    let mut e = empty();
    ok(
        mls_client_new(name.as_ptr(), name.len(), sk.as_ptr(), sk.len(), pk.as_ptr(), pk.len(), &mut c, &mut e),
        e,
    );
    c
}

#[test]
fn two_members_exchange_message() {
    unsafe { two_members() }
}

unsafe fn two_members() {
    let alice = new_client("alice");
    let bob = new_client("bob");
    let gid = b"group-1";

    let (mut kp, mut e) = (empty(), empty());
    ok(mls_generate_key_package(bob, &mut kp, &mut e), e);
    let kps = codec::encode_list(&[take(kp)]);

    let mut e = empty();
    ok(mls_create_group(alice, gid.as_ptr(), gid.len(), &mut e), e);
    let (mut commit, mut welcome, mut e) = (empty(), empty(), empty());
    ok(
        mls_create_commit(alice, gid.as_ptr(), gid.len(), kps.as_ptr(), kps.len(), null_mut(), 0, &mut commit, &mut welcome, null_mut(), &mut e),
        e,
    );
    take(commit);
    let welcome = take(welcome);
    let mut e = empty();
    ok(mls_apply_pending_commit(alice, gid.as_ptr(), gid.len(), &mut e), e);

    let (mut out_gid, mut e) = (empty(), empty());
    ok(mls_join_group(bob, welcome.as_ptr(), welcome.len(), &mut out_gid, &mut e), e);
    assert_eq!(take(out_gid), gid);

    let (mut ct, mut e) = (empty(), empty());
    let pt = b"hello bob";
    ok(mls_encrypt_application_message(alice, gid.as_ptr(), gid.len(), pt.as_ptr(), pt.len(), &mut ct, &mut e), e);
    let ct = take(ct);
    assert!(!ct.windows(pt.len()).any(|w| w == pt));

    let mut p = MlsProcessed { kind: 0, data: empty(), sender: empty(), epoch: 0, removed: 0, external: 0 };
    let mut e = empty();
    ok(mls_process_message(bob, gid.as_ptr(), gid.len(), ct.as_ptr(), ct.len(), &mut p, &mut e), e);
    assert_eq!(p.kind, MLS_KIND_APPLICATION);
    assert_eq!(p.epoch, 1);
    assert_eq!(take(p.data), pt);
    assert_eq!(take(p.sender), b"alice");

    let (mut ch, mut e) = (empty(), empty());
    ok(mls_client_take_changes(alice, &mut ch, &mut e), e);
    assert!(!take(ch).is_empty());

    mls_client_free(alice);
    mls_client_free(bob);
}
