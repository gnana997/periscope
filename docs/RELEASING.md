# Releasing Periscope

How tags become published artifacts. The `release.yaml` GitHub
Actions workflow does the heavy lifting; this document covers the
human side of the loop.

## TL;DR

```sh
git checkout main && git pull
git tag -a v0.5.0 -m "v0.5.0"
git push origin v0.5.0
```

That's it. The workflow at `.github/workflows/release.yaml` triggers
on the tag push, builds + signs + publishes everything, opens a
GitHub Release, and you're done.

## What gets published

Every tag (`v*`) triggers the workflow, which:

1. Builds a multi-arch container image (`linux/amd64`, `linux/arm64`)
2. Pushes it to `ghcr.io/gnana997/periscope` with semver tags
3. Signs the image keylessly via cosign (Sigstore)
4. Generates an SPDX SBOM and attaches it as an OCI artifact
5. Packages the Helm chart at `deploy/helm/periscope/`
6. Pushes the chart to `ghcr.io/gnana997/charts/periscope` as an
   OCI artifact
7. Signs the chart keylessly via cosign
8. Opens a GitHub Release with auto-generated notes from PR titles
   since the previous tag, attaching the SBOM and chart `.tgz`

## Tagging convention

- Stable: `vX.Y.Z` (e.g. `v1.0.0`, `v1.2.3`)
- Pre-release: `vX.Y.Z-rcN` (e.g. `v1.0.0-rc1`)
- Both must be valid semver — Helm rejects non-semver tags

Stable tags get the `latest`, `vX`, `vX.Y` aliases on the container
image. Pre-releases only get the exact tag. The GitHub Release is
also marked as a pre-release when the tag contains `-`.

## Pre-release checklist

Before tagging:

- [ ] CI green on `main`
- [ ] Helm `Chart.yaml`'s `artifacthub.io/changes` annotation
      reflects the PRs in this release (the workflow does NOT
      auto-update this — it stays as a curated changelog)
- [ ] If the API or config shape changed, `docs/setup/deploy.md`
      and the relevant guide are updated
- [ ] If a new env var was added, it's documented in `values.yaml`
      with a comment block
- [ ] `README.md` features list still matches what ships
- [ ] `cmd/periscope/main.go`'s `version` and `commit` ldflags
      defaults are sane (the workflow overrides them with the tag
      and SHA, but the dev-mode default is what `make build` uses)

## Cutting a release

```sh
# 1. Ensure local main is up to date
git checkout main
git pull origin main

# 2. Tag with an annotated message (required — the message becomes
#    the GitHub Release title context)
git tag -a v0.5.0 -m "v0.5.0"

# 3. Push the tag — this is what triggers the workflow
git push origin v0.5.0
```

Watch the workflow run at:
`https://github.com/gnana997/periscope/actions/workflows/release.yaml`

Total runtime is ~6-10 minutes depending on cache state.

## Verifying signatures

Anyone (including operators evaluating Periscope for production
use) can verify the published artifacts:

```sh
# Verify the container image signature
cosign verify \
  ghcr.io/gnana997/periscope:v1.0.0 \
  --certificate-identity-regexp="https://github.com/gnana997/periscope" \
  --certificate-oidc-issuer=https://token.actions.githubusercontent.com

# Verify the Helm chart signature
cosign verify \
  ghcr.io/gnana997/charts/periscope:1.0.0 \
  --certificate-identity-regexp="https://github.com/gnana997/periscope" \
  --certificate-oidc-issuer=https://token.actions.githubusercontent.com

# Pull the SBOM
cosign download sbom \
  ghcr.io/gnana997/periscope:v1.0.0 > sbom.spdx.json
```

## Artifact Hub

The chart appears at
[artifacthub.io/packages/helm/periscope/periscope](https://artifacthub.io/packages/helm/periscope/periscope)
once registered (one-time setup — see
`deploy/helm/periscope/artifacthub-repo.yml` for the metadata
push command). Artifact Hub polls the OCI registry every ~30
minutes and re-renders the chart's listing page.

## Rollback

If a release goes wrong:

1. **Don't delete the tag.** Pushing a moved tag is destructive
   and confuses anyone who already pulled.
2. **Cut a new patch release** that reverts the bad change
   (`v1.0.0` bad → `v1.0.1` with the revert).
3. **Mark the GitHub Release as "broken"** with a prominent edit
   to its body pointing at the patch release.
4. The container image and chart cannot be unpublished from
   ghcr.io once consumers have pulled them — the new tag is the
   forward-only fix path.

## First-time-only setup

Done once, then the workflow handles everything:

1. **GitHub repo settings** — Settings → Actions → General →
   Workflow permissions → "Read and write permissions" (already
   done as of the CI workflow PR)
2. **First release publishes packages** — after `v0.5.0-rc1`
   pushes the first image and chart, navigate to
   `https://github.com/gnana997?tab=packages`, open each new
   package, set visibility to **Public** in Package settings →
   Danger Zone
3. **Artifact Hub registration** — once the chart is public on
   ghcr.io, sign in to artifacthub.io with GitHub OAuth, add the
   repository (kind: Helm OCI, URL:
   `oci://ghcr.io/gnana997/charts/periscope`), copy the assigned
   `repositoryID` UUID into `deploy/helm/periscope/artifacthub-repo.yml`,
   and re-push the metadata layer with `oras` (command in the
   yaml file's header comment)
