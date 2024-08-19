package knot

import (
	"encoding/binary"
	"errors"
)

const (
	ReferDomain byte = 0xa1 // Refer to the rest part of the sni/hostname
)

type Refer struct {
	Domain
}

func (r *Refer) Type() byte {
	return ReferDomain
}

func (r *Refer) Encode() []byte {
	out := make([]byte, 2)
	binary.BigEndian.PutUint16(out, r.IPort)
	return out
}

func (r *Refer) Length() int {
	return 2
}

func DecodeRefer(b []byte, stemHostname string) (*Refer, error) {
	if len(b) < 2 {
		return nil, errors.New("Refer parse error, insufficient length")
	}
	r := &Refer{Domain: Domain{
		Addr: stemHostname,
		IPort: binary.BigEndian.Uint16(b[:2]),
	}}
	return r, nil
}
