package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"slices"
	"sync"
	"time"
)

// ManagedHTTPServer owns the application's HTTP listener and graceful shutdown.
// With H2C disabled it delegates directly to the embedded native server. With
// H2C enabled the two native servers retain independent idle timeouts.
// Configuration is set before serving; this is not a general callback adapter.
type ManagedHTTPServer struct {
	*http.Server
	h2Idle      time.Duration
	h2c         bool
	mu          sync.Mutex
	listener    net.Listener
	children    [2]*http.Server
	queues      [2]*managedListener
	pending     map[net.Conn]struct{}
	stopped     bool
	failure     error
	started     bool
	stop        chan struct{}
	classifiers sync.WaitGroup
}

func newManagedHTTPServer(base *http.Server, h2c bool, h2Idle time.Duration) *ManagedHTTPServer {
	return &ManagedHTTPServer{Server: base, h2c: h2c, h2Idle: h2Idle, pending: make(map[net.Conn]struct{}), stop: make(chan struct{})}
}

func (m *ManagedHTTPServer) ListenAndServe() error {
	if !m.h2c {
		return m.Server.ListenAndServe()
	}
	m.mu.Lock()
	stopped := m.stopped
	m.mu.Unlock()
	if stopped {
		return http.ErrServerClosed
	}
	addr := m.Addr
	if addr == "" {
		addr = ":http"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return m.Serve(ln)
}

// ServeTLS preserves net/http's certificate loading and protocol selection.
func (m *ManagedHTTPServer) ServeTLS(ln net.Listener, certFile, keyFile string) error {
	if !m.h2c {
		return m.Server.ServeTLS(ln, certFile, keyFile)
	}
	cfg := &tls.Config{}
	if m.TLSConfig != nil {
		cfg = m.TLSConfig.Clone()
	}
	// Match native protocol defaults without mutating the exposed selection.
	protocols := m.Protocols
	if protocols == nil {
		protocols = new(http.Protocols)
		protocols.SetHTTP1(true)
		protocols.SetHTTP2(true)
	} else if !protocols.HTTP1() && !protocols.HTTP2() && !protocols.UnencryptedHTTP2() {
		protocols = new(http.Protocols)
		protocols.SetHTTP1(true)
	}
	cfg.NextProtos = slices.Clone(cfg.NextProtos)
	cfg.NextProtos = slices.DeleteFunc(cfg.NextProtos, func(p string) bool {
		return p == "h2" && !protocols.HTTP2() || p == "http/1.1" && !protocols.HTTP1()
	})
	if protocols.HTTP1() && !slices.Contains(cfg.NextProtos, "http/1.1") {
		cfg.NextProtos = append(cfg.NextProtos, "http/1.1")
	}
	if protocols.HTTP2() && !slices.Contains(cfg.NextProtos, "h2") {
		cfg.NextProtos = append(cfg.NextProtos, "h2")
	}
	if len(cfg.Certificates) == 0 && cfg.GetCertificate == nil && cfg.GetConfigForClient == nil || certFile != "" || keyFile != "" {
		certificate, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return err
		}
		cfg.Certificates = []tls.Certificate{certificate}
	}
	// Configuration failure leaves a caller-owned listener open, as native
	// ServeTLS does. Ownership transfers only after configuration succeeds.
	ln = &managedOnceListener{Listener: ln}
	defer func() { _ = ln.Close() }()
	return m.Serve(tls.NewListener(ln, cfg))
}

