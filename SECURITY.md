# Security policy

## Reporting a vulnerability

**Please do not file a public GitHub issue for security vulnerabilities.**

Use one of the following private channels instead:

1. **GitHub private vulnerability reporting** *(preferred)* — open the [Security tab](https://github.com/gnana997/periscope/security) on the repo and click *"Report a vulnerability"*. This creates a private thread with the maintainers.
2. **Email** — `gnana097@gmail.com` with subject `[periscope security]`. PGP key available on request.

Please include:

- A clear description of the issue and its impact.
- Steps to reproduce, or a proof-of-concept if you have one.
- The version / commit you found it on (`git rev-parse HEAD` if you're on `main`).
- Whether the issue is already public anywhere.

## What to expect

Periscope is in early development and maintained by a small team. We aim to:

| Step | Target turnaround |
|---|---|
| Acknowledge receipt of your report | Within **3 business days** |
| Initial assessment + severity triage | Within **7 business days** |
| Fix + coordinated disclosure | Best effort; depends on severity and complexity |

We'll keep you updated and credit you in the release notes if you'd like.

## Scope

Issues we consider in scope:

- Authentication / authorization bypass on the Periscope backend.
- Privilege escalation between users with different IdP-group tiers.
- Leakage of cluster credentials (kubeconfig, AWS credentials, OIDC tokens) outside the running pod.
- Code execution from a malicious cluster, kubeconfig, or YAML payload.
- Audit-log tampering or omission for in-scope sensitive operations.
- Vulnerabilities in dependencies that materially affect the above.

Issues that are typically **out of scope**:

- Misconfiguration that requires the operator to deliberately weaken Periscope's defaults (e.g. disabling auth).
- Vulnerabilities in third-party services Periscope integrates with (Auth0, Okta, AWS) — please report those upstream.
- Findings from automated scanners without a demonstrated impact.
- Denial-of-service via resource exhaustion against a single user's session.

## Supported versions

| Version | Status |
|---|---|
| `v1.x` (latest minor on `main`) | **Active** — security and bug fixes |
| `v1.0.x` | **Maintenance** — security fixes only, until `v1.2` ships |
| `< v1.0` | Unsupported |

Periscope follows [semantic versioning](https://semver.org/spec/v2.0.0.html)
for the public HTTP API, OIDC / cluster-registry config shape, and Helm
chart values. The audit-log schema is wire-stable per RFC 0003. Breaking
changes land in a future major (`v2`).

## Supply-chain verification

Every release ships:

- **Cosign keyless signatures** on the container image and Helm chart,
  backed by Sigstore's public transparency log. Verify before deploying:

  \`\`\`sh
  cosign verify ghcr.io/gnana997/periscope:vX.Y.Z \
    --certificate-identity-regexp "https://github.com/gnana997/periscope" \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com
  \`\`\`

  Same pattern verifies the Helm chart at
  \`oci://ghcr.io/gnana997/charts/periscope\`.

- **SPDX SBOM** attached to the image (downloadable via \`cosign download
  sbom\` or the GitHub release page). Operators wanting to enumerate
  Periscope's dependency graph for their own scanners get it directly
  from the artifact rather than re-deriving from source.

- **Distroless runtime, non-root, read-only filesystem, all capabilities
  dropped, RuntimeDefault seccomp** (Helm chart defaults). The runtime
  attack surface is the Go binary plus a CA bundle — nothing else.

## Disclosure policy

We follow [coordinated disclosure](https://en.wikipedia.org/wiki/Coordinated_vulnerability_disclosure):

1. You report privately.
2. We confirm and work on a fix.
3. We agree on a public disclosure date with you (typically once a fix is shipped, or 90 days from your initial report — whichever is sooner).
4. The fix lands on `main` and is announced in the release notes / GitHub Security Advisory.

## Compliance posture (non-vulnerability)

For non-vulnerability questions about Periscope's compliance posture (audit-event coverage, secret handling, container hardening), please open a regular [GitHub Discussion](https://github.com/gnana997/periscope/discussions) — those don't need to be private.
