# Shared Go authentication

`authcore` handles login identity for the Romanian arcade and social server.
Applications own persistent PostgreSQL adapters, age and parental-consent gates,
roles, permissions, privacy notices and account erasure.

`New(Config, Store)` requires an HTTPS public origin (HTTP is allowed only on
loopback). It registers native username/password signup/login, logout, current
identity, CSRF and Google/Facebook authorization-code handlers. Provider buttons
remain unavailable until credentials are configured. Google uses pinned
`coreos/go-oidc` signature/issuer/audience/expiry verification. External accounts
are identified by provider plus subject and never linked by matching email.

Password hashes use Django-compatible PBKDF2-SHA256 with one million iterations;
four hashing workers bound CPU concurrency. Existing supported Django hashes
continue to verify, including PBKDF2-SHA1, Argon2i/id, bcrypt-SHA256/bcrypt and
scrypt. Work factors are bounded; memory-hard verification is serialized with a
128-MiB per-operation ceiling. Cookies carry random opaque tokens; adapters store their
SHA-256 hashes and expiry, check user status on every read and revoke on logout.
Session cookies use HttpOnly, Secure in HTTPS and SameSite=Lax. Write handlers
require both a matching public-origin Origin/Referer and the CSRF cookie/header.

OAuth state, PKCE and Google nonce are browser-bound, five-minute, single-use.
Persistent adapters implement `OAuthFlowStore` for atomic consumption across
replicas and `AuthAttemptStore` for a shared login-attempt budget. Without those
interfaces the bounded fallback retains up to 1,024 flows and 4,096 counters in
one process; a restart rejects unfinished flows. Normalize the server's
`RemoteAddr` only through explicitly trusted proxy middleware. The core ignores
arbitrary forwarded-address headers.

Facebook additionally verifies token application ID, stable app-scoped user ID
and expiry, then requests the matching profile using an app-secret proof. Its
API version must be supplied explicitly as `vNN.0`. PKCE parameters are sent;
provider acceptance must be checked in the registered-app integration gate.
Provider token exchanges have a ten-second timeout and one-MiB response limit.
Redirect responses are not followed, token material is not logged or persisted,
and remote avatar files are not fetched.

Optional `OAuthCallbackPaths` preserves existing registered callback paths.
`EnsureCSRF`, `Authenticate`, `CheckCSRF`, `RevokeSession` and `ClearCookies` support
application-specific payloads and handlers. `Register` mounts the default routes
under a caller-provided prefix such as `/api/auth`; applications with a custom
logout payload should mount individual methods instead of registering that route
twice.

Run `go test -race ./...` and `go vet ./...` from this module. Tests use local mock
providers and generated RSA keys, never production accounts or credentials.
