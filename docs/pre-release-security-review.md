# Pre-Release Security Review — v1.0.0-rc.1

**Date:** June 10, 2026
**Scope:** All findings from SECURITY-AUDIT-2026-05-27.md

---

## Closure Summary

| Severity | Total | Remediated | Remaining |
|----------|-------|------------|-----------|
| Critical | 3 | 3 | 0 |
| High | 6 | 6 | 0 |
| Medium | 5 | 5 | 0 |
| Low | 4 | 4 | 0 |
| **Total** | **18** | **18** | **0** |

All 18 findings from the May 27 audit are fully remediated. Details below.

---

## Finding Closure Table

| ID | Description | Severity | Status | Remediation |
|----|-------------|----------|--------|-------------|
| F01 | SSRF — spool fetcher | Critical | ✅ Closed | Scheme restricted to HTTPS; private IPs / loopback / link-local blocked via DNS resolution; `io.LimitReader` (1 MB); host allow-list via `MUXCORE_SPOOL_ALLOWED_HOSTS` |
| F02 | Module binary integrity | Critical | ✅ Closed | Checksum verification in spool format; binary SHA-256 checked after build; rejection on mismatch |
| F03 | No request body limit | Critical | ✅ Closed | `maxBodyMiddleware` with 10 MB `MaxBytesReader`, outermost in middleware chain |
| F04 | HSTS over plaintext | High | ✅ Closed | HSTS header conditional on `tlsActive` flag; omitted in insecure mode |
| F05 | gRPC auth no enforcement | High | ✅ Closed | `AuthUnaryInterceptor` and `AuthStreamInterceptor` both call `extractAndVerify()` which enforces via `Authorizer.Can()` |
| F06 | Insecure heartbeat fallback | High | ✅ Closed | Heartbeat loop requires TLS; skipped when credentials are nil |
| F07 | DB URL in logs | High | ✅ Closed | `DatabaseConfig.LogValue()` and `CacheConfig.LogValue()` redact credentials via `redactURL()` |
| F08 | Non-constant-time join token | High | ✅ Closed | `subtle.ConstantTimeCompare` for join token verification |
| F09 | Audit hash chain not populated | High | ✅ Closed | `FileLogger.Log()` computes SHA-256 of previous entry, sets `PrevEntryHash` |
| F10 | writeJSON error dropped | Medium | ✅ Closed | `json.NewEncoder(w).Encode(v)` error logged via `slog.Error` |
| F11 | SidecarProxy always healthy | Medium | ✅ Closed | `Health()` returns error when `exitErr != nil` (process crashed) |
| F12 | Seed auto-join nil creds | Medium | ✅ Closed | Auto-join guarded by TLS credentials check |
| F13 | No response size limit | Medium | ✅ Closed | `io.LimitReader(resp.Body, 1<<20)` in spool fetcher |
| F14 | No gRPC keepalive | Medium | ✅ Closed | `KeepaliveParams` (5m idle, 2m ping, 20s timeout) and `KeepaliveEnforcementPolicy` (30s min) set on gRPC server |
| F15 | CRLF in trace ID | Low | ✅ Closed | `isValidTraceID` rejects strings containing `\r` or `\n` |
| F16 | TLS certs not validated at boot | Low | ✅ Closed | `tls.LoadX509KeyPair` called in `validate()` on startup |
| F17 | Health endpoint lacks CSP | Low | ✅ Closed | Health endpoint uses same middleware chain; CSP applied to all responses |
| F18 | Module cache path traversal | Low | ✅ Closed | `moduleIDFromRepo` sanitizes path via `filepath.Base` and URL parsing |

## Verdict

**All findings resolved. No open security issues at time of v1.0.0-rc.1.**

The initial audit identified 3 critical, 6 high, 5 medium, and 4 low findings.
Over the following 14 days, all 18 findings were remediated through
PRs #58, #59, #60, #62, #69, and the final F01 private-IP gap closure on June 10.

Security posture is now:
- TLS required for all production communication
- Deny-by-default for inter-module calls and event publishes
- RBAC enforced on all HTTP and gRPC endpoints
- Credentials redacted from logs
- Tamper-evident audit log with SHA-256 hash chain
- Input size limits at all entry points
- SSRF protection with scheme + host + private-IP validation
- Constant-time comparison for all security-sensitive tokens
- gRPC keepalive enforcement
- TLS certificate validation at boot
