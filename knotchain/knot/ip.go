package knot

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"strconv"
)

const (
	IPv4 byte = 0x01
	IPv6 byte = 0x04
)

type IP struct {
	Addr  net.IP
	IPort uint16
}

func (ip *IP) Type() byte {
	if len(ip.Addr) == net.IPv4len {
		return IPv4
	} else if len(ip.Addr) == net.IPv6len {
		return IPv6
	}
	return 0
}

func (ip *IP) Host() string {
	return ip.Addr.String()
}

func (ip *IP) Port() uint16 {
	return ip.IPort
}

func (ip *IP) String() string {
	return net.JoinHostPort(ip.Addr.String(), strconv.FormatUint(uint64(ip.IPort), 10))
}

func (ip *IP) Encode() []byte {
	out := make([]byte, ip.Length())
	if ip.Type() == IPv4 {
		copy(out[:net.IPv4len], ip.Addr.To4())
		out = out[:net.IPv4len+2]
	} else if ip.Type() == IPv6 {
		copy(out[:net.IPv6len], ip.Addr)
	} else {
		panic("IP length is wrong")
	}
	binary.BigEndian.PutUint16(out[len(out)-2:], ip.IPort)
	return out
}

func (ip *IP) Length() int {
	return len(ip.Addr) + 2
}

func (ip *IP) DialContext(ctx context.Context, network string) (net.Conn, error) {
	dialer := &net.Dialer{}
	return dialer.DialContext(ctx, network, ip.String())
}

func DecodeIPv4(b []byte) (*IP, error) {
	if len(b) < net.IPv4len+2 {
		return nil, errors.New("IP parse error, incorrect length")
	}
	ip := &IP{
		Addr: make(net.IP, net.IPv4len),
		IPort: binary.BigEndian.Uint16(b[net.IPv4len : net.IPv4len+2]),
	}
	copy(ip.Addr, b[:net.IPv4len])
	return ip, nil
}

func DecodeIPv6(b []byte) (*IP, error) {
	if len(b) < net.IPv6len+2 {
		return nil, errors.New("IP parse error, incorrect length")
	}
	ip := &IP{
		Addr: make(net.IP, net.IPv6len),
		IPort: binary.BigEndian.Uint16(b[net.IPv6len : net.IPv6len+2]),
	}
	copy(ip.Addr, b[:net.IPv6len])
	return ip, nil
}
