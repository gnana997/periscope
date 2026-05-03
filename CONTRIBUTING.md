# Contributing to Periscope

Thanks for your interest in contributing. Periscope is in early development, so the surface is still moving — but bug reports, feature ideas, and pull requests are all welcome.

By participating in this project you agree to abide by the [Code of Conduct](CODE_OF_CONDUCT.md).

## Ways to contribute

- **File a bug.** Use [GitHub Issues](https://github.com/gnana997/periscope/issues). Include the cluster type (EKS / kind / minikube / …), the auth mode (OIDC provider, IRSA / Pod Identity / kubeconfig), and steps to reproduce.
- **Propose a feature.** Open an issue describing the use case before writing the code, especially for anything that affects the backend API or auth model.
- **Improve the docs.** Setup guides under [`docs/setup/`](docs/setup/), RFCs under [`docs/rfcs/`](docs/rfcs/), and inline comments in code are all fair game.
- **Send a pull request.** Bug fixes, refactors, and small features are usually fine without a prior discussion. For larger work, open an issue first so we don't duplicate effort.

## Reporting security vulnerabilities

Please **do not** file a public issue for security vulnerabilities. See [`SECURITY.md`](SECURITY.md).

## Development setup

Prerequisites:

- Go 1.26+
- Node 22+
- A kubeconfig with access to at least one Kubernetes cluster (a local [kind](https://kind.sigs.k8s.io/) cluster is fine for most work)
- `make`, `git`

Run the backend and frontend in two terminals:

```sh
make backend     # Go API on :8088
make frontend    # Vite dev server on :5173 (proxies /api → :8088)
```

Open <http://localhost:5173>.

### Backend tests

```sh
make test
```

### Frontend tests

```sh
cd web
npx vitest run            # one-shot
npx vitest                # watch mode
npm run lint              # ESLint
npx tsc --noEmit          # type check only
npm run build             # production build (also runs tsc)
```

### Repository layout

```
cmd/periscope/    backend entry point
internal/         backend packages (auth, authz, clusters, credentials,
                  exec, k8s, secrets, httpx, spa)
web/              React + TypeScript SPA (Vite, Monaco editor)
deploy/helm/      Helm chart
docs/             setup guides and RFCs
examples/         reference configs
sketches/         throwaway design sketches (not shipped)
```

## Coding conventions

### Go

- `gofmt` and `goimports` clean.
- Standard library first, third-party next, internal last in import groups.
- Error messages: lower case, no trailing punctuation (Go convention). Wrap with `fmt.Errorf("doing the thing: %w", err)`.
- Avoid `panic` outside `main`. Return errors.
- Tests with `testing` + `testify` where helpful. Table-driven tests preferred.
- Use `slog` for logging; structured fields, not `Sprintf`.

### TypeScript / React

- ESLint + TypeScript strict mode. `npm run lint` must be clean.
- React components are functional with hooks. Prefer `useMemo` / `useCallback` only when there's a real reason; don't pre-optimise.
- React Query for all server state. Don't reach for `useEffect` to fetch.
- Imports grouped: builtin React → third-party → app-relative (with `../`) → CSS / asset.
- Comments explain *why*, not *what*. The code already says what.

### Style across both

- Small, focused PRs. If you're tempted to add "while I'm here" cleanups, that's a separate PR.
- Keep tests green. Run them before pushing.

## Pull request process

1. Fork the repo and create a topic branch off `main`.
2. Make your change. Keep the diff focused. If you touched the API or config shape, update the relevant docs.
3. Ensure `make test`, `make build`, frontend `npx vitest run`, `npm run lint` all pass locally.
4. Push and open a PR against `main`.
5. The PR description should answer:
   - **What** does this change?
   - **Why** is it needed? (Link the issue if there is one.)
   - **How** to test it? (Manual steps if not covered by tests.)
6. Address review feedback as additional commits — the maintainer will squash on merge.

### Commit messages

Loose conventional-commits style works well. The first line is the most important — keep it under ~70 characters and in present tense.

```
<type>(<scope>): <short summary>

<body — optional, wrap at ~72 chars>
```

Common types: `feat`, `fix`, `refactor`, `docs`, `test`, `chore`.
Common scopes: `web`, `backend`, `helm`, `auth`, `k8s`, `editor`, `docs`.

Examples:

```
feat(web): drift detection — show-diff overlay
fix(k8s): handle compact-list YAML in stripForEdit
refactor(web): unify YAML editor on EditorSource
docs: restructure README around standard OSS sections
```

## License

By contributing, you agree that your contributions will be licensed under the [Apache License 2.0](LICENSE).
