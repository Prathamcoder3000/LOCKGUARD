package consensus

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/hashicorp/raft"
)

// TLSStreamLayer implements raft.StreamLayer with mutual TLS support.
type TLSStreamLayer struct {
	advertise net.Addr
	listener  net.Listener
	tlsConfig *tls.Config
}

// NewTLSStreamLayer creates a new TLS stream layer listening on bindAddr with mutual TLS.
func NewTLSStreamLayer(bindAddr string, advAddr net.Addr, tlsConfig *tls.Config) (*TLSStreamLayer, error) {
	if tlsConfig == nil {
		return nil, fmt.Errorf("tlsConfig cannot be nil")
	}

	listener, err := tls.Listen("tcp", bindAddr, tlsConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to listen on %s with TLS: %w", bindAddr, err)
	}

	adv := advAddr
	if adv == nil {
		adv = listener.Addr()
	}

	return &TLSStreamLayer{
		advertise: adv,
		listener:  listener,
		tlsConfig: tlsConfig,
	}, nil
}

// Accept waits for and returns the next connection to the listener.
func (t *TLSStreamLayer) Accept() (net.Conn, error) {
	return t.listener.Accept()
}

// Close closes the listener.
func (t *TLSStreamLayer) Close() error {
	return t.listener.Close()
}

// Addr returns the listener's network address.
func (t *TLSStreamLayer) Addr() net.Addr {
	if t.advertise != nil {
		return t.advertise
	}
	return t.listener.Addr()
}

// Dial opens a TLS connection to the remote Raft peer.
func (t *TLSStreamLayer) Dial(address raft.ServerAddress, timeout time.Duration) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: timeout}
	return tls.DialWithDialer(dialer, "tcp", string(address), t.tlsConfig)
}

// BuildTLSConfig builds a secure tls.Config for mutual authentication (mTLS) with TLS 1.3.
func BuildTLSConfig(certFile, keyFile, caFile string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load key pair (%s, %s): %w", certFile, keyFile, err)
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
	}

	if caFile != "" {
		caCert, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA certificate %s: %w", caFile, err)
		}
		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA certificate from %s", caFile)
		}
		tlsConfig.RootCAs = caCertPool
		tlsConfig.ClientCAs = caCertPool
		tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
	}

	return tlsConfig, nil
}