func (m *ManagedHTTPServer) ListenAndServeTLS(certFile, keyFile string) error {
	if !m.h2c {
		return m.Server.ListenAndServeTLS(certFile, keyFile)
	}
	m.mu.Lock()
	stopped := m.stopped
	m.mu.Unlock()
	if stopped {
		return http.ErrServerClosed
	}
	addr := m.Addr
	if addr == "" {
		addr = ":https"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	defer func() { _ = ln.Close() }()
	return m.ServeTLS(ln, certFile, keyFile)
}

func (m *ManagedHTTPServer) nativeServer(h2 bool) *http.Server {
	protocols := new(http.Protocols)
	protocols.SetHTTP1(!h2)
	protocols.SetUnencryptedHTTP2(h2)
	protocols.SetHTTP2(h2)
	s := &http.Server{Addr: m.Addr, Handler: m.Handler, ReadHeaderTimeout: m.ReadHeaderTimeout, IdleTimeout: m.IdleTimeout, MaxHeaderBytes: m.MaxHeaderBytes, ReadTimeout: m.ReadTimeout, WriteTimeout: m.WriteTimeout, ErrorLog: m.ErrorLog, Protocols: protocols}
	if h2 {
		s.IdleTimeout = m.h2Idle
		s.HTTP2 = m.HTTP2
	}
	return s
}

func (m *ManagedHTTPServer) Serve(ln net.Listener) error {
	if !m.h2c {
		return m.Server.Serve(ln)
	}
	m.mu.Lock()
	if m.stopped || m.started {
		m.mu.Unlock()
		_ = ln.Close()
		return http.ErrServerClosed
	}
	m.started = true
	m.listener = ln
	for i := range m.children {
		m.children[i] = m.nativeServer(i == 1)
		m.queues[i] = &managedListener{owner: m, addr: ln.Addr(), conns: make(chan net.Conn), done: make(chan struct{})}
	}
	children, queues := m.children, m.queues
	m.mu.Unlock()
	for i := range children {
		go func(i int) {
			err := children[i].Serve(queues[i])
			m.mu.Lock()
			if m.stopped {
				m.mu.Unlock()
				return
			}
			if errors.Is(err, http.ErrServerClosed) {
				err = errors.New("native HTTP server stopped outside manager shutdown")
			}
			if m.failure == nil {
				m.failure = err
			}
			m.mu.Unlock()
			_ = m.Close()
		}(i)
	}
	var acceptErr error
	var backoff time.Duration
	for {
		conn, err := ln.Accept()
		if err != nil {
			if temporary, ok := err.(interface{ Temporary() bool }); ok && temporary.Temporary() {
				if backoff == 0 {
					backoff = 5 * time.Millisecond
				} else {
					backoff *= 2
				}
				if backoff > time.Second {
					backoff = time.Second
				}
				timer := time.NewTimer(backoff)
				select {
				case <-timer.C:
				case <-m.stop:
					timer.Stop()
					acceptErr = http.ErrServerClosed
				}
				if acceptErr != nil {
					break
				}
				continue
			}
			acceptErr = err
			break
		}
		backoff = 0
		accepted := time.Now()
		m.mu.Lock()
		if m.stopped {
			m.mu.Unlock()
			_ = conn.Close()
			acceptErr = http.ErrServerClosed
			break
		}
		m.pending[conn] = struct{}{}
		m.classifiers.Add(1)
		m.mu.Unlock()
		go func() { defer m.classifiers.Done(); m.classify(conn, queues, accepted) }()
	}
	m.mu.Lock()
	stopped, failure := m.stopped, m.failure
	m.mu.Unlock()
	if stopped {
		if failure != nil {
			_ = m.Close()
			return failure
		}
		return http.ErrServerClosed
	}

	// A listener failure must not leave the sibling server or pending sockets live.
	_ = m.Close()
	return acceptErr
}

const managedH2Preface = "PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"

func (m *ManagedHTTPServer) classify(conn net.Conn, queues [2]*managedListener, accepted time.Time) {
	if secure, ok := conn.(*tls.Conn); ok {
		var timeout time.Duration
		for _, d := range []time.Duration{m.ReadHeaderTimeout, m.ReadTimeout, m.WriteTimeout} {
			if d > 0 && (timeout == 0 || d < timeout) {
				timeout = d
			}
		}
		if timeout > 0 {
			_ = secure.SetDeadline(accepted.Add(timeout))
		}
		err := secure.HandshakeContext(context.Background())
		index := 0
		if err == nil && secure.ConnectionState().NegotiatedProtocol == "h2" {
			index = 1
		}
		// On failure hand the original TLS connection to the native H1 server;
		// its cached handshake error retains native error/HTTP-on-TLS handling.
		select {
		case queues[index].conns <- secure:
		case <-queues[index].done:
			_ = secure.Close()
			m.removePending(conn)
		}
		return
	}
	deadline := time.Time{}
	timeout := m.ReadHeaderTimeout
	if timeout == 0 {
		timeout = m.ReadTimeout
	}
	if timeout > 0 {
		deadline = accepted.Add(timeout)
	}
	_ = conn.SetReadDeadline(deadline)
	prefix := make([]byte, 13, 24)
	n, err := io.ReadFull(conn, prefix)
	prefix = prefix[:n]
	h2 := false
	if err == nil && bytes.Equal(prefix, []byte(managedH2Preface[:13])) {
		rest := make([]byte, 11)
		n, err = io.ReadFull(conn, rest)
		prefix = append(prefix, rest[:n]...)
		h2 = err == nil && bytes.Equal(prefix, []byte(managedH2Preface))
	}
	wrapped := &managedPrefixConn{Conn: conn, reader: io.MultiReader(bytes.NewReader(prefix), conn), firstDeadline: deadline}
	index := 0
	if h2 {
		index = 1
	}
	// Native H2 resets this initial deadline after accepting the complete preface;
	// H1 resets it after parsing its first headers. Do not clamp later body/idle IO.
	select {
	case queues[index].conns <- wrapped:
	case <-queues[index].done:
		_ = conn.Close()
		m.removePending(conn)
	}
}

func (m *ManagedHTTPServer) removePending(conn net.Conn) {
	m.mu.Lock()
	delete(m.pending, conn)
	m.mu.Unlock()
}

func (m *ManagedHTTPServer) stopAdmission() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped {
		return
	}
	m.stopped = true
	close(m.stop)
	if m.listener != nil {
		_ = m.listener.Close()
	}
	for conn := range m.pending {
		_ = conn.Close()
	}
	for _, q := range m.queues {
		if q != nil {
			q.closeOnce.Do(func() { close(q.done) })
		}
	}
}

