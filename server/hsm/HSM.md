# NATS Server HSM (PKCS#11) Support

## Why HSM?

In a standard TLS setup, the server's private key sits on disk as a PEM file. Anyone
with read access to that file can impersonate the server. Hardware Security Modules (HSMs)
solve this by keeping the private key inside tamper-resistant hardware — the key **never
leaves the device**. The server sends data to the HSM for signing, and gets back a
signature. Even if the server is fully compromised, the attacker cannot extract the key.

This matters for:

- **Compliance** — PCI-DSS, FIPS 140-2/3, and similar standards often require HSM-protected keys.
- **Key lifecycle** — HSMs provide secure key generation, rotation, and destruction.
- **Audit** — HSM access is logged at the hardware level.

## Do clients need changes?

**No.** HSM support is entirely server-side. From the client's perspective, nothing changes.
The TLS handshake is standard — the server presents its certificate and proves possession
of the private key via a signature. Whether that signature came from a PEM file on disk or
an HSM is invisible to the client.

A Go client connects the same way it always does:

```go
nc, err := nats.Connect("tls://myserver:4222",
    nats.RootCAs("./ca-cert.pem"),        // to verify the server's cert
)
```

Or with mutual TLS (client certs):

```go
nc, err := nats.Connect("tls://myserver:4222",
    nats.RootCAs("./ca-cert.pem"),
    nats.ClientCert("./client-cert.pem", "./client-key.pem"),
)
```

These are identical whether the server uses a key file or an HSM.

## Building with HSM support

HSM support uses CGo (for `dlopen` of the PKCS#11 library), so it is gated behind
a build tag to keep default builds pure Go:

```bash
go build -tags hsm ./...
```

Without the tag, configuring `hsm {}` in the config file will produce a clear error:

    hsm: support not available, build with '-tags hsm'

## Configuration

The `hsm {}` block goes inside the `tls {}` block. It replaces `key_file` — the
certificate is still loaded from a PEM file via `cert_file`:

```
# nats-server.conf
listen: "0.0.0.0:4222"

tls {
    cert_file: "/path/to/server-cert.pem"
    ca_file:   "/path/to/ca-cert.pem"

    hsm {
        provider:    "/usr/lib/softhsm/libsofthsm2.so"
        token_label: "nats-production"
        key_label:   "nats-tls-key"
        pin:         $HSM_PIN
    }
}
```

### HSM config fields

| Field         | Required | Description |
|---------------|----------|-------------|
| `provider`    | Yes      | Path to the PKCS#11 shared library (`.so`/`.dylib`/`.dll`) |
| `token_label` | Yes      | Label of the HSM token/slot to use |
| `key_label`   | *        | CKA_LABEL attribute to find the private key |
| `key_id`      | *        | CKA_ID attribute (hex-encoded) to find the private key |
| `pin`         | No       | Token PIN for login |

\* At least one of `key_label` or `key_id` is required. Both can be specified.

### PIN handling

The `pin` field supports several approaches:

```
# Environment variable (recommended for production)
pin: $HSM_PIN

# Literal value (ok for development/testing)
pin: "1234"

# Omitted entirely — some HSMs don't require a PIN,
# or the token may already be logged in.
```

For production, set the environment variable before starting the server:

```bash
export HSM_PIN="your-secret-pin"
nats-server -c nats-server.conf
```

### Conflict rules

The `hsm` block cannot be combined with:
- `key_file` — the HSM replaces the key file
- `cert_store` — Windows certificate store is a separate mechanism
- `certs` — multiple certificate pairs (each would need its own HSM config)

## Quick start with SoftHSM2 (testing)

SoftHSM2 is a software PKCS#11 implementation for testing without real hardware.

```bash
# Install SoftHSM2
# Ubuntu/Debian:
sudo apt-get install softhsm2

# macOS:
brew install softhsm

# Initialize a token
softhsm2-util --init-token --slot 0 --label "nats-test" \
    --pin 1234 --so-pin 5678

# Generate an RSA key pair
pkcs11-tool --module /usr/lib/softhsm/libsofthsm2.so \
    --login --pin 1234 --token-label "nats-test" \
    --keypairgen --key-type rsa:2048 \
    --label "nats-tls-key" --id 01

# Export the public key and create a certificate (using OpenSSL)
# First extract the public key:
pkcs11-tool --module /usr/lib/softhsm/libsofthsm2.so \
    --login --pin 1234 --token-label "nats-test" \
    --read-object --type pubkey --label "nats-tls-key" \
    -o pubkey.der

openssl rsa -pubin -inform DER -in pubkey.der -out pubkey.pem

# Create a self-signed certificate using the extracted public key
# (In production, use your CA to sign a CSR instead)
openssl req -new -x509 -key pubkey.pem -out server-cert.pem \
    -days 365 -subj "/CN=localhost"
```

Then configure the server:

```
listen: "0.0.0.0:4222"

tls {
    cert_file: "./server-cert.pem"

    hsm {
        provider:    "/usr/lib/softhsm/libsofthsm2.so"
        token_label: "nats-test"
        key_label:   "nats-tls-key"
        pin:         1234
    }
}
```

Build and run:

```bash
go build -tags hsm -o nats-server .
./nats-server -c nats-server.conf
```

## Supported key types and TLS versions

| Key Type | TLS 1.2 | TLS 1.3 |
|----------|---------|---------|
| RSA (PKCS#1 v1.5) | Yes | — |
| RSA-PSS | Yes | Yes |
| ECDSA (P-256, P-384, P-521) | Yes | Yes |

## Architecture

```
┌──────────────┐     TLS Handshake      ┌──────────────────┐
│  NATS Client │◄──────────────────────►│   NATS Server    │
│              │   (standard TLS,       │                  │
│  no changes  │    client unaware      │  cert from file  │
│   needed)    │    of HSM)             │  key ops → HSM   │
└──────────────┘                        └────────┬─────────┘
                                                 │ PKCS#11
                                                 │ (dlopen)
                                        ┌────────▼─────────┐
                                        │   HSM / Token     │
                                        │                   │
                                        │  Private key      │
                                        │  never leaves     │
                                        │  the device       │
                                        └───────────────────┘
```

The server loads the PKCS#11 shared library at startup via `dlopen`, opens a session
to the token, and locates the private key. During each TLS handshake, Go's `crypto/tls`
calls the `crypto.Signer.Sign()` method, which delegates to the HSM via PKCS#11
`C_SignInit`/`C_Sign`. The certificate chain is served from the PEM file as usual.

## Limitations (POC)

- **Single session** — Uses one PKCS#11 session with a mutex. Production would benefit
  from a session pool for high-concurrency workloads.
- **No hot reload** — HSM config changes require a server restart.
- **Linux/macOS only** — The CGo `dlopen` approach works on Unix-like systems. Windows
  HSMs would use the existing `cert_store` mechanism instead.
- **Build tag required** — Must build with `-tags hsm` to include PKCS#11 support.
