# Security policy

## Reporting a vulnerability

Please don't open a public issue. Report privately through GitHub's [private vulnerability reporting](https://github.com/notsliver/Ignition/security/advisories/new) with steps to reproduce and the affected version.

You'll get an acknowledgement within a few days. Fixes ship as soon as possible, and reporters are credited unless they'd rather not be.

## Supported versions

During the beta, only the latest release receives security fixes.

## Scope

IGNITION controls the Docker Engine on its host, so any account with the developer role or above is effectively trusted with root on that machine. That's by design and not a vulnerability. Ways to gain access you shouldn't have (authentication or authorization bypass, CSRF, secret disclosure, escaping the role model, SSRF through diagnostics) are in scope.

The trust model and existing controls are described in [docs/security.md](docs/security.md).