func (m *ManagedHTTPServer) Shutdown(ctx context.Context) error {
	if !m.h2c {
		return m.Server.Shutdown(ctx)
	}
	m.stopAdmission()
	m.mu.Lock()
	children := m.children
	m.mu.Unlock()
	results := make(chan error, 2)
	for _, s := range children {
		go func(s *http.Server) {
			if s == nil {
				results <- nil
			} else {
				results <- s.Shutdown(ctx)
			}
		}(s)
	}
	classified := make(chan struct{})
	go func() { m.classifiers.Wait(); close(classified) }()
	var result error
	for range children {
		select {
		case err := <-results:
			result = errors.Join(result, err)
		case <-ctx.Done():
			return errors.Join(result, ctx.Err())
		}
	}
	select {
	case <-classified:
		return result
	case <-ctx.Done():
		return errors.Join(result, ctx.Err())
	}
}

func (m *ManagedHTTPServer) Close() error {
	if !m.h2c {
		return m.Server.Close()
	}
	m.stopAdmission()
	m.mu.Lock()
	children := m.children
	m.mu.Unlock()
	var result error
	for _, s := range children {
		if s != nil {
			result = errors.Join(result, s.Close())
		}
	}
	m.classifiers.Wait()
	return result
}

type managedListener struct {
	owner     *ManagedHTTPServer
	addr      net.Addr
	conns     chan net.Conn
	done      chan struct{}
	closeOnce sync.Once
}

func (l *managedListener) Addr() net.Addr { return l.addr }
func (l *managedListener) Close() error   { l.closeOnce.Do(func() { close(l.done) }); return nil }
func (l *managedListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.conns:
		original := conn
		if prefix, ok := conn.(*managedPrefixConn); ok {
			original = prefix.Conn
		}
		l.owner.mu.Lock()
		stopped := l.owner.stopped
		delete(l.owner.pending, original)
		l.owner.mu.Unlock()
		if stopped {
			_ = conn.Close()
			return nil, net.ErrClosed
		}
		return conn, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

type managedPrefixConn struct {
	net.Conn
	reader        io.Reader
	mu            sync.Mutex
	firstDeadline time.Time
}

func (c *managedPrefixConn) Read(p []byte) (int, error) { return c.reader.Read(p) }
func (c *managedPrefixConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if t.IsZero() {
		c.firstDeadline = time.Time{}
	} else if !c.firstDeadline.IsZero() && t.After(c.firstDeadline) {
		t = c.firstDeadline
	}
	return c.Conn.SetReadDeadline(t)
}
func (c *managedPrefixConn) SetDeadline(t time.Time) error {
	return errors.Join(c.SetReadDeadline(t), c.SetWriteDeadline(t))
}
func (c *managedPrefixConn) CloseWrite() error {
	if conn, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return conn.CloseWrite()
	}
	return errors.New("connection does not support CloseWrite")
}
func (c *managedPrefixConn) CloseRead() error {
	if conn, ok := c.Conn.(interface{ CloseRead() error }); ok {
		return conn.CloseRead()
	}
	return errors.New("connection does not support CloseRead")
}

// Successful TLS serving owns its passed listener exactly once.
type managedOnceListener struct {
	net.Listener
	once sync.Once
	err  error
}

func (l *managedOnceListener) Close() error {
	l.once.Do(func() { l.err = l.Listener.Close() })
	return l.err
}
