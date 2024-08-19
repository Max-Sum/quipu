package router

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/Max-Sum/quipu/knotchain"
	"github.com/ginuerzh/gosocks4"
	"github.com/ginuerzh/gosocks5"
	dissector "github.com/go-gost/tls-dissector"
)

var bufferPool = sync.Pool{
	New: func() interface{} {
		return make([]byte, 32*1024)
	},
}

type TCPServer struct {
	listen string
	isTLS  bool
	cfg    *routerConf
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	conns  map[*net.Conn]struct{}
	ln     net.Listener
}

func NewTCPServer(listen string, isTLS bool, cfg *routerConf) *TCPServer {
	return &TCPServer{
		listen: listen,
		isTLS:  isTLS,
		cfg:    cfg,
		conns:  make(map[*net.Conn]struct{}),
	}
}

func (s *TCPServer) ListenAndServe() (err error) {
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.ln, err = net.Listen("tcp", s.listen)
	log.Printf("listening on %s\n", s.listen)
	if err != nil {
		s.cancel()
		return err
	}
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			s.cancel()
			return err
		}
		go s.Handle(conn)
	}
}

func (s *TCPServer) Shutdown() {
	s.ln.Close()
	for c := range s.conns {
		(*c).Close()
	}
	s.cancel()
}

func (s *TCPServer) Handle(conn net.Conn) {
	fmt.Println(conn.RemoteAddr())
	s.trackConn(&conn, true)
	defer func() {
		s.trackConn(&conn, false)
		conn.Close()
	}()
	br := bufio.NewReader(conn)

	var readahead []byte
	var nextKnot knotchain.Knot
	proto := ""
	var err error
	if !s.isTLS {
		// We assume it is an HTTP request
		// HTTP/Socks sniff
		readahead, proto, nextKnot, err = s.untieTCPHost(br)
	} else {
		// TLS sniff
		proto = "tls"
		readahead, nextKnot, err = s.untieClientHelloRecord(br)
	}
	if err == knotchain.ErrNoKnotToUntie {
		err = nil
		nextKnot = nil
	} else if err != nil {
		log.Printf("[handle] Failed to untie %s -> %s : %s",
			conn.RemoteAddr(), conn.LocalAddr(), err)
		return
	}

	conn = &wrappedConn{br: br, Conn: conn, prepend: readahead}

	if nextKnot == nil {
		// Reached end of chain
		network := ""
		address := ""
		switch proto {
		case "tls":
			network = "tcp"
			address = s.cfg.FinalTLS
		case "http":
			network = "tcp"
			address = s.cfg.FinalHTTP
		case "socks4":
			fallthrough
		case "socks5":
			network = "tcp"
			address = s.cfg.FinalSocks
		}
		if len(address) == 0 {
			log.Printf("[handle] No final backend for proto[%s] (%s -> %s)",
				proto, conn.RemoteAddr(), conn.LocalAddr())
			return
		}
		if !strings.Contains(address, ":") {
			// Assume address as unix socket
			if network == "udp" {
				network = "unixgram"
			} else {
				network = "unixpacket"
			}
		}
		log.Printf("Final: %s -> %s", conn.RemoteAddr(), address)
		dialer := net.Dialer{}
		rconn, err := dialer.DialContext(s.ctx, network, address)
		if err != nil {
			log.Printf("[handle] Failed to relay %s -> %s -> %s : %s",
				conn.RemoteAddr(), conn.LocalAddr(), address, err)
			return
		}
		defer rconn.Close()
		transport(conn, rconn)
		return
	}

	if !s.cfg.EnableRedir {
		// no nextKnot and no routes matched, failing
		log.Printf("[handle] redir is disabled %s -> %s -> %s",
			conn.RemoteAddr(), conn.LocalAddr(), knotchain.KnotString(nextKnot))
		return
	}

	// Filter allow and deny
	if !s.cfg.IsPortAllowed(nextKnot.Port()) {
		log.Printf("[handle] Redir port not allowed %s -> %s -> %s",
			conn.RemoteAddr(), conn.LocalAddr(), knotchain.KnotString(nextKnot))
		return
	}

	log.Printf("Redirect: %s -> %s:%d", conn.RemoteAddr(), nextKnot.Host(), nextKnot.Port())
	rconn, err := nextKnot.DialContext(s.ctx, "tcp")
	if err != nil {
		log.Printf("[handle] Failed to relay %s -> %s -> %s : %s",
			conn.RemoteAddr(), conn.LocalAddr(), knotchain.KnotString(nextKnot), err)
		return
	}
	defer rconn.Close()
	transport(conn, rconn)
}

func (s *TCPServer) trackConn(conn *net.Conn, add bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if add {
		s.conns[conn] = struct{}{}
	} else {
		delete(s.conns, conn)
	}
}

