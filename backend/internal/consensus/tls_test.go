package consensus

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hashicorp/raft"
	"lockguard/backend/internal/lockstate"
)

// generateTestCerts creates a self-signed CA and a server cert in the given dir.
func generateTestCerts(t *testing.T, dir, name string) (caFile, certFile, keyFile string) {
	t.Helper()

	// 1. Generate CA
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate CA key: %v", err)
	}

	caTemplate := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"LockGuard Test CA - " + name},
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}

	caBytes, err := x509.CreateCertificate(rand.Reader, &caTemplate, &caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("failed to create CA certificate: %v", err)
	}

	caFile = filepath.Join(dir, fmt.Sprintf("ca-%s.pem", name))
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caBytes})
	if err := os.WriteFile(caFile, caPEM, 0600); err != nil {
		t.Fatalf("failed to write CA file: %v", err)
	}

	// 2. Generate Server / Peer Cert
	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate server key: %v", err)
	}

	serverTemplate := x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject: pkix.Name{
			Organization: []string{"LockGuard Test Peer - " + name},
		},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:    []string{"localhost", name},
		NotBefore:   time.Now().Add(-1 * time.Hour),
		NotAfter:    time.Now().Add(24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}

	serverBytes, err := x509.CreateCertificate(rand.Reader, &serverTemplate, &caTemplate, &serverKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("failed to create server certificate: %v", err)
	}

	certFile = filepath.Join(dir, fmt.Sprintf("peer-%s.crt", name))
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverBytes})
	if err := os.WriteFile(certFile, certPEM, 0600); err != nil {
		t.Fatalf("failed to write cert file: %v", err)
	}

	keyFile = filepath.Join(dir, fmt.Sprintf("peer-%s.key", name))
	keyBytes, err := x509.MarshalECPrivateKey(serverKey)
	if err != nil {
		t.Fatalf("failed to marshal EC private key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})
	if err := os.WriteFile(keyFile, keyPEM, 0600); err != nil {
		t.Fatalf("failed to write key file: %v", err)
	}

	return caFile, certFile, keyFile
}

func generatePeerCertWithCA(t *testing.T, dir, name, caCertFile, caKeyFile string, caCert *x509.Certificate, caKey *ecdsa.PrivateKey) (certFile, keyFile string) {
	t.Helper()

	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate server key: %v", err)
	}

	serverTemplate := x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject: pkix.Name{
			Organization: []string{"LockGuard Peer " + name},
		},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:    []string{"localhost", name},
		NotBefore:   time.Now().Add(-1 * time.Hour),
		NotAfter:    time.Now().Add(24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}

	serverBytes, err := x509.CreateCertificate(rand.Reader, &serverTemplate, caCert, &serverKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("failed to create server certificate: %v", err)
	}

	certFile = filepath.Join(dir, fmt.Sprintf("%s.crt", name))
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverBytes})
	if err := os.WriteFile(certFile, certPEM, 0600); err != nil {
		t.Fatalf("failed to write cert file: %v", err)
	}

	keyFile = filepath.Join(dir, fmt.Sprintf("%s.key", name))
	keyBytes, err := x509.MarshalECPrivateKey(serverKey)
	if err != nil {
		t.Fatalf("failed to marshal EC private key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})
	if err := os.WriteFile(keyFile, keyPEM, 0600); err != nil {
		t.Fatalf("failed to write key file: %v", err)
	}

	return certFile, keyFile
}

func TestTLSStreamLayer_Handshake(t *testing.T) {
	dir := t.TempDir()
	caFile, certFile, keyFile := generateTestCerts(t, dir, "node1")

	tlsConf, err := BuildTLSConfig(certFile, keyFile, caFile)
	if err != nil {
		t.Fatalf("BuildTLSConfig failed: %v", err)
	}

	stream, err := NewTLSStreamLayer("127.0.0.1:0", nil, tlsConf)
	if err != nil {
		t.Fatalf("NewTLSStreamLayer failed: %v", err)
	}
	defer stream.Close()

	addr := stream.Addr().String()

	// Connect client
	doneCh := make(chan error, 1)
	go func() {
		conn, err := stream.Accept()
		if err != nil {
			doneCh <- err
			return
		}
		defer conn.Close()
		buf := make([]byte, 4)
		_, err = conn.Read(buf)
		doneCh <- err
	}()

	clientConn, err := stream.Dial(raft.ServerAddress(addr), 2*time.Second)
	if err != nil {
		t.Fatalf("stream.Dial failed: %v", err)
	}
	defer clientConn.Close()

	if _, err := clientConn.Write([]byte("PING")); err != nil {
		t.Fatalf("failed to write on clientConn: %v", err)
	}

	select {
	case err := <-doneCh:
		if err != nil {
			t.Fatalf("server accept/read error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for TLS handshake")
	}
}

func TestTLSStreamLayer_UntrustedPeerRejection(t *testing.T) {
	dir := t.TempDir()

	// 1. Valid CA and Server
	caFile, srvCert, srvKey := generateTestCerts(t, dir, "cluster")
	srvTLS, err := BuildTLSConfig(srvCert, srvKey, caFile)
	if err != nil {
		t.Fatalf("BuildTLSConfig for server failed: %v", err)
	}

	stream, err := NewTLSStreamLayer("127.0.0.1:0", nil, srvTLS)
	if err != nil {
		t.Fatalf("NewTLSStreamLayer failed: %v", err)
	}
	defer stream.Close()

	addr := stream.Addr().String()

	// Server accept loop expecting failure
	errCh := make(chan error, 1)
	go func() {
		conn, err := stream.Accept()
		if err != nil {
			errCh <- err
			return
		}
		defer conn.Close()
		buf := make([]byte, 10)
		_, readErr := conn.Read(buf)
		errCh <- readErr
	}()

	// 2. Rogue client with certificate signed by an UNTRUSTED foreign CA
	rogueDir := t.TempDir()
	rogueCA, rogueCert, rogueKey := generateTestCerts(t, rogueDir, "rogue")
	rogueTLS, err := BuildTLSConfig(rogueCert, rogueKey, rogueCA)
	if err != nil {
		t.Fatalf("BuildTLSConfig for rogue failed: %v", err)
	}

	dialer := &net.Dialer{Timeout: 2 * time.Second}
	clientConn, err := tls.DialWithDialer(dialer, "tcp", addr, rogueTLS)

	// Either dial fails during handshake or server drops unauthenticated connection
	if err == nil {
		defer clientConn.Close()
		_, _ = clientConn.Write([]byte("UNAUTHORIZED"))
	}

	select {
	case srvErr := <-errCh:
		if srvErr == nil {
			t.Errorf("expected server to reject untrusted client certificate, but connection succeeded")
		}
	case <-time.After(2 * time.Second):
		// Dial failure was already caught
	}
}

func getFreePorts(count int) ([]int, error) {
	var ports []int
	var listeners []net.Listener
	for i := 0; i < count; i++ {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		ports = append(ports, l.Addr().(*net.TCPAddr).Port)
		listeners = append(listeners, l)
	}
	for _, l := range listeners {
		_ = l.Close()
	}
	return ports, nil
}

// TestRaft_ThreeNodeCluster_MutualTLS verifies full 3-node Raft consensus over mTLS.
func TestRaft_ThreeNodeCluster_MutualTLS(t *testing.T) {
	dir := t.TempDir()

	// 1. Create Cluster Root CA
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate CA key: %v", err)
	}

	caTemplate := x509.Certificate{
		SerialNumber: big.NewInt(100),
		Subject: pkix.Name{
			Organization: []string{"LockGuard Cluster CA"},
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}

	caBytes, err := x509.CreateCertificate(rand.Reader, &caTemplate, &caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("failed to create CA certificate: %v", err)
	}

	caFile := filepath.Join(dir, "ca.pem")
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caBytes})
	_ = os.WriteFile(caFile, caPEM, 0600)

	ports, err := getFreePorts(3)
	if err != nil {
		t.Fatalf("failed to obtain free ports: %v", err)
	}

	nodeIDs := []string{"mtls-node-a", "mtls-node-b", "mtls-node-c"}
	raftServers := make([]raft.Server, 3)
	for i, id := range nodeIDs {
		raftServers[i] = raft.Server{
			ID:       raft.ServerID(id),
			Address:  raft.ServerAddress(fmt.Sprintf("127.0.0.1:%d", ports[i])),
			Suffrage: raft.Voter,
		}
	}

	nodes := make([]*RaftNode, 3)
	for i, id := range nodeIDs {
		certFile, keyFile := generatePeerCertWithCA(t, dir, id, caFile, "", &caTemplate, caKey)

		cfg := NodeConfig{
			NodeID:             id,
			BindAddr:           fmt.Sprintf("127.0.0.1:%d", ports[i]),
			AdvertiseAddr:      fmt.Sprintf("127.0.0.1:%d", ports[i]),
			DataDir:            filepath.Join(dir, "data-"+id),
			HeartbeatTimeout:   50 * time.Millisecond,
			ElectionTimeout:    100 * time.Millisecond,
			CommitTimeout:      10 * time.Millisecond,
			LeaderLeaseTimeout: 50 * time.Millisecond,
			BootstrapCluster:   (i == 0),
			BootstrapServers:   raftServers,
			EnableTLS:          true,
			TLSCertFile:        certFile,
			TLSKeyFile:         keyFile,
			TLSCAFile:          caFile,
			LogLevel:           "error",
		}

		sm := lockstate.New()
		rn, err := NewRaftNode(cfg, sm)
		if err != nil {
			t.Fatalf("failed to start Raft mTLS node %s: %v", id, err)
		}
		nodes[i] = rn
	}

	defer func() {
		for _, n := range nodes {
			if n != nil {
				_ = n.Close()
			}
		}
	}()

	// 2. Await leader election over mTLS
	var leader *RaftNode
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		for _, n := range nodes {
			if n.IsLeader() {
				leader = n
				break
			}
		}
		if leader != nil {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}

	if leader == nil {
		t.Fatalf("timeout waiting for 3-node Raft mTLS leader election")
	}

	// 3. Replicate state command over mTLS
	cmd := lockstate.AcquireLockCommand{
		RequestID:         "req-mtls-1",
		ResourceName:      "res-mtls-lock",
		OwnerID:           "worker-mtls",
		TTLNanoseconds:    10 * int64(time.Second),
		TimestampUnixNano: time.Now().UnixNano(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	res, err := leader.ApplyCommand(ctx, cmd)
	if err != nil {
		t.Fatalf("failed to acquire lock over Raft mTLS: %v", err)
	}

	if !res.Success || res.FencingToken < 1 {
		t.Fatalf("expected successful AcquireResult with fencing token >= 1, got: %+v", res)
	}

	// 4. Test Node Failure & Quorum Retention: stop node C, verify cluster remains operational
	_ = nodes[2].Close()
	nodes[2] = nil
	time.Sleep(100 * time.Millisecond)

	// Leader can still commit with quorum of 2 (mtls-node-a and mtls-node-b)
	cmd2 := lockstate.AcquireLockCommand{
		RequestID:         "req-mtls-2",
		ResourceName:      "res-mtls-lock-2",
		OwnerID:           "worker-mtls",
		TTLNanoseconds:    10 * int64(time.Second),
		TimestampUnixNano: time.Now().UnixNano(),
	}
	res2, err := leader.ApplyCommand(ctx, cmd2)
	if err != nil || !res2.Success {
		t.Fatalf("quorum commit failed after node C stopped: %v, res: %+v", err, res2)
	}

	// 5. Verify untrusted rogue node dial rejection
	rogueDir := t.TempDir()
	rogueCA, rogueCert, rogueKey := generateTestCerts(t, rogueDir, "rogue-peer")
	rogueTLS, _ := BuildTLSConfig(rogueCert, rogueKey, rogueCA)

	dialer := &net.Dialer{Timeout: 1 * time.Second}
	rogueConn, err := tls.DialWithDialer(dialer, "tcp", fmt.Sprintf("127.0.0.1:%d", ports[0]), rogueTLS)
	if err == nil {
		buf := make([]byte, 10)
		_ = rogueConn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		_, readErr := rogueConn.Read(buf)
		_ = rogueConn.Close()
		if readErr == nil {
			t.Errorf("expected mTLS peer connection from rogue node to fail, but succeeded")
		}
	}
}
