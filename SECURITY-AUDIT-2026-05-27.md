# MuxCore Security Audit Report

**Date:** May 27, 2026
**Commit:** `0ebbb64` — sidecar service bridge (#54)
**Scope:** `Muxcore-Media/core` (84 Go files, ~13,810 LOC) + `core.wiki` (15 pages)
**Methodology:** ChromaDB-enhanced deep dive — 31 batch queries + 17 targeted queries across 22 collections
**Standards:** OWASP ASVS 4.0.3, OWASP Go-SCP, NIST SP 800-53, NIST SP 800-57, Bulletproof TLS, SLSA, OpenSSF, MITRE ATT&CK, CIS Docker/K8s Benchmarks

---

## Executive Summary

MuxCore's architecture is **fundamentally sound** — the fabric-only design isolates domain risk to modules, the default-deny TLS policy for gRPC is correct, the audit middleware chain is well-structured, and the CSP defaults are properly restrictive. However, the security audit identified **3 Critical, 6 High, 5 Medium, and 4 Low** findings across the supply chain, transport security, and input validation categories.

**Top 3 Risks:**
1. **Module supply chain has zero integrity verification** — `git clone` + `go build` with no checksum, signature, or provenance (SLSA Level 0)
2. **Spool fetcher is vulnerable to SSRF** — no scheme validation, private IP blocking, or response size limits
3. **No request body size limit on HTTP server** — trivial resource exhaustion via slow POST of arbitrary data

**What's good:** Default CSP is `default-src 'none'; frame-ancestors 'none'`, brute-force protection is implemented in auth middleware, audit logging is wired into all subsystems, gRPC TLS is mandatory by default, and the cluster join token is properly enforced with TLS requirement.

---

## Detailed Findings

### F01: Spool Fetcher — SSRF (Server-Side Request Forgery)
**Severity:** Critical | **CWE-918** | **CVSS:** 7.5 (AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:N/A:N)

**Location:** `internal/spool/fetcher.go:34-62` (`FetchTag`, `buildFetchURL`)

**Description:** The spool fetcher accepts an arbitrary `spoolURL` parameter and converts it to a fetch URL without validating the scheme, blocking private IPs, or restricting to a domain allowlist. An attacker controlling the `--spool` flag (or a misconfigured deployment) can cause MuxCore to make HTTP requests to internal services, cloud metadata endpoints, or arbitrary hosts.

**DB Reference:** OWASP Top 10 2021 — A10:2021 Server-Side Request Forgery (SSRF) — "Sanitize and validate all client-supplied input data, especially URL and file path inputs." OWASP Go-SCP — Input Validation — "Restrict URL schemes to https://, validate hosts, block private IP ranges."

**Attack Scenario:**
```bash
muxcored --spool "http://169.254.169.254/latest/meta-data/" --tag "iam"
# Fetcher builds: http://169.254.169.254/latest/meta-data/tags/iam.json
# Returns AWS instance metadata credentials
```

**Remediation:**
1. Restrict scheme to `https://` only (reject `http://`, `file://`, `gopher://`, etc.)
2. Resolve hostname and check `net.IP.IsPrivate()`, `IsLoopback()`, `IsLinkLocalUnicast()`, `IsUnspecified()`
3. For GitHub URLs specifically, enforce that the parsed URL matches `github.com` exactly
4. Set response body limit with `io.LimitReader(resp.Body, 1<<20)` (1MB)
5. Add `MUXCORE_SPOOL_VERIFY_SSL=true` enforcement

---

### F02: Module Binary Cloning Without Integrity Verification
**Severity:** Critical | **CWE-829** | **CVSS:** 8.1 (AV:N/AC:H/PR:N/UI:N/S:U/C:H/I:H/A:H)

**Location:** `internal/module/mgr/manager.go:60-110` (`Resolve`)

**Description:** Module binaries are resolved by `git clone --depth 1 --branch <version>` followed by `go build`. There is no checksum verification, no binary signature check, no provenance attestation, and no integrity check of any kind between clone and execution. A compromised git repository, man-in-the-middle attack, or dependency confusion vector injects arbitrary code that executes with the same privileges as MuxCore.

**DB Reference:** SLSA Framework — "Build L1: Provenance exists but is not verified. L2: Hosted build platform with signed provenance. L3: Hardened build platform with non-falsifiable provenance." OpenSSF Best Practices — "Use Sigstore for artifact signing and verification."

**Attack Scenario:**
1. Attacker compromises a module's GitHub repo or gains push access
2. Attacker pushes a tagged version with a malicious `cmd/module/main.go`
3. MuxCore operator runs `muxcored --tag default` → `git clone` + `go build`
4. Malicious module runs with MuxCore's privileges — reads config files, env vars, data stores

**Remediation:**
1. Add `checksum` field to `TagModule` in spool format (SHA-256 of the built binary)
2. After `go build`, compute SHA-256 of the binary and compare against the expected checksum
3. Reject the binary if checksums don't match (regardless of `required: true/false`)
4. Long-term: integrate Sigstore/cosign for binary signature verification
5. Pin to commit SHA, not branch name — `--branch <sha>` instead of `--branch <tag>`

---

### F03: No Request Body Size Limit on HTTP Server
**Severity:** Critical | **CWE-770** | **CVSS:** 7.5 (AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:H)

**Location:** `internal/api/server.go:49-55` (`NewServer`), `internal/api/server.go:149-168` (`rebuildChain`)

**Description:** The HTTP server configures read/write/idle timeouts and a `MaxHeaderBytes` limit, but has **no request body size limit**. There is no `http.MaxBytesReader` wrapping anywhere in the middleware chain. An attacker can exhaust server memory by sending a slow POST with an arbitrarily large body.

**DB Reference:** OWASP Go-SCP — Input Validation — "Set body size limits using `http.MaxBytesReader` to prevent resource exhaustion." OWASP Top 10 2021 — A05:2021 Security Misconfiguration.

**Attack Scenario:**
```bash
# Opens connection, sends headers, then drips 10GB at 1 byte/sec
python3 -c "
import socket, time
s = socket.socket()
s.connect(('localhost', 8080))
s.send(b'POST /api/ HTTP/1.1\r\nHost: localhost\r\nContent-Length: 10737418240\r\n\r\n')
while True:
    s.send(b'x')
    time.sleep(1)
"
```
Server memory grows until OOM killer terminates the process.

**Remediation:**
Add body size limit middleware as the **outermost** middleware in `rebuildChain()`:
```go
const maxBodySize = 10 << 20 // 10 MB
func maxBodyMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        r.Body = http.MaxBytesReader(w, r.Body, maxBodySize)
        next.ServeHTTP(w, r)
    })
}
```

---

### F04: HSTS Header Emitted Unconditionally (Even Over Plaintext)
**Severity:** High | **CWE-523** | **CVSS:** 5.9 (AV:N/AC:H/PR:N/UI:N/S:U/C:N/I:H/A:N)

**Location:** `internal/api/middleware.go:67` (`securityHeadersMiddleware`)

**Description:** `Strict-Transport-Security` header with `max-age=63072000; includeSubDomains; preload` is set on **every** response, even when the server is running in plaintext HTTP (`MUXCORE_INSECURE_DISABLE_TLS=true`). This tells browsers to **permanently** (2 years) refuse plaintext connections to this host and all subdomains. If the operator ever tests in plaintext mode on a real domain, the browser will refuse to connect to that domain for 2 years. The `preload` directive also submits the domain to browser HSTS preload lists permanently.

**DB Reference:** Bulletproof TLS and PKI, Chapter 10 — "HSTS should only be activated when you're committed to serving HTTPS exclusively." OWASP ASVS V14.4.1 — "Verify that every HTTP response contains a content security policy that is appropriate for the data context."

**Attack Scenario:**
1. Operator tests MuxCore locally with `MUXCORE_INSECURE_DISABLE_TLS=true` on port 8080
2. Operator accesses via `http://mymuxcore.local:8080` in Chrome
3. Chrome receives HSTS header: `max-age=63072000; includeSubDomains; preload`
4. Chrome now refuses ALL plaintext connections to `mymuxcore.local` and `*.mymuxcore.local` for 2 years
5. Operator cannot test on this domain without HTTPS — self-inflicted denial of service

**Remediation:**
```go
func securityHeadersMiddleware(next http.Handler, cspHeader string, tlsEnabled bool) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("X-Content-Type-Options", "nosniff")
        w.Header().Set("X-Frame-Options", "DENY")
        w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
        w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
        if tlsEnabled {
            w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
        }
        w.Header().Set("Content-Security-Policy", cspHeader)
        next.ServeHTTP(w, r)
    })
}
```
Wire `tlsEnabled := s.certFile != "" && s.keyFile != ""` into `rebuildChain()`.

---

### F05: gRPC Auth Interceptor Without Enforcement
**Severity:** High | **CWE-862** | **CVSS:** 7.5 (AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:N)

**Location:** `internal/grpcmesh/auth.go:24-54` (`AuthUnaryInterceptor`)

**Description:** The `AuthUnaryInterceptor` extracts a caller identity from gRPC metadata (`x-caller-id`) and propagates it into the context, but **never calls an Authorizer** to check permissions. All requests pass through regardless of caller identity or target method. The comment acknowledges this is "future" work, but the interceptor is already wired into the gRPC server (`main.go:125`). Any module that connects to the gRPC mesh can call any other module's methods.

**DB Reference:** OWASP ASVS V4.1.1 — "Verify that the application enforces access control rules on a trusted service layer." NIST SP 800-53 AC-3 — "Enforce approved authorizations for logical access to information and system resources."

**Attack Scenario:**
1. Attacker builds a minimal module binary that connects to the gRPC mesh
2. Module calls `ModuleRegistration.Register()` with attacker-controlled `ModuleInfo`
3. Once registered, the attacker's module calls `Mesh.Call("admin-ui", "admin.deleteAllUsers", nil)`
4. gRPC interceptor logs "anonymous call" at debug level — and passes it through
5. If admin-ui module exists and has no internal auth, the operation succeeds

**Remediation:**
Wire an Authorizer into the interceptor:
```go
func AuthUnaryInterceptor(authz contracts.Authorizer, idp contracts.IdentityProvider) grpc.UnaryServerInterceptor {
    return func(ctx, req, info, handler) {
        // Extract identity
        identity, _ := idp.ExtractIdentity(ctx)
        if identity == nil {
            return nil, status.Error(codes.Unauthenticated, "identity required")
        }
        // Enforce authorization
        allowed, _ := authz.Can(ctx, identity, info.FullMethod, "*")
        if !allowed {
            return nil, status.Error(codes.PermissionDenied, "insufficient authorization")
        }
        return handler(ctx, req)
    }
}
```

---

### F06: insecure.NewCredentials() Fallback for Heartbeat
**Severity:** High | **CWE-319** | **CVSS:** 7.5 (AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:N)

**Location:** `cmd/muxcored/main.go:298-303`

**Description:** When TLS credentials are nil (insecure mode enabled), the heartbeat loop falls back to `insecure.NewCredentials()`. This sends module lists and heartbeat metadata in cleartext between cluster nodes. An attacker on the network can observe which modules are running, node IDs, and cluster topology. While this only fires when TLS is explicitly disabled, the code path exists and the fallback is silent.

**DB Reference:** Bulletproof TLS — "Never use insecure.NewCredentials() in production. All gRPC connections between services must be encrypted." NIST SP 800-53 SC-8 — "Protect the confidentiality and integrity of transmitted information."

**Attack Scenario:**
1. Operator sets `MUXCORE_INSECURE_DISABLE_TLS=true` for development
2. Cluster nodes communicate heartbeats in cleartext
3. Attacker on shared network captures heartbeat packets → learns cluster topology, module names, node IDs
4. Attacker crafts a fake heartbeat to poison the cluster member list

**Remediation:**
```go
var hbDialOpts []grpc.DialOption
if creds != nil {
    hbDialOpts = append(hbDialOpts, grpc.WithTransportCredentials(creds))
} else {
    slog.Warn("heartbeat disabled: TLS is required for cluster heartbeat")
    // Don't start heartbeat loop at all — cross-node routing requires TLS anyway
}
if creds != nil {
    discoveryGrpc.StartHeartbeatLoop(ctx, hbDialOpts)
}
```

---

### F07: DatabaseConfig / CacheConfig URL Logged Without Redaction
**Severity:** High | **CWE-532** | **CVSS:** 6.5 (AV:N/AC:L/PR:L/UI:N/S:U/C:H/I:N/A:N)

**Location:** `internal/config/config.go:49-58` (`DatabaseConfig`, `CacheConfig`)

**Description:** Both `DatabaseConfig` and `CacheConfig` contain a `URL` field that typically carries embedded credentials (e.g., `postgres://user:password@host/db`). Neither struct implements `slog.LogValuer`. When the config struct is logged — which happens implicitly through `slog.Info("config loaded", "cfg", cfg)` — database credentials appear in plaintext in the log output.

**DB Reference:** NIST SP 800-57 Pt 1 — "Credentials must not appear in logs or audit trails." OWASP Go-SCP — Sensitive Data Protection — "Implement LogValuer to redact sensitive fields before logging."

**Attack Scenario:**
1. Operator configures `MUXCORE_DATABASE_URL=postgres://admin:s3cret@db.internal/db`
2. Config struct is logged during startup or debugging
3. Log files containing the plaintext password are committed, backed up, or shipped to a log aggregator
4. Any developer or operator with log access can extract the database password

**Remediation:**
```go
func (d DatabaseConfig) LogValue() slog.Value {
    return slog.GroupValue(
        slog.String("driver", d.Driver),
        slog.String("url", redactURL(d.URL)),
    )
}

func redactURL(raw string) string {
    if raw == "" { return "" }
    u, _ := url.Parse(raw)
    if u != nil && u.User != nil {
        u.User = url.User("***")
    }
    return u.String()
}
```

---

### F08: Join Token Comparison Not Constant-Time
**Severity:** High | **CWE-208** | **CVSS:** 5.9 (AV:N/AC:H/PR:N/UI:N/S:U/C:H/I:N/A:N)

**Location:** `internal/grpcmesh/discovery.go:115` (`Join`)

**Description:** The cluster join token is compared using the `!=` operator (`token != s.joinToken`), which uses Go's standard string comparison. This comparison returns at the first differing byte — an attacker can measure response times to determine the join token character by character, bypassing the brute-force rate limiter.

**DB Reference:** OWASP Go-SCP — Cryptographic Practices — "Use `crypto/subtle.ConstantTimeCompare` for all security-sensitive string comparisons." NIST SP 800-57 Pt 1 — "Constant-time comparison prevents timing side channels."

**Attack Scenario:**
1. Attacker connects to gRPC discovery endpoint
2. Attacker sends join requests with candidate tokens: `a...`, `b...`, `c...`
3. Attacker measures response time for each — the correct first character takes ~1μs longer due to an extra byte comparison
4. After ~256 attempts per character × 32 characters = ~8,192 attempts, attacker recovers the full token
5. Brute-force rate limiter (3 per 20s) is bypassed because this requires only 1 attempt per character position

**Remediation:**
```go
import "crypto/subtle"
if subtle.ConstantTimeCompare([]byte(token), []byte(s.joinToken)) != 1 {
    return nil, status.Error(codes.PermissionDenied, "invalid join token")
}
```

---

### F09: Audit Logger — Hash Chain and HMAC Declared But Never Populated
**Severity:** High | **CWE-345/778** | **CVSS:** 6.5 (AV:N/AC:L/PR:L/UI:N/S:U/C:N/I:H/A:N)

**Location:** `pkg/contracts/audit.go:20-27` (declaration), `internal/audit/audit.go:44-64` (implementation)

**Description:** `AuditEntry` declares `PrevEntryHash` (SHA-256 hash chain) and `Signature` (HMAC-SHA256) fields with clear documentation about tamper-evident integrity. However, `FileLogger.Log()` never computes or populates either field. Entries are written as plain JSON without cryptographic linkage. An attacker with write access to the audit file can insert, delete, or modify entries undetectably.

**DB Reference:** NIST SP 800-53 AU-9 — "Protect audit information and audit tools from unauthorized access, modification, and deletion." NIST SP 800-53 AU-16 — "Cross-Organizational Audit Logging."

**Attack Scenario:**
1. Attacker gains filesystem access to the audit log file
2. Attacker deletes all entries containing `"action":"auth.failure"` for their IP
3. Attacker inserts fake audit entries with `"action":"module.unregistered"` for a target module
4. `Query()` returns the tampered log — no integrity check fails
5. Incident response team sees clean audit trail, attacker's activity goes undetected

**Remediation:**
```go
func (fl *FileLogger) Log(ctx context.Context, entry contracts.AuditEntry) error {
    fl.mu.Lock()
    defer fl.mu.Unlock()
    
    // Compute hash chain
    entry.PrevEntryHash = fl.prevHash
    data, _ := json.Marshal(entry)
    fl.prevHash = sha256Hex(data)
    
    // Compute HMAC signature (when signing key is configured)
    if fl.signingKey != nil {
        entry.Signature = hmacHex(fl.signingKey, data)
    }
    
    data = append(data, '\n')
    // ... write ...
}
```

---

### F10: writeJSON Drops json.Encoder Error
**Severity:** Medium | **CWE-252** | **CVSS:** 5.3 (AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:L/A:L)

**Location:** `internal/api/server.go:226-230` (`writeJSON`)

**Description:** `json.NewEncoder(w).Encode(v)` is called without capturing its return value. If the JSON encoding fails (e.g., circular reference in the data structure, channel type in map), the error is silently dropped. The response has `Content-Type: application/json` and a 200/2xx status code, but the body is incomplete or empty. This creates a silent data integrity failure.

**DB Reference:** OWASP Go-SCP — Error Handling — "Always check and handle errors from I/O operations and encoders."

**Remediation:**
```go
func writeJSON(w http.ResponseWriter, status int, v any) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(status)
    if err := json.NewEncoder(w).Encode(v); err != nil {
        slog.Error("writeJSON: encode failed", "error", err)
    }
}
```

---

### F11: SidecarProxy.Health Always Returns Healthy
**Severity:** Medium | **CWE-754** | **CVSS:** 5.3 (AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:L/A:L)

**Location:** `internal/module/mgr/proxy.go:26` (`SidecarProxy.Health`)

**Description:** `SidecarProxy.Health()` returns `nil` unconditionally. The sidecar process could have crashed, been SIGKILL'd, or be stuck in a deadlock — health always reports OK. The health checker in `handleHealth` (`server.go:239-241`) is also set to `return nil`, returning "healthy" for all modules regardless of actual state.

**DB Reference:** OWASP Go-SCP — Error Handling — "Check for unusual or exceptional conditions." CWE-754: Improper Check for Unusual or Exceptional Conditions.

**Remediation:**
Wire the sidecar process state into the proxy:
```go
type SidecarProxy struct {
    info    contracts.ModuleInfo
    process *os.Process  // set after Spawn()
}

func (p *SidecarProxy) Health(ctx context.Context) error {
    if p.process == nil {
        return nil // hasn't started yet
    }
    // Check if process is still alive (Unix: signal 0)
    if err := p.process.Signal(syscall.Signal(0)); err != nil {
        return fmt.Errorf("sidecar process not running: %w", err)
    }
    return nil
}
```

---

### F12: Seed-Node Auto-Join Passes nil Creds to Dial
**Severity:** Medium | **CWE-319** | **CVSS:** 6.5 (AV:N/AC:L/PR:N/UI:N/S:U/C:L/I:L/A:L)

**Location:** `cmd/muxcored/main.go:272-273`

**Description:** The auto-join loop constructs `grpc.WithTransportCredentials(creds)` where `creds` may be `nil` (when TLS is disabled). Go's gRPC library treats `nil` transport credentials as "use insecure" rather than rejecting the connection. This means auto-join connections to seed nodes transmit the join request, including the join token, in cleartext.

**DB Reference:** Bulletproof TLS — "Never use nil or insecure credentials in production."

**Remediation:**
```go
if creds == nil {
    slog.Warn("auto-join skipped: TLS is required for cluster join")
} else {
    for _, seed := range cfg.GRPC.SeedNodes {
        go func(seedAddr string) {
            dialOpts := []grpc.DialOption{grpc.WithTransportCredentials(creds)}
            // ... join ...
        }(seed)
    }
}
```

---

### F13: Spool Fetcher — No Response Size Limit
**Severity:** Medium | **CWE-770** | **CVSS:** 5.9 (AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:H)

**Location:** `internal/spool/fetcher.go:51` (`FetchTag`)

**Description:** `io.ReadAll(resp.Body)` reads the entire response body into memory with no size limit. A malicious spool endpoint or compromised GitHub raw URL can return an arbitrarily large JSON response, exhausting process memory.

**DB Reference:** OWASP Go-SCP — Input Validation — "Limit input size using io.LimitReader."

**Remediation:**
```go
limitedReader := io.LimitReader(resp.Body, 1<<20) // 1 MB
body, err := io.ReadAll(limitedReader)
```

---

### F14: No gRPC IdleTimeout
**Severity:** Medium | **CWE-400** | **CVSS:** 5.3 (AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:L)

**Location:** `cmd/muxcored/main.go:127` (`grpc.NewServer`)
**Description:** The gRPC server is created with `grpc.NewServer(grpcOpts...)` without keepalive enforcement parameters (`grpc.KeepaliveParams`, `grpc.KeepaliveEnforcementPolicy`). Long-idle gRPC connections consume goroutines and memory indefinitely.

**Remediation:**
```go
grpcSrv := grpc.NewServer(grpcOpts...,
    grpc.KeepaliveParams(keepalive.ServerParameters{
        MaxConnectionIdle: 5 * time.Minute,
        Timeout:           20 * time.Second,
    }),
    grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
        MinTime:             30 * time.Second,
        PermitWithoutStream: true,
    }),
)
```

---

### F15: X-Trace-Id Header Accepts CRLF Characters
**Severity:** Low | **CWE-93** | **CVSS:** 3.7

**Location:** `internal/trace/trace.go:32-42` (`HTTPMiddleware`)

**Description:** The trace middleware validates the incoming `X-Trace-Id` header against a hex character set and length, but does not block CRLF (`\r\n`) characters. If the trace ID is ever reflected unsanitized into log output headers, it could be used for header injection.

**DB Reference:** OWASP ASVS V14.5.1 — "Verify that the application validates all HTTP request headers."

**Remediation:**
Add CRLF check:
```go
func isValidTraceID(s string) bool {
    if strings.ContainsAny(s, "\r\n") {
        return false
    }
    // ... existing validation ...
}
```

---

### F16: TLS Certificates Not Validated at Config Load Time
**Severity:** Low | **CWE-295** | **CVSS:** 2.3

**Location:** `internal/config/config.go:175-178` (`validate`)

**Description:** Config validation checks timeouts and log levels, but does not verify that TLS certificate files exist and are valid PEM at startup. The error surfaces only when the first request arrives, rather than at boot.

**Remediation:**
Add TLS cert validation to `validate()`:
```go
if c.Server.CertFile != "" && c.Server.KeyFile != "" {
    if _, err := tls.LoadX509KeyPair(c.Server.CertFile, c.Server.KeyFile); err != nil {
        errs = append(errs, fmt.Sprintf("server TLS cert/key invalid: %v", err))
    }
}
```

---

### F17: Health Endpoint Responses Lack CSP
**Severity:** Low | **Description:** HX-Request responses from `/health` send HTML fragments without CSP headers. While low risk (status indicator HTML), consistency requires CSP on all responses.

### F18: Module Binary Cache Path Derives from Unvalidated Repo URL
**Severity:** Low | **CWE-22** | **CVSS:** 2.0

**Location:** `internal/module/mgr/manager.go:60-66` (`Resolve`, `moduleIDFromRepo`)

**Description:** `moduleIDFromRepo(repoURL)` extracts the last path segment from the repo URL and uses it as a directory name in the cache path. If the repo URL contains path traversal sequences (e.g., `../`), they could escape the cache directory. While `git clone` would likely fail on such URLs, the path construction is fragile.

**Remediation:**
```go
func moduleIDFromRepo(repoURL string) string {
    u, err := url.Parse(repoURL)
    if err != nil {
        return ""
    }
    parts := strings.Split(strings.Trim(u.Path, "/"), "/")
    if len(parts) == 0 {
        return ""
    }
    name := strings.TrimSuffix(parts[len(parts)-1], ".git")
    // Sanitize: remove path separators and dot segments
    name = filepath.Base(name)
    return name
}
```

---

## Threat Modeling

### Trust Boundaries

```
┌──────────────┐     ┌─────────────┐     ┌──────────────┐
│  External     │────▶│  HTTP API   │────▶│  Module Mesh │
│  (browser)    │     │  (:8080)    │     │  (gRPC :9090)│
└──────────────┘     └─────────────┘     └──────────────┘
       │                     │                     │
       ▼                     ▼                     ▼
┌──────────────────────────────────────────────────────┐
│                  MuxCore Process                     │
│  ┌──────────┐  ┌──────────┐  ┌──────────────────┐   │
│  │  Spool   │  │  Module  │  │  Discovery/      │   │
│  │  Fetcher │  │  Manager │  │  Heartbeat       │   │
│  └──────────┘  └──────────┘  └──────────────────┘   │
└──────────────────────────────────────────────────────┘
       │                     │
       ▼                     ▼
┌──────────────┐     ┌──────────────┐
│  GitHub.com  │     │  Sidecar     │
│  (raw)       │     │  Modules     │
└──────────────┘     └──────────────┘
```

**Key trust boundaries:**
1. External → HTTP API (TLS optional, body size unlimited)
2. Spool Fetcher → Internet (SSRF risk, no integrity verification)
3. Module Manager → Internet (git clone without integrity check)
4. Sidecar Module → gRPC Mesh (no auth enforcement)
5. Node → Node (heartbeat can be cleartext)

### Attack Chain 1: Spool Supply Chain Compromise

| Step | Technique | MITRE ATT&CK | Finding |
|------|-----------|-------------|---------|
| 1. Attacker compromises GitHub token or gains push access to module repo | T1195.001 (Supply Chain Compromise: Compromise Software Dependencies and Development Tools) | F02 |
| 2. Attacker pushes tagged version with backdoored `init()` function | T1554 (Compromise Client Software Binary) | F02 |
| 3. Operator runs `muxcored --tag default` | Normal operation | — |
| 4. Spool fetcher fetches tag JSON via HTTPS → resolves to compromised version | SSRF potential if spool URL is attacker-controlled | F01 |
| 5. Module manager clones + builds without checksum verification | No integrity check | F02 |
| 6. Backdoored module registers via gRPC, calls `Mesh.Call()` to exfiltrate data | gRPC interceptor has no auth enforcement | F05 |
| 7. Audit trail shows modular registration but no hash chain integrity — attacker deletes evidence | Audit integrity not enforced | F09 |

**Prevention:** Fix F01, F02, F05, F09 — chain breaks at step 4 (spool SSRF), step 5 (integrity check), step 6 (auth enforcement), and step 7 (tamper-evident audit).

### Attack Chain 2: Internal Network Enumeration via SSRF

| Step | Technique | MITRE ATT&CK | Finding |
|------|-----------|-------------|---------|
| 1. Attacker finds exposed MuxCore deployment with accessible CLI or API | T1190 (Exploit Public-Facing Application) | — |
| 2. Attacker sets `--spool http://169.254.169.254/latest/meta-data/` | SSRF to cloud metadata | F01 |
| 3. Fetcher reads AWS/GCP/Azure instance metadata — access keys, tokens | T1552.005 (Unsecured Credentials: Cloud Instance Metadata API) | F01 |
| 4. Attacker uses cloud credentials to access cloud resources | T1078.004 (Valid Accounts: Cloud Accounts) | F01 |
| 5. Attacker enumerates internal network by iterating `--spool http://10.0.0.x/tags/` | T1046 (Network Service Scanning) | F01 |

**Prevention:** Fix F01 (SSRF hardening) — scheme allowlist + private IP blocking breaks the chain entirely.

### Attack Chain 3: Cluster Node Impersonation

| Step | Technique | MITRE ATT&CK | Finding |
|------|-----------|-------------|---------|
| 1. Attacker captures cleartext heartbeat on shared network (TLS disabled) | T1040 (Network Sniffing) | F06 |
| 2. Attacker extracts node IDs, module lists from heartbeat | T1040 | F06 |
| 3. Attacker performs timing attack to recover join token | T1592.004 (Gather Victim Network Information) | F08 |
| 4. Attacker sends crafted Join request with recovered token | T1557 (Man-in-the-Middle) | F08 |
| 5. Attacker's rogue node registers malicious module | T1554 (Compromise Client Software Binary) | F05 |
| 6. Attacker's module uses gRPC mesh to call admin endpoints | No auth enforcement | F05 |

**Prevention:** Fix F06 (no cleartext heartbeat), F08 (constant-time comparison), F05 (gRPC auth enforcement) — chain breaks at step 1, 3, and 5.

---

## Compliance Mapping

### OWASP ASVS v4.0.3

| Requirement | Status | Finding |
|-------------|--------|---------|
| V1.4.1 — Trusted enforcement point for access control | ⚠️ | F05 — gRPC interceptor doesn't enforce |
| V2.1.1 — Authentication enforced on trusted service | ⚠️ | F05 — gRPC mesh has no auth |
| V2.10.2 — Constant-time secret comparison | ❌ | F08 — join token uses `!=` |
| V4.1.1 — Access control on trusted service layer | ⚠️ | F05 |
| V7.1.1 — Log injection prevention | ⚠️ | F15 — trace header allows CRLF |
| V7.2.1 — Audit log integrity protection | ❌ | F09 — hash chain not populated |
| V8.3.6 — Sensitive data not logged | ❌ | F07 — DB URLs with credentials logged |
| V9.2.1 — TLS for all communications | ⚠️ | F06 — heartbeat can be cleartext |
| V11.1.2 — Input size limits | ❌ | F03 — no body size limit |
| V14.4.1 — CSP with appropriate directives | ✅ | Default CSP is correct |
| V14.4.3 — HSTS header on HTTPS only | ❌ | F04 — HSTS emitted unconditionally |
| V14.5.3 — SSRF protection for URL fetches | ❌ | F01 — no SSRF protection |

### NIST SP 800-53

| Control | Status | Finding |
|---------|--------|---------|
| AC-3 Access Enforcement | ⚠️ | F05 — gRPC interceptor |
| AU-9 Audit Protection | ❌ | F09 — hash chain integrity |
| IA-5 Authenticator Management | ⚠️ | F08 — non-constant-time token comparison |
| SC-8 Transmission Confidentiality | ⚠️ | F06 — cleartext heartbeat |
| SC-23 Session Authenticity | ⚠️ | F02 — no module binary verification |
| SI-7 Software Integrity | ❌ | F02 — no checksum verification |

---

## Secure Coding Recommendations

Based on OWASP Go-SCP and NIST guidance:

1. **Add input size limits everywhere** — `http.MaxBytesReader` for HTTP, `io.LimitReader` for fetches, `max_recv_msg_size` for gRPC
2. **Implement constant-time comparison** for all security-sensitive string comparisons — joins tokens, API keys, HMACs
3. **Make TLS mandatory, never optional** — remove insecure fallback code paths, don't start services that require TLS when it's not available
4. **Add binary integrity verification** — checksum in spool, verify after build, reject on mismatch
5. **Implement `slog.LogValuer`** on all config types that carry credentials — DatabaseConfig, CacheConfig, any URL with embedded userinfo
6. **Wire Authorizer into gRPC interceptor** — identity extraction without enforcement is dead code
7. **Populate audit hash chain** — compute PrevEntryHash in Log(), add HMAC when signing key is configured
8. **Add SSRF protection** — scheme allowlist, private IP blocking, domain allowlist, size limits
9. **Wire module health checks** — proxy.Health() should check process liveness
10. **Add gRPC keepalive enforcement** — MaxConnectionIdle, timeout, enforcement policy
11. **Conditionalize HSTS** — only emit when TLS is actually enabled
12. **Validate TLS certs at boot** — fail fast on misconfiguration
