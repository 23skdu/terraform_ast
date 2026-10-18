# Security Policy

## Supported Versions

| Version | Supported          |
| :------ | :----------------- |
| 0.9.x   | Yes                |
| < 0.9.0 | No                 |

## Reporting a Vulnerability

If you discover a security vulnerability in `terraform_ast`, please report it privately rather than opening a public issue.

Please report vulnerabilities via GitHub Security Advisories or by emailing the project maintainers directly. Include:
- A description of the vulnerability and affected component.
- Steps to reproduce or a minimal proof of concept.
- Potential impact on consumers or CI pipelines.

Maintainers will respond within 48 hours to confirm receipt and coordinate remediation.

## Supply Chain Security

`terraform_ast` releases include:
- Hermetic SLSA Level 3 build provenance.
- Sigstore Cosign signatures.
- CycloneDX and SPDX Software Bill of Materials (SBOMs).
- Trivy vulnerability scans for container images.
