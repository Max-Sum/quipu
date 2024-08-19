package knotchain

import (
	"bytes"
	"compress/flate"
	"context"
	"encoding/base32"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"

	"github.com/Max-Sum/quipu/knotchain/knot"
)

const (
	Version1      byte = 0x01
	NotCompressed byte = 0
	Deflate       byte = 1
	Encrypted     byte = 1
)

var ErrNoKnotToUntie error = fmt.Errorf("no more knot to untie")
var Base32LowerCaseEncoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

type Knot interface {
	Type() byte
	Host() string
	Port() uint16
	Encode() []byte
	Length() int
	DialContext(context.Context, string) (net.Conn, error)
}

type KnotChain struct {
	Version   byte
	Knots     []Knot
}

// Knot chain byte format:
// | Encrypted(1) | Compress(1) | Ver(6) | CurrHop(4) | TotalHops(4) |  Addresses (Var)      |
// |                                     | >> ------ (optional) Compressed ----->>           |

func TieChain(chain *KnotChain) ([]byte, error) {
	compressed := NotCompressed
	version := chain.Version
	// Encode Knots
	buf := new(bytes.Buffer)
	if len(chain.Knots) > 0b00001111 {
		return nil, fmt.Errorf("TieChain error: chain is too long")
	}
	buf.Write([]byte{byte(len(chain.Knots))})
	for _, k := range chain.Knots {
		if _, err := buf.Write([]byte{k.Type()}); err != nil {
			return nil, err
		}
		if _, err := buf.Write(k.Encode()); err != nil {
			return nil, err
		}
	}
	// Try compress
	b, isCompressed := compress(buf.Bytes(), true)
	if isCompressed {
		compressed = Deflate
	}
	nb := make([]byte, len(b)+1)
	nb[0] = compressed<<6 + version
	copy(nb[1:], b)
	return nb, nil
}

// Untie the first knot in the chain, returning the untied knot and a new encoded chain
func Untie(b []byte, stemHostname string) (Knot, []byte, error) {
	compressed := b[0] >> 6
	version := b[0] & 0b00111111
	if version != Version1 {
		return nil, nil, fmt.Errorf("Untie error: not supported Version %d", version)
	}
	var r io.Reader
	var w io.Writer
	var nextKnot Knot

	r = bytes.NewReader(b[1:])
	newBuf := new(bytes.Buffer)
	newBuf.Write([]byte{b[0]})
	w = newBuf
	if compressed == Deflate {
		r = flate.NewReader(r)
		w, _ = flate.NewWriter(w, flate.BestCompression)
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, nil, fmt.Errorf("Untie error: failed to decompress: %v", err)
	}
	// Decode Hops
	currHop := b[0] >> 4
	totalHops := b[0] & 0b00001111
	if currHop == totalHops {
		// Revert the host to original state
		w.Write([]byte{totalHops})
		w.Write(b[1:])
		err = ErrNoKnotToUntie
	} else {
		// Decode the knot
		addrtype := b[1]
		b = b[2:]
		switch addrtype {
		case knot.IPv4:
			nextKnot, err = knot.DecodeIPv4(b[:net.IPv4len+2])
		case knot.IPv6:
			nextKnot, err = knot.DecodeIPv6(b[:net.IPv6len+2])
		case knot.DomainName:
			nextKnot, err = knot.DecodeDomain(b)
		case knot.ReferDomain:
			nextKnot, err = knot.DecodeRefer(b, stemHostname)
		}
		if err != nil {
			return nil, nil, err
		}
		b = b[nextKnot.Length():]
		
		// Put the first knot to the end
		w.Write(b)
		w.Write([]byte{nextKnot.Type()})
		w.Write(nextKnot.Encode())
	}

	if compressed == Deflate {
		wc := w.(io.WriteCloser)
		if err := wc.Close(); err != nil {
			return nil, nil, err
		}
	}

	return nextKnot, newBuf.Bytes(), err
}

func compress(b []byte, try bool) ([]byte, bool) {
	cbuf := new(bytes.Buffer)
	w, _ := flate.NewWriter(cbuf, flate.BestCompression)
	if _, err := w.Write(b); err != nil {
		return b, false
	}
	if err := w.Close(); err != nil {
		return b, false
	}
	if try && len(cbuf.Bytes()) >= len(b) {
		return b, false
	}
	return cbuf.Bytes(), true
}

func tryRefer(chain *KnotChain, stemHostname string) *KnotChain {
	isMatch := false
	for _, k := range chain.Knots {
		if k.Type() == knot.DomainName && k.Host() == stemHostname {
			isMatch = true
			break
		}
	}
	if !isMatch {
		return chain
	}
	newChain := &KnotChain{
		Version: chain.Version,
		Knots: make([]Knot, len(chain.Knots)),
	}
	for i, k := range chain.Knots {
		if k.Type() == knot.DomainName && k.Host() == stemHostname {
			domainKnot := k.(*knot.Domain)
			newChain.Knots[i] = &knot.Refer{Domain: *domainKnot}
		} else {
			newChain.Knots[i] = k
		}
	}
	return newChain
}

func TieChainToHostname(chain *KnotChain, stemHostname string) (string, error) {
	chain = tryRefer(chain, stemHostname)
	b, err := TieChain(chain)
	if err != nil {
		return "", err
	}
	encodedStr := Base32LowerCaseEncoding.EncodeToString(b)
	host := "q--" + encodedStr + "." + stemHostname
	if len(host) > 253 {
		return "", fmt.Errorf("TieChainToHostname error: result hostname is too long, %d > 253", len(host))
	}
	return host, nil
}

// UntieHostname untie the knot chain from the hostname.
// It returns the untied knot and a new hostname.
func UntieHostname(hostname string) (Knot, string, error) {
	first, stem, _ := strings.Cut(hostname, ".")
	if !strings.HasPrefix(first, "q--") {
		return nil, hostname, ErrNoKnotToUntie
	}
	b, err := Base32LowerCaseEncoding.DecodeString(first[3:])
	if err != nil {
		return nil, "", err
	}
	nextKnot, b, err := Untie(b, stem)
	newHost := "q--" + Base32LowerCaseEncoding.EncodeToString(b) + "." + stem
	if len(newHost) > 253 {
		return nil, "", fmt.Errorf("UntieHostname error: new hostname is too long, %d > 253", len(newHost))
	}
	return nextKnot, newHost, err
}

func KnotString(k Knot) string {
	return net.JoinHostPort(k.Host(), strconv.FormatUint(uint64(k.Port()), 10))
}
