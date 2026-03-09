# SPIFFE Auth Callout — End-to-End Example

This example demonstrates NATS auth callout with SPIFFE X509-SVID identity.

A workload presents its SPIFFE SVID certificate to NATS. The server extracts
the SPIFFE ID and forwards it to an auth callout service, which looks up
permissions from a policy file and returns a signed user JWT.

```
Client (X509-SVID) → NATS Server → Auth Callout Service → policies.json
                                         ↓
                                    Signed User JWT
```

## Components

| Component | Path | Description |
|-----------|------|-------------|
| Auth service | `auth-service/` | Subscribes to `$SYS.REQ.USER.AUTH`, maps SPIFFE IDs to NATS permissions |
| Client | `client/` | Connects with an SVID cert (no username/password), publishes and subscribes |
| Server config | `nats-server.conf` | NATS server with TLS + auth callout + SPIFFE enabled |
| Policies | `policies.json` | SPIFFE ID → account/permissions mapping |

## Prerequisites

- Go 1.25+
- The NATS server binary (build from this repo: `go build .`)
- Test SVID certificates are already in `test/configs/certs/svid/`

## Running

### 1. Start the NATS server

From this directory:

```bash
# Build the server first (from repo root)
cd ../.. && go build -o nats-server . && cd -

# Start with the e2e config
../../nats-server -c nats-server.conf
```

### 2. Start the auth callout service

```bash
cd auth-service
go run . \
  -nats tls://localhost:4222 \
  -ca ../../../test/configs/certs/svid/ca.pem \
  -cert ../../../test/configs/certs/svid/client-a.pem \
  -key ../../../test/configs/certs/svid/client-a.key \
  -policies ../policies.json
```

### 3. Connect a SPIFFE client

```bash
# As user-a (spiffe://localhost/my-nats-service/user-a)
cd client
go run . \
  -nats tls://localhost:4222 \
  -ca ../../../test/configs/certs/svid/ca.pem \
  -cert ../../../test/configs/certs/svid/client-a.pem \
  -key ../../../test/configs/certs/svid/client-a.key \
  -subject demo.hello

# As user-b (spiffe://localhost/my-nats-service/user-b) — different permissions
go run . \
  -nats tls://localhost:4222 \
  -ca ../../../test/configs/certs/svid/ca.pem \
  -cert ../../../test/configs/certs/svid/client-b.pem \
  -key ../../../test/configs/certs/svid/client-b.key \
  -subject demo.hello
```

## What Happens

1. Client connects with only a TLS certificate — no username or token
2. NATS extracts `spiffe://localhost/my-nats-service/user-a` from the cert's URI SAN
3. The auth callout service receives the SPIFFE ID in `client_info.user` and as a tag
4. It looks up `policies.json` and finds the matching entry
5. It returns a signed JWT granting `pub: demo.>` and `sub: demo.>` permissions
6. The client is placed in the `APP` account with those permissions

## Customizing Policies

Edit `policies.json` to add/change SPIFFE ID mappings:

```json
{
  "spiffe://example.org/my-service": {
    "account": "APP",
    "name": "my-service",
    "permissions": {
      "pub": { "allow": ["service.>"] },
      "sub": { "allow": ["service.>", "_INBOX.>"] }
    }
  }
}
```

## Trust Domain Validation

The `nats-server.conf` restricts accepted trust domains:

```hcl
spiffe {
  trust_domains: ["localhost"]
}
```

Clients with SPIFFE IDs from other trust domains are rejected at the TLS
level, before the auth callout is even invoked.
