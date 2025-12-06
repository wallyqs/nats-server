package server

import (
	cbor "github.com/delaneyj/cbor/runtime"
)

// MarshalCBOR encodes the StorageType as a small CBOR integer.
func (st StorageType) MarshalCBOR(b []byte) ([]byte, error) {
	return cbor.AppendInt(b, int(st)), nil
}

// UnmarshalCBOR decodes the StorageType from a CBOR integer.
func (st *StorageType) UnmarshalCBOR(b []byte) ([]byte, error) {
	v, rest, err := cbor.ReadIntBytes(b)
	if err != nil {
		return b, err
	}
	*st = StorageType(v)
	return rest, nil
}
