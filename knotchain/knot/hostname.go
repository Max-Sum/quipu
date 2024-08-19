package knot

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"strconv"
	"strings"
)

const (
	DomainName byte = 0x03
)

// |- HostLen(1B) -| ---- Addr (Var) ---- | -- Port (2B) -- |

type Domain struct {
	Addr  string
	IPort uint16

	ips     []net.IPAddr
	dynPort uint16 // for IP4P extraction
}

func (d *Domain) Type() byte {
	return DomainName
}

func (d *Domain) String() string {
	return net.JoinHostPort(d.Addr, strconv.FormatUint(uint64(d.Port()), 10))
}

func (d *Domain) Host() string {
	return d.Addr
}

func (d *Domain) Port() uint16 {
	if d.dynPort != 0 {
		return d.dynPort
	}
	return d.IPort
}

func (d *Domain) Encode() []byte {
	out := make([]byte, len([]byte(d.Addr))+3)
	if len([]byte(d.Addr)) > 255 {
		panic("Domain name is too long")
	}
	out[0] = byte(len([]byte(d.Addr)))
	copy(out[1:len(out)-2], []byte(d.Addr))
	binary.BigEndian.PutUint16(out[len(out)-2:], d.IPort)
	return out
}

func (d *Domain) Length() int {
	return len([]byte(d.Addr)) + 3
}

func (d *Domain) DialContext(ctx context.Context, network string) (net.Conn, error) {
	dialer := &net.Dialer{}
	portStr := strconv.FormatUint(uint64(d.Port()), 10)
	// Resolved
	if d.ips != nil {
		var err error
		var conn net.Conn
		for _, ip := range d.ips {
			conn, err = dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), portStr))
			if err != nil {
				return conn, nil
			}
		}
		if err != nil {
			return nil, err
		}
	}
	return dialer.DialContext(ctx, network, net.JoinHostPort(d.Addr, portStr))
}

func DecodeDomain(b []byte) (*Domain, error) {
	Hostlen := int(b[0])
	if len(b) < Hostlen+3 {
		return nil, errors.New("Domain parse error, insufficient length")
	}
	d := &Domain{
		Addr:  strings.Clone(string(b[1 : Hostlen+1])),
		IPort: binary.BigEndian.Uint16(b[Hostlen+1 : Hostlen+3]),
	}
	// Resolve IP4P
	if d.IPort == 0 {
		ips, err := net.DefaultResolver.LookupIPAddr(context.Background(), d.Addr)
		if err != nil {
			return nil, err
		}
		for _, ip := range ips {
			// Find IP4P
			ipv4, IPort := lookupIP4P(ip.IP)
			if IPort != 0 {
				d.ips = []net.IPAddr{{IP: ipv4}}
				d.dynPort = IPort
				break
			}
		}
		if d.dynPort == 0 {
			d.ips = ips
		}
	}
	return d, nil
}

func lookupIP4P(ip net.IP) (ipv4 net.IP, IPort uint16) {
	if len(ip) != net.IPv6len {
		return ip, 0
	}
	if ip[0] == 0x20 && ip[1] == 0x01 && ip[2] == 0x00 && ip[3] == 0x00 {
		for i := 4; i < 10; i++ {
			if ip[i] != 0 {
				return ip, 0
			}
		}
		ipv4 = net.IPv4(ip[12], ip[13], ip[14], ip[15])
		IPort = uint16(ip[10])<<8 + uint16(ip[11])
		return
	}
	return ip, 0
}
