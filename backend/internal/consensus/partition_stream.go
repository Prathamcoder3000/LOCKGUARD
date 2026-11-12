package consensus

import (
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hashicorp/raft"
)

// ErrNodeIsolated is returned by Dial when a simulated network partition isolates the node's Raft transport.
var ErrNodeIsolated = errors.New("consensus transport isolated by simulated network partition")

// PlainTCPStreamLayer implements raft.StreamLayer over standard TCP without TLS.
type PlainTCPStreamLayer struct {
	advertise net.Addr
	listener  net.Listener
}

// NewPlainTCPStreamLayer listens on bindAddr and advertises advAddr to peers.
func NewPlainTCPStreamLayer(bindAddr string, advAddr *net.TCPAddr) (*PlainTCPStreamLayer, error) {
	listener, err := net.Listen("tcp", bindAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to listen on %q: %w", bindAddr, err)
	}

	var adv net.Addr = listener.Addr()
	if advAddr != nil && advAddr.Port != 0 && len(advAddr.IP) > 0 {
		adv = advAddr
	}

	return &PlainTCPStreamLayer{
		advertise: adv,
		listener:  listener,
	}, nil
}

// Accept waits for and returns the next incoming TCP connection.
func (t *PlainTCPStreamLayer) Accept() (net.Conn, error) {
	return t.listener.Accept()
}

// Close stops listening for incoming TCP connections.
func (t *PlainTCPStreamLayer) Close() error {
	return t.listener.Close()
}

// Addr returns the advertised listener address.
func (t *PlainTCPStreamLayer) Addr() net.Addr {
	if t.advertise != nil {
		return t.advertise
	}
	return t.listener.Addr()
}

// Dial connects to the target Raft peer over TCP.
func (t *PlainTCPStreamLayer) Dial(address raft.ServerAddress, timeout time.Duration) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: timeout}
	return dialer.Dial("tcp", string(address))
}

// PartitionableStreamLayer wraps a raft.StreamLayer so Raft peer traffic can be deterministically
// isolated and healed at runtime without closing the underlying listener socket or management HTTP port.
type PartitionableStreamLayer struct {
	inner    raft.StreamLayer
	isolated atomic.Bool
	closed   atomic.Bool

	mu    sync.Mutex
	conns map[*trackedConn]struct{}
}

// NewPartitionableStreamLayer wraps an existing raft.StreamLayer with runtime isolation controls.
func NewPartitionableStreamLayer(inner raft.StreamLayer) *PartitionableStreamLayer {
	return &PartitionableStreamLayer{
		inner: inner,
		conns: make(map[*trackedConn]struct{}),
	}
}

type trackedConn struct {
	net.Conn
	layer *PartitionableStreamLayer
	once  sync.Once
}

func (c *trackedConn) Close() error {
	c.once.Do(func() {
		if c.layer != nil {
			c.layer.untrackConn(c)
		}
	})
	return c.Conn.Close()
}

func (p *PartitionableStreamLayer) trackConn(conn net.Conn) net.Conn {
	tc := &trackedConn{Conn: conn, layer: p}
	p.mu.Lock()
	p.conns[tc] = struct{}{}
	p.mu.Unlock()
	return tc
}

func (p *PartitionableStreamLayer) untrackConn(tc *trackedConn) {
	p.mu.Lock()
	delete(p.conns, tc)
	p.mu.Unlock()
}

// SetIsolated toggles simulated network isolation for this Raft node's transport.
// When isolated becomes true, all active peer connections are dropped immediately.
func (p *PartitionableStreamLayer) SetIsolated(isolated bool) {
	p.isolated.Store(isolated)
	if !isolated {
		return
	}

	p.mu.Lock()
	toClose := make([]*trackedConn, 0, len(p.conns))
	for tc := range p.conns {
		toClose = append(toClose, tc)
	}
	p.conns = make(map[*trackedConn]struct{})
	p.mu.Unlock()

	for _, tc := range toClose {
		_ = tc.Conn.Close()
	}
}

// IsIsolated reports whether the transport is currently rejecting peer RPCs.
func (p *PartitionableStreamLayer) IsIsolated() bool {
	return p.isolated.Load()
}

// Accept waits for incoming connections, immediately rejecting connections while isolated.
func (p *PartitionableStreamLayer) Accept() (net.Conn, error) {
	for {
		conn, err := p.inner.Accept()
		if err != nil {
			return nil, err
		}
		if p.closed.Load() {
			_ = conn.Close()
			return nil, net.ErrClosed
		}
		if p.isolated.Load() {
			_ = conn.Close()
			continue
		}
		return p.trackConn(conn), nil
	}
}

// Close closes the underlying listener and terminates tracked connections.
func (p *PartitionableStreamLayer) Close() error {
	p.closed.Store(true)
	err := p.inner.Close()

	p.mu.Lock()
	toClose := make([]*trackedConn, 0, len(p.conns))
	for tc := range p.conns {
		toClose = append(toClose, tc)
	}
	p.conns = make(map[*trackedConn]struct{})
	p.mu.Unlock()

	for _, tc := range toClose {
		_ = tc.Conn.Close()
	}
	return err
}

// Addr returns the underlying stream layer's advertised network address.
func (p *PartitionableStreamLayer) Addr() net.Addr {
	return p.inner.Addr()
}

// Dial connects to a remote Raft peer unless this node is isolated.
func (p *PartitionableStreamLayer) Dial(address raft.ServerAddress, timeout time.Duration) (net.Conn, error) {
	if p.closed.Load() {
		return nil, net.ErrClosed
	}
	if p.isolated.Load() {
		return nil, ErrNodeIsolated
	}
	conn, err := p.inner.Dial(address, timeout)
	if err != nil {
		return nil, err
	}
	if p.isolated.Load() || p.closed.Load() {
		_ = conn.Close()
		if p.closed.Load() {
			return nil, net.ErrClosed
		}
		return nil, ErrNodeIsolated
	}
	return p.trackConn(conn), nil
}