func (s *TCPServer) untieTCPHost(r *bufio.Reader) (buf []byte, proto string, nextKnot knotchain.Knot, err error) {
	b, err := r.Peek(1)
	if err != nil {
		return nil, "", nil, err
	}

	switch b[0] {
	case gosocks4.Ver4:
		proto = "socks4"
		buf, nextKnot, err = s.untieSocks4Host(r)
	case gosocks5.Ver5: // socks5
		proto = "socks5"
		buf, nextKnot, err = s.untieSocks5Host(r)
	default: // http
		proto = "http"
		buf, nextKnot, err = s.untieHTTPHost(r)
	}
	return
}

func (s *TCPServer) untieSocks4Host(r *bufio.Reader) ([]byte, knotchain.Knot, error) {
	req, err := gosocks4.ReadRequest(r)
	if err != nil {
		return nil, nil, err
	}
	var nextKnot knotchain.Knot
	nextKnot, req.Addr.Host, err = knotchain.UntieHostname(req.Addr.Host)
	if err != nil && err != knotchain.ErrNoKnotToUntie {
		return nil, nil, err
	}
	// Prepend the read part
	buf := &bytes.Buffer{}
	req.Write(buf)
	return buf.Bytes(), nextKnot, err
}

func (s *TCPServer) untieSocks5Host(r *bufio.Reader) ([]byte, knotchain.Knot, error) {
	req, err := gosocks5.ReadRequest(r)
	if err != nil {
		return nil, nil, err
	}
	var nextKnot knotchain.Knot
	nextKnot, req.Addr.Host, err = knotchain.UntieHostname(req.Addr.Host)
	if err != nil && err != knotchain.ErrNoKnotToUntie {
		return nil, nil, err
	}
	// Prepend the read part
	buf := &bytes.Buffer{}
	req.Write(buf)
	return buf.Bytes(), nextKnot, err
}

func (s *TCPServer) untieHTTPHost(r *bufio.Reader) ([]byte, knotchain.Knot, error) {
	req, err := http.ReadRequest(r)
	if err != nil {
		return nil, nil, err
	}
	host, port, err := net.SplitHostPort(req.Host)
	if err != nil {
		return nil, nil, err
	}
	nextKnot, newHost, err := knotchain.UntieHostname(host)
	if err != nil && err != knotchain.ErrNoKnotToUntie {
		return nil, nil, err
	}
	req.Host = net.JoinHostPort(newHost, port)

	// Prepend the read part
	buf := &bytes.Buffer{}
	if req.URL.IsAbs() {
		req.WriteProxy(buf)
	} else {
		req.Write(buf)
	}
	return buf.Bytes(), nextKnot, err
}

func (s *TCPServer) untieClientHelloRecord(r io.Reader) ([]byte, knotchain.Knot, error) {
	record, err := dissector.ReadRecord(r)
	if err != nil {
		return nil, nil, err
	}
	clientHello := &dissector.ClientHelloHandshake{}
	if err := clientHello.Decode(record.Opaque); err != nil {
		return nil, nil, err
	}

	var nextKnot knotchain.Knot
	for _, ext := range clientHello.Extensions {
		if ext.Type() != dissector.ExtServerName {
			continue
		}
		snExtension := ext.(*dissector.ServerNameExtension)
		nextKnot, snExtension.Name, err = knotchain.UntieHostname(snExtension.Name)
		if err != nil && err != knotchain.ErrNoKnotToUntie {
			return nil, nil, err
		}
		break
	}
	record.Opaque, err = clientHello.Encode()
	if err != nil {
		return nil, nil, err
	}

	buf := &bytes.Buffer{}
	if _, err := record.WriteTo(buf); err != nil {
		return nil, nil, err
	}

	if nextKnot == nil {
		err = knotchain.ErrNoKnotToUntie
	}
	return buf.Bytes(), nextKnot, err
}

type wrappedConn struct {
	net.Conn
	br      *bufio.Reader
	prepend []byte
}

func (c *wrappedConn) Read(b []byte) (int, error) {
	if len(c.prepend) > 0 {
		n := copy(b, c.prepend)
		if n == len(c.prepend) {
			c.prepend = nil
		} else {
			c.prepend = c.prepend[n:]
		}
		return n, nil
	}
	return c.br.Read(b)
}

func transport(rw1, rw2 io.ReadWriter) error {
	errc := make(chan error, 1)
	go func() {
		errc <- copyBuffer(rw1, rw2)
	}()

	go func() {
		errc <- copyBuffer(rw2, rw1)
	}()

	if err := <-errc; err != nil && err != io.EOF {
		return err
	}

	return nil
}

func copyBuffer(dst io.Writer, src io.Reader) error {
	buf := bufferPool.Get().([]byte)
	defer bufferPool.Put(buf)

	_, err := io.CopyBuffer(dst, src, buf)
	return err
}
