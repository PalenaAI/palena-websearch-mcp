# Security Policy

Palena is an enterprise-grade web search MCP server used in regulated
environments. We take security reports seriously and aim to respond quickly.

## Supported Versions

Only the latest minor release line of Palena is supported with security fixes.
Older versions are best-effort and may not receive patches.

| Version | Supported |
| ------- | --------- |
| latest  | yes       |
| older   | no        |

## Reporting a Vulnerability

**Please do not open a public GitHub issue for security vulnerabilities.**

Preferred reporting channels (in order):

1. **GitHub Security Advisories** — use the "Report a vulnerability" button
   under the repository's Security tab. This creates a private discussion
   with the maintainers.
2. **Email** — `security@bitkaio.com` with the subject prefix
   `[palena security]`. PGP-encrypted reports are welcome; request our public
   key in the first email if you need one.

Include, where possible:

- A description of the vulnerability and its potential impact
- Steps to reproduce, ideally with a minimal proof-of-concept
- The affected version, commit SHA, or container image digest
- Any suggested remediation

## Response SLA

We aim to meet the following targets, measured from acknowledged receipt of a
report:

| Severity   | Acknowledge | Fix or mitigation released |
| ---------- | ----------- | -------------------------- |
| Critical   | 48 hours    | 7 days                     |
| High       | 72 hours    | 30 days                    |
| Medium     | 7 days      | 90 days                    |
| Low / info | 14 days     | next regular release       |

## Coordinated Disclosure

Palena follows coordinated vulnerability disclosure. We ask reporters to allow
us a reasonable window to remediate before public disclosure, and we will
credit reporters in the release notes unless anonymity is requested.

## Supply-Chain Integrity

Every tagged release publishes:

- A signed container image (Sigstore keyless via GitHub OIDC)
- CycloneDX and SPDX SBOMs attached to the release
- A Trivy HIGH/CRITICAL vulnerability report
- SLSA Level 3 build provenance

Verify the image signature with:

```sh
cosign verify \
  --certificate-identity-regexp 'https://github.com/PalenaAI/palena-websearch-mcp/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  ghcr.io/palenaai/palena-websearch-mcp@<digest>
```

## Scope

In scope:

- The Palena Go binary and its first-party sidecars (`deploy/`)
- The released container images under `ghcr.io/palenaai/palena-websearch-mcp`
- Configuration handling, domain policy enforcement, PII redaction, and
  prompt-injection screening logic

Out of scope:

- Vulnerabilities in upstream third-party services (SearXNG, Presidio,
  Playwright, HuggingFace TEI) unless they are exploitable through Palena's
  default configuration
- Issues requiring physical access, modification of the host OS, or stolen
  administrative credentials
- Denial-of-service via resource exhaustion against a single deployment
