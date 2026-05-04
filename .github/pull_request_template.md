<!--
Thanks for the PR. A few notes before you submit:

- For larger work, please link to the issue / RFC where the design was discussed.
- For backend or auth/audit changes, please re-read CONTRIBUTING.md's "Coding
  conventions" section before opening.
- CI will run golangci-lint, go test, npm lint/test/build, helm lint+template,
  and an embedded-binary build. Locally: `make test` + `make build` covers most.
-->

## Summary

<!-- 1-3 sentences. What changed and why. -->

## Related issue / RFC

<!-- Closes #N, refs #M. If there's no issue, briefly justify why. -->

## Type of change

- [ ] Bug fix (non-breaking)
- [ ] New feature (non-breaking)
- [ ] Breaking change (user-visible API, config shape, or Helm values)
- [ ] Docs only
- [ ] Refactor / internal cleanup
- [ ] Test or CI only

## Surfaces touched

- [ ] HTTP API (`/api/...`) — covered by semver, see `docs/api.md`
- [ ] Helm values — see `deploy/helm/periscope/values.yaml` schema
- [ ] OIDC / auth / authz
- [ ] Audit / RBAC
- [ ] Agent backend / tunnel
- [ ] SPA / frontend
- [ ] Documentation
- [ ] None of the above

## How was this tested?

<!--
Describe the test plan. For bugs, what was the repro and what does the fix
exhibit now. For features, the golden path + edge cases you exercised.
-->

## Checklist

- [ ] I read [CONTRIBUTING.md](../CONTRIBUTING.md).
- [ ] Tests added or updated where it makes sense.
- [ ] Docs updated (`docs/`, `README.md`, or `CHANGELOG.md` under `[Unreleased]`).
- [ ] No secrets, real cluster names, or real OIDC client IDs in committed files.
