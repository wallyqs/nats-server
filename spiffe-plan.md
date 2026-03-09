# SPIFFE Identity Support for NATS Auth Callout

## Problem

NATS JWTs embed permissions (claims) directly in the token, making authorization decentralized. With SPIFFE X509-SVIDs, you get strong workload **identity** but no built-in **authorization** — permissions end up in static config files, losing the dynamic, decentralized nature of JWT-based auth.

## Solution: Auth Callout with SPIFFE Identity

Bridge SPIFFE identity with NATS's existing JWT authorization model using the auth callout mechanism:

```
Client (X509-SVID) → NATS Server → Auth Callout Service → returns signed user JWT
                                         │
                                         ▼
                                    Policy Engine
                                    (OPA, SPIRE, DB, etc.)
```

1. Client presents X509-SVID (TLS cert with SPIFFE ID in URI SAN)
2. NATS server extracts `spiffe://example.org/payments/processor`
3. Auth callout service receives the SPIFFE ID directly in the request
4. Service looks up permissions from a policy engine
5. Service returns a signed NATS user JWT with embedded permissions
6. NATS applies the JWT — standard NATS auth from here on

This gives decentralized, dynamic authorization while using SPIFFE for identity.

## What Was Implemented

### Configuration

A `spiffe` block inside `auth_callout` enables SPIFFE identity extraction and optional trust domain validation:

```hcl
authorization {
  auth_callout {
    issuer: "ABJHLOVMPA..."
    account: AUTH
    auth_users: [ auth ]
    spiffe {
      trust_domains: ["example.org", "prod.example.com"]
    }
  }
}
```

- `trust_domains` — optional allowlist of SPIFFE trust domains. If empty, all domains are accepted. Clients with SPIFFE IDs from non-listed domains are rejected before the auth callout is invoked.

### Server-Side Changes

#### New: `server/spiffe.go`

SPIFFE helper functions:

- `SPIFFEConfig` struct — holds `TrustDomains []string`
- `isSPIFFEID(u *url.URL) bool` — validates URI matches SPIFFE spec (scheme `spiffe`, non-empty host, no query/fragment/port/userinfo)
- `extractSPIFFEIDs(cert *x509.Certificate) []string` — extracts all SPIFFE IDs from a certificate's URI SANs
- `extractSPIFFEIDsFromChains(verifiedChains, peerCerts) []string` — extracts from TLS state (verified chains preferred, falls back to peer certs)
- `trustDomain(spiffeID string) string` — extracts the trust domain from a SPIFFE ID
- `validateSPIFFETrustDomain(spiffeID, allowedDomains) error` — validates against configured trust domains

#### Modified: `server/opts.go`

- Added `SPIFFE *SPIFFEConfig` field to `AuthCallout` struct
- Added `parseSPIFFEConfig()` for config file parsing
- Added `"spiffe"` case in `parseAuthCallout()`

#### Modified: `server/client.go`

- Added `spiffeID string` field to the `client` struct for identity tracking

#### Modified: `server/auth_callout.go`

In `processClientOrLeafCallout()`, after TLS certificate processing:

1. **Extraction**: When `spiffe` config is present, extracts SPIFFE IDs from the client's TLS certificate
2. **Identity propagation**: Sets `ClientInformation.User` to the primary SPIFFE ID when no other user identity (username, nkey, JWT) is provided
3. **Tag propagation**: Adds all SPIFFE IDs as `spiffe-id:`-prefixed tags on the `AuthorizationRequest` for easy identification by the auth callout service
4. **Trust domain validation**: After unlocking the client mutex, validates the SPIFFE ID's trust domain against the configured allowlist before sending the request

### How the Auth Callout Service Receives SPIFFE IDs

The SPIFFE ID is available in two places in the `AuthorizationRequestClaims`:

| Field | Value | Notes |
|---|---|---|
| `client_info.user` | `spiffe://example.org/payments/processor` | Primary SPIFFE ID, preserves original case. Only set when client has no other identity. |
| `tags` | `["spiffe-id:spiffe://example.org/payments/processor"]` | All SPIFFE IDs, lowercased (TagList normalizes). Always set when SPIFFE IDs are found. |

The full TLS certificate chain is still available in `client_tls.verified_chains` / `client_tls.certs` for services that need additional certificate inspection.

### Tests

#### Unit tests: `server/spiffe_test.go`

- `TestIsSPIFFEID` — validates SPIFFE URI detection (valid IDs, non-SPIFFE URIs, edge cases)
- `TestExtractSPIFFEIDs` — extraction from certificates with/without SPIFFE URIs
- `TestTrustDomain` — trust domain extraction from SPIFFE ID strings
- `TestValidateSPIFFETrustDomain` — trust domain validation (allowed, disallowed, case-insensitive, empty list)
- `TestExtractSPIFFEIDsFromChains` — extraction from verified chains vs peer certs

#### Integration tests: `server/auth_callout_test.go`

- `TestAuthCalloutSPIFFEIdentity` — full flow: SPIFFE SVID cert → auth callout receives SPIFFE ID in `client_info.user` and tags → service returns JWT with specific permissions → client verified in correct account with correct identity
- `TestAuthCalloutSPIFFETrustDomainRejection` — client with SPIFFE ID from `localhost` domain rejected when only `example.org` is configured
- `TestAuthCalloutSPIFFEMultipleTrustDomains` — client accepted when its trust domain is in the allowed list among multiple domains

## Design Decisions

1. **No JWT library changes required** — SPIFFE IDs are passed through existing fields (`ClientInformation.User` and `AuthorizationRequest.Tags`) rather than adding new fields to the external `nats-io/jwt/v2` library.

2. **Opt-in via config** — SPIFFE extraction only happens when the `spiffe` block is configured inside `auth_callout`. Existing TLS auth callout behavior is unchanged.

3. **Trust domain validation before callout** — Invalid trust domains are rejected server-side before the auth callout request is sent, avoiding unnecessary round-trips to the auth service.

4. **Tags preserve all SPIFFE IDs** — A certificate may contain multiple SPIFFE IDs (unusual but spec-valid). All are included as tags. The primary ID (first one) is used for `ClientInformation.User`.

## Future Considerations

- **SPIRE Workload API integration** — Instead of relying on static TLS config, the NATS server could act as a SPIRE workload and use the Workload API to obtain its own SVID and validate client SVIDs.
- **SPIRE registration entry metadata** — The auth callout service could query SPIRE's Registration API to fetch metadata attached to a SPIFFE ID and derive NATS permissions from it.
- **OPA integration example** — An example auth callout service that uses Open Policy Agent to map SPIFFE IDs to NATS permissions.
- **JWT library enhancement** — Adding a dedicated `SpiffeIDs []string` field to `ClientTLS` or `ClientInformation` in `nats-io/jwt/v2` would be cleaner than using tags.
