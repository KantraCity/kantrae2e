//! In-memory implementation of the mls-rs storage traits that also records
//! every mutation in a change log for the host to persist.
//!
//! Change log encoding (sequence of records, see codec.rs):
//!   tag 1 GroupWrite: bytes group_id, bytes state,
//!                     u32 n, (u64 epoch_id, bytes data)*   -- upserts
//!                     u64 keep_from                        -- delete epochs < keep_from
//!   tag 2 KeyPackageInsert: bytes id, bytes data (MLS-encoded KeyPackageData)
//!   tag 3 KeyPackageDelete: bytes id

use std::collections::{BTreeMap, HashMap};
use std::convert::Infallible;
use std::sync::{Arc, Mutex};

use mls_rs_codec::{MlsDecode, MlsEncode};
use mls_rs_core::group::{EpochRecord, GroupState, GroupStateStorage};
use mls_rs_core::key_package::{KeyPackageData, KeyPackageStorage};
use zeroize::Zeroizing;

use crate::codec::Writer;

/// Same default as mls-rs' in-memory storage.
const EPOCH_RETENTION: usize = 3;

#[derive(Default)]
struct GroupData {
    state: Zeroizing<Vec<u8>>,
    epochs: BTreeMap<u64, Zeroizing<Vec<u8>>>,
}

#[derive(Default)]
pub struct Store {
    groups: HashMap<Vec<u8>, GroupData>,
    key_packages: HashMap<Vec<u8>, Vec<u8>>,
    changes: Writer,
}

impl Store {
    pub fn load_group(&mut self, id: Vec<u8>, state: Vec<u8>, epochs: Vec<(u64, Vec<u8>)>) {
        let data = GroupData {
            state: Zeroizing::new(state),
            epochs: epochs.into_iter().map(|(k, v)| (k, Zeroizing::new(v))).collect(),
        };
        self.groups.insert(id, data);
    }

    pub fn load_key_package(&mut self, id: Vec<u8>, data: Vec<u8>) {
        self.key_packages.insert(id, data);
    }

    pub fn forget_group(&mut self, id: &[u8]) {
        self.groups.remove(id);
    }

    pub fn take_changes(&mut self) -> Vec<u8> {
        std::mem::take(&mut self.changes.buf)
    }
}

#[derive(Clone)]
pub struct GroupStore(pub Arc<Mutex<Store>>);

#[derive(Clone)]
pub struct KeyPackageStore(pub Arc<Mutex<Store>>);

impl GroupStateStorage for GroupStore {
    type Error = Infallible;

    fn state(&self, group_id: &[u8]) -> Result<Option<Zeroizing<Vec<u8>>>, Infallible> {
        Ok(self.0.lock().unwrap().groups.get(group_id).map(|g| g.state.clone()))
    }

    fn epoch(&self, group_id: &[u8], epoch_id: u64) -> Result<Option<Zeroizing<Vec<u8>>>, Infallible> {
        let s = self.0.lock().unwrap();
        Ok(s.groups.get(group_id).and_then(|g| g.epochs.get(&epoch_id).cloned()))
    }

    fn write(
        &mut self,
        state: GroupState,
        epoch_inserts: Vec<EpochRecord>,
        epoch_updates: Vec<EpochRecord>,
    ) -> Result<(), Infallible> {
        let mut guard = self.0.lock().unwrap();
        let s = &mut *guard;
        let g = s.groups.entry(state.id.clone()).or_default();
        g.state = state.data.clone();
        let upserts: Vec<EpochRecord> = epoch_inserts.into_iter().chain(epoch_updates).collect();
        for e in &upserts {
            g.epochs.insert(e.id, e.data.clone());
        }
        while g.epochs.len() > EPOCH_RETENTION {
            let first = *g.epochs.keys().next().unwrap();
            g.epochs.remove(&first);
        }
        let keep_from = g.epochs.keys().next().copied().unwrap_or(0);

        let w = &mut s.changes;
        w.u8(1);
        w.bytes(&state.id);
        w.bytes(&state.data);
        let kept: Vec<&EpochRecord> = upserts.iter().filter(|e| e.id >= keep_from).collect();
        w.u32(kept.len() as u32);
        for e in kept {
            w.u64(e.id);
            w.bytes(&e.data);
        }
        w.u64(keep_from);
        Ok(())
    }

    fn max_epoch_id(&self, group_id: &[u8]) -> Result<Option<u64>, Infallible> {
        let s = self.0.lock().unwrap();
        Ok(s.groups.get(group_id).and_then(|g| g.epochs.keys().next_back().copied()))
    }
}

impl KeyPackageStorage for KeyPackageStore {
    type Error = mls_rs_codec::Error;

    fn delete(&mut self, id: &[u8]) -> Result<(), Self::Error> {
        let mut s = self.0.lock().unwrap();
        s.key_packages.remove(id);
        s.changes.u8(3);
        s.changes.bytes(id);
        Ok(())
    }

    fn insert(&mut self, id: Vec<u8>, pkg: KeyPackageData) -> Result<(), Self::Error> {
        let data = pkg.mls_encode_to_vec()?;
        let mut s = self.0.lock().unwrap();
        s.changes.u8(2);
        s.changes.bytes(&id);
        s.changes.bytes(&data);
        s.key_packages.insert(id, data);
        Ok(())
    }

    fn get(&self, id: &[u8]) -> Result<Option<KeyPackageData>, Self::Error> {
        let s = self.0.lock().unwrap();
        s.key_packages
            .get(id)
            .map(|d| KeyPackageData::mls_decode(&mut d.as_slice()))
            .transpose()
    }
}
