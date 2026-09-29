package mls

import (
	"encoding/binary"
	"errors"
)

// Encoding shared with mls-ffi/src/codec.rs and store.rs.

var errTruncated = errors.New("mls: truncated buffer")

// Epoch is one retained prior-epoch record of a group.
type Epoch struct {
	ID   uint64
	Data []byte
}

// ChangeKind enumerates state mutations reported by TakeChanges.
type ChangeKind uint8

const (
	ChangeGroupWrite       ChangeKind = 1
	ChangeKeyPackageInsert ChangeKind = 2
	ChangeKeyPackageDelete ChangeKind = 3
)

// Change is a single persisted-state mutation.
type Change struct {
	Kind ChangeKind
	// GroupWrite
	GroupID  []byte
	State    []byte
	Epochs   []Epoch // upserts
	KeepFrom uint64  // delete epochs with ID < KeepFrom
	// KeyPackageInsert / Delete
	KeyPackageID   []byte
	KeyPackageData []byte
}

type reader struct{ b []byte }

func (r *reader) take(n int) ([]byte, error) {
	if len(r.b) < n {
		return nil, errTruncated
	}
	v := r.b[:n]
	r.b = r.b[n:]
	return v, nil
}

func (r *reader) u8() (uint8, error) {
	v, err := r.take(1)
	if err != nil {
		return 0, err
	}
	return v[0], nil
}

func (r *reader) u32() (uint32, error) {
	v, err := r.take(4)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(v), nil
}

func (r *reader) u64() (uint64, error) {
	v, err := r.take(8)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint64(v), nil
}

func (r *reader) bytes() ([]byte, error) {
	n, err := r.u32()
	if err != nil {
		return nil, err
	}
	v, err := r.take(int(n))
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), v...), nil
}

func appendBytes(dst, b []byte) []byte {
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(b)))
	return append(dst, b...)
}

func encodeList(items [][]byte) []byte {
	if len(items) == 0 {
		return nil
	}
	out := binary.BigEndian.AppendUint32(nil, uint32(len(items)))
	for _, it := range items {
		out = appendBytes(out, it)
	}
	return out
}

func decodeList(b []byte) ([][]byte, error) {
	if len(b) == 0 {
		return nil, nil
	}
	r := &reader{b}
	n, err := r.u32()
	if err != nil {
		return nil, err
	}
	out := make([][]byte, 0, n)
	for i := uint32(0); i < n; i++ {
		v, err := r.bytes()
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func encodeEpochs(eps []Epoch) []byte {
	if len(eps) == 0 {
		return nil
	}
	out := binary.BigEndian.AppendUint32(nil, uint32(len(eps)))
	for _, e := range eps {
		out = binary.BigEndian.AppendUint64(out, e.ID)
		out = appendBytes(out, e.Data)
	}
	return out
}

func decodeChanges(b []byte) ([]Change, error) {
	r := &reader{b}
	var out []Change
	for len(r.b) > 0 {
		tag, err := r.u8()
		if err != nil {
			return nil, err
		}
		ch := Change{Kind: ChangeKind(tag)}
		switch ch.Kind {
		case ChangeGroupWrite:
			if ch.GroupID, err = r.bytes(); err != nil {
				return nil, err
			}
			if ch.State, err = r.bytes(); err != nil {
				return nil, err
			}
			n, err := r.u32()
			if err != nil {
				return nil, err
			}
			for i := uint32(0); i < n; i++ {
				id, err := r.u64()
				if err != nil {
					return nil, err
				}
				data, err := r.bytes()
				if err != nil {
					return nil, err
				}
				ch.Epochs = append(ch.Epochs, Epoch{ID: id, Data: data})
			}
			if ch.KeepFrom, err = r.u64(); err != nil {
				return nil, err
			}
		case ChangeKeyPackageInsert:
			if ch.KeyPackageID, err = r.bytes(); err != nil {
				return nil, err
			}
			if ch.KeyPackageData, err = r.bytes(); err != nil {
				return nil, err
			}
		case ChangeKeyPackageDelete:
			if ch.KeyPackageID, err = r.bytes(); err != nil {
				return nil, err
			}
		default:
			return nil, errors.New("mls: unknown change tag")
		}
		out = append(out, ch)
	}
	return out, nil
}
