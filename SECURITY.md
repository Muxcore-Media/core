# Security Policy

## Supported Versions

MuxCore is pre-1.0. Security patches are applied to `main` and backported only as
needed. Once 1.0 ships, a formal version support matrix will be published.

| Version | Supported          |
| ------- | ------------------ |
| main    | :white_check_mark: |
| < 1.0   | :x:                |

## Reporting a Vulnerability

**Do not open a public issue.** Disclose security vulnerabilities privately via
GitHub's built-in vulnerability reporting:

1. Go to the [Security Advisories](https://github.com/Muxcore-Media/core/security/advisories) tab
2. Click **New draft security advisory**
3. Fill in the affected versions, severity, and a clear description with reproduction steps

You will receive an acknowledgement within **72 hours**. After triage, we will
keep you informed of progress and coordinate a disclosure timeline. We aim to
release a patch within **7 days** for critical issues and **30 days** for
moderate ones — but complex issues may take longer. If the vulnerability is
declined, we will explain why.

### What to Include

- Affected component (core fabric, event bus, module contracts, API server, a specific module)
- Steps to reproduce, ideally as a minimal Go test case or `curl` one-liner
- Impact (data exposure, privilege escalation, denial of service, remote code execution)
- Whether you plan to disclose publicly and your preferred timeline

## Scope

### In Scope

- **Core fabric** — event bus, module registry, lifecycle manager, scheduler, API server (`/health`), config system
- **Module contracts** — any interface in `pkg/contracts/` that could enable privilege escalation between modules
- **Authentication / authorization** — OIDC flow, RBAC enforcement, capability checks
- **Module sandboxing** — gVisor boundary escapes (when implemented)
- **gRPC mesh** — mTLS bypass, message injection (when implemented)
- **Default preset modules** — `admin-ui`, `api-rest`, `scheduler-cron` (when built with `-tags default`)
- **Docker image** — container escapes, misconfigurations in the published image

### Out of Scope

- Issues in third-party modules not published under the `Muxcore-Media` GitHub organization
- Issues in infrastructure not controlled by MuxCore (user's reverse proxy, firewall, host OS)
- DoS via resource exhaustion on an unauthenticated endpoint (these are tracked as availability improvements, not security advisories)
- Social engineering, phishing, or physical attacks
- Vulnerabilities in dependencies that have no patch available upstream

## Disclosure Policy

1. Reporter submits a private report
2. MuxCore maintainers triage within 72 hours and assign a severity
3. A fix is developed in a private fork; the reporter is credited (with permission) in the advisory
4. A GitHub Security Advisory is published in coordination with the fix release
5. A CVE may be requested for critical vulnerabilities

We follow **coordinated disclosure**. Please give us a reasonable window to patch
before going public. We consider 30 days reasonable by default, and will
negotiate a shorter window for actively-exploited issues.

## Security Model

MuxCore's security boundary is **between modules, not inside them**. Core provides
the fabric — event bus, registry, lifecycle — and each module runs with the
capabilities it declared. A module that declares only `downloader.torrent` should
not be able to read the filesystem or call the notification system. The attack
surface that matters:

- **Module → Core**: can a module escape its declared capabilities?
- **Module → Module**: can a module intercept or forge events bound for another?
- **Network → API**: can an unauthenticated caller reach an admin endpoint?
- **Event bus**: can an event payload trigger unexpected behavior in a listener?

When reporting, frame issues against these boundaries. A bug in a module's
business logic that stays within its own capabilities is a regular bug, not a
security vulnerability.

## Safe Harbor

We will not pursue legal action or file takedown requests against security
researchers who:

- Test against their own MuxCore instance or an instance they have explicit permission to test
- Avoid accessing or modifying data that does not belong to them
- Make a good-faith effort to avoid degradation of service during testing
- Follow this policy's reporting and disclosure process

## Recognition

Researchers who report valid vulnerabilities will be credited in the advisory
and listed here (with permission).

| Name | Issue | Date |
| ---- | ----- | ---- |
| —    | —     | —    |
