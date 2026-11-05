# LockGuard Inter-Service TLS & Certificate Specification

**Document Version:** 1.0.0  
**Phase:** 5 (Security Hardening, Performance Optimization & Production Readiness)  
**Classification:** Infrastructure Security & Cryptographic Transport Specification  
**System Title:** Fault Tolerant Distributed Lock Service (LOCKGUARD)

---

## 1. Overview & Consensus Transport Security

In a distributed consensus cluster, the consensus transport exchanges log entries, heartbeats, install-snapshot streams, and leader elections among peer coordinators. If inter-node communication is unencrypted, network adversaries on the shared network could:
1. Eavesdrop on sensitive resource names, lease metadata, and client identifiers.
2. Inject forged log entries or disrupt election terms.
3. Introduce unauthorized rogue nodes into the Raft membership.

LockGuard addresses these vulnerabilities by introducing **Mutual TLS (mTLS)** for HashiCorp Raft peer-to-peer transport using Go standard library cryptography (`crypto/tls`, `crypto/x509`).

---

## 2. TLS Architecture & HashiCorp Raft Integration

HashiCorp Raft abstracts network communication through the `raft.Transport` interface. LockGuard implements a native `TLSStreamLayer` (`backend/internal/consensus/tls.go`) fulfilling `raft.StreamLayer`:

```go
type StreamLayer interface {
    net.Listener
    Dial(address raft.ServerAddress, timeout time.Duration) (net.Conn, error)
}
```

### 2.1 Cipher Suite & Protocol Version Enforcement
- **Minimum Protocol Version:** `tls.VersionTLS13` (strictly enforced, rejecting legacy TLS 1.0, 1.1, and 1.2).
- **Mutual Authentication (`mTLS`):** When a Certificate Authority (`TLSCAFile`) is configured, the server sets:
  ```go
  tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
  ```
  Every connecting peer coordinator MUST present a valid x509 client certificate signed by the trusted cluster CA.

---

## 3. Configuration Parameters

Raft TLS options can be configured via environment variables or the configuration file:

| Environment Variable | Config Field | Default | Description |
|---|---|---|---|
| `RAFT_ENABLE_TLS` | `cfg.Raft.EnableTLS` | `false` | Enables TLS transport for Raft peer consensus. |
| `RAFT_TLS_CERT_FILE` | `cfg.Raft.TLSCertFile` | `""` | Path to node x509 public certificate (`.crt` / `.pem`). |
| `RAFT_TLS_KEY_FILE` | `cfg.Raft.TLSKeyFile` | `""` | Path to node private key (`.key`). |
| `RAFT_TLS_CA_FILE` | `cfg.Raft.TLSCAFile` | `""` | Path to root CA certificate (`ca.pem`) for peer verification. |

---

## 4. Certificate Generation Guide (OpenSSL / PKI)

To deploy LockGuard with mTLS in production, generate certificates using standard OpenSSL procedures:

### Step 1: Generate Private CA
```bash
# Generate CA private key
openssl ecparam -name prime256v1 -genkey -noout -out ca.key

# Create self-signed root CA certificate
openssl req -new -x509 -sha256 -key ca.key -out ca.pem -days 365 \
  -subj "/C=US/ST=CA/O=LockGuard/CN=LockGuard Root CA"
```

### Step 2: Generate Peer Certificates for Coordinators
For each coordinator node (`coordinator-a`, `coordinator-b`, `coordinator-c`):
```bash
# Generate coordinator private key
openssl ecparam -name prime256v1 -genkey -noout -out peer.key

# Generate CSR
openssl req -new -sha256 -key peer.key -out peer.csr \
  -subj "/C=US/ST=CA/O=LockGuard/CN=coordinator-a"

# Sign certificate with CA and subjectAltNames
openssl x509 -req -in peer.csr -CA ca.pem -CAkey ca.key -CAcreateserial \
  -out peer.crt -days 365 -sha256 \
  -extfile <(printf "subjectAltName=DNS:coordinator-a,DNS:localhost,IP:127.0.0.1")
```

---

## 5. Verification & Security Testing

The Raft TLS layer is covered by automated unit tests in `backend/internal/consensus/tls_test.go`:
- `TestTLSStreamLayer_Handshake`: Dynamically creates an in-memory ECDSA CA, issues server and client certificates, establishes an mTLS stream, performs mutual handshakes, and validates byte transfer over TLS 1.3.

