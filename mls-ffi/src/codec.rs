//! Minimal length-prefixed binary encoding shared with the Go side
//! (client/mls/codec.go). All integers are big-endian.
//!
//! bytes := u32 len, len bytes
//! list  := u32 count, bytes*

#[derive(Debug)]
pub struct Truncated;

pub struct Reader<'a> {
    buf: &'a [u8],
}

impl<'a> Reader<'a> {
    pub fn new(buf: &'a [u8]) -> Self {
        Reader { buf }
    }

    fn take(&mut self, n: usize) -> Result<&'a [u8], Truncated> {
        if self.buf.len() < n {
            return Err(Truncated);
        }
        let (a, b) = self.buf.split_at(n);
        self.buf = b;
        Ok(a)
    }

    pub fn u32(&mut self) -> Result<u32, Truncated> {
        Ok(u32::from_be_bytes(self.take(4)?.try_into().unwrap()))
    }

    pub fn u64(&mut self) -> Result<u64, Truncated> {
        Ok(u64::from_be_bytes(self.take(8)?.try_into().unwrap()))
    }

    pub fn bytes(&mut self) -> Result<&'a [u8], Truncated> {
        let n = self.u32()? as usize;
        self.take(n)
    }
}

#[derive(Default)]
pub struct Writer {
    pub buf: Vec<u8>,
}

impl Writer {
    pub fn u8(&mut self, v: u8) {
        self.buf.push(v);
    }

    pub fn u32(&mut self, v: u32) {
        self.buf.extend_from_slice(&v.to_be_bytes());
    }

    pub fn u64(&mut self, v: u64) {
        self.buf.extend_from_slice(&v.to_be_bytes());
    }

    pub fn bytes(&mut self, v: &[u8]) {
        self.u32(v.len() as u32);
        self.buf.extend_from_slice(v);
    }
}

pub fn decode_list(buf: &[u8]) -> Result<Vec<Vec<u8>>, Truncated> {
    if buf.is_empty() {
        return Ok(Vec::new());
    }
    let mut r = Reader::new(buf);
    let n = r.u32()?;
    (0..n).map(|_| r.bytes().map(|b| b.to_vec())).collect()
}

pub fn encode_list(items: &[Vec<u8>]) -> Vec<u8> {
    let mut w = Writer::default();
    w.u32(items.len() as u32);
    for i in items {
        w.bytes(i);
    }
    w.buf
}
