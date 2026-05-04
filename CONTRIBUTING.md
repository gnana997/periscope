# Contributing to Periscope

Thanks for your interest. Periscope is in early development, so the surface is still moving — but bug reports, feature ideas, docs improvements, and pull requests are all welcome.

By participating in this project you agree to abide by the [Code of Conduct](CODE_OF_CONDUCT.md).

## Reporting security vulnerabilities

Please **do not** file a public issue for security vulnerabilities. See [`SECURITY.md`](SECURITY.md) for the responsible-disclosure process.

## Ways to contribute

- **File a bug.** Use [GitHub Issues](https://github.com/gnana997/periscope/issues). Include the cluster type (EKS / kind / minikube / …), the auth mode (OIDC provider, IRSA / Pod Identity / kubeconfig), and steps to reproduce.
- **Propose a feature.** Open an issue describing the use case before writing the code, especially for anything that affects the backend API, auth model, or audit shape.
- **Improve the docs.** Setup guides under [`docs/setup/`](docs/setup/), architecture notes under [`docs/architecture/`](docs/architecture/), RFCs under [`docs/rfcs/`](docs/rfcs/), and inline comments in code are all fair game. Doc changes auto-sync to [periscopehq.dev](https://periscopehq.dev) within an hour.
- **Send a pull request.** Bug fixes, refactors, and small features are usually fine without prior discussion. For larger work, open an issue first so we don't duplicate effort.

## Adding a new live-updating list page

Periscope has a cleanly-factored primitive for adding new live-updating
list pages — most of the work flows through a `kindReg` registry, so a
new kind is small and repetitive.

**Quick recipe (4 steps):**

1. **Define the DTO** in `internal/k8s/<kind>.go` — list-view struct +
   `<kind>Summary` function that maps the API type to the DTO.
2. **Add `Watch<Kind>`** in `internal/k8s/watch.go` — thin wrapper using
   `watchKind` with list/watch closures.
3. **Register the kind** in the `watchKinds` slice (in `cmd/periscope/main.go`) so the router and SSE
   stream handler pick it up.
4. **Add the SPA route** in the frontend so the new list page renders.

For the full walkthrough with code templates, see
[docs/architecture/watch-streams.md](docs/architecture/watch-streams.md)
(section "8. Adding a new kind").

Worked example: `internal/k8s/pods.go` and `WatchPods` in
`internal/k8s/watch.go`.

## Development environment

### Prerequisites

- Go 1.26+
- Node 22+
- A kubeconfig with access to at least one Kubernetes cluster — a local [kind](https://kind.sigs.k8s.io/) cluster is fine for most work
- `make`, `git`, optional `helm` (3.12+) for chart work

### First-time setup

```sh
git clone https://github.com/gnana997/periscope.git
cd periscope

# Backend deps
go mod download

# Frontend deps
cd web && npm ci && cd ..
```

### Run the dev servers

In two terminals:

```sh
make backend     # Go API on :8088
make frontend    # Vite dev server on :5173 (proxies /api → :8088)
```

Open <http://localhost:5173>.

The frontend dev server hot-reloads on save. The backend doesn't — kill and re-run `make backend` after Go edits, or use [air](https://github.com/cosmtrek/air) if you want auto-restart.

### Running against a kind cluster

```sh
kind create cluster --name periscope-dev
kubectl config use-context kind-periscope-dev
make backend     # picks up the kubeconfig automatically
```

For OIDC auth in dev, set `PERISCOPE_AUTH_MODE=raw` to bypass it temporarily. See [`docs/setup/auth0.md`](docs/setup/auth0.md) or [`docs/setup/okta.md`](docs/setup/okta.md) for the full IdP wiring when you need to test the real flow.

### Editor setup

- **gopls**: works out of the box. No special config needed.
- **ESLint**: install the editor extension and point it at `web/.eslintrc` (most editors auto-detect via `eslint.config.mjs`). Run `npm run lint` from `web/` for a CLI check.
- **Prettier**: not used in this repo — ESLint enforces formatting through `eslint --fix`. Disable Prettier for the `web/` workspace if you have it installed globally.

## Repository layout

```
cmd/periscope/    backend entry point — main.go and per-feature *_handler.go files
internal/         backend packages:
  audit/            audit event emitter + sinks (stdout, sqlite)
  auth/             OIDC user authentication (modes: shared / tier / raw)
  authz/            authorization resolver — IdP-group → tier mapping
  clusters/         cluster registry (load + lookup)
  credentials/      per-request credentials provider (Pod Identity / IRSA)
  exec/             pod exec session orchestration (websocket + k8s SPDY)
  httpx/            small HTTP helpers
  k8s/              all K8s API access — list/get/watch/yaml/diff per resource
  secrets/          OIDC client secret resolver (env, file, native, k8s secret)
  spa/              embedded SPA file server (build-tag-gated)
  sse/              shared SSE writer (used by watch streams + push events)
web/              React + TypeScript SPA (Vite, Monaco editor)
deploy/helm/      Helm chart — values.yaml, schema, templates
docs/             setup guides, architecture notes, RFCs (auto-syncs to periscopehq.dev)
examples/         reference configs (auth.yaml.auth0, auth.yaml.okta, etc.)
sketches/         throwaway design sketches (not shipped, not in CI)
.github/          CI workflows + CODEOWNERS
Makefile          common targets
```

## Testing

### Backend

```sh
make test                                  # full Go test suite
go test -race ./...                        # with race detector (CI runs without)
go test -run TestParseWatchStreamsEnv ./cmd/periscope/    # single test
```

Conventions: prefer table-driven tests. Use `testing` + `testify` where helpful. Tests for HTTP handlers should drive through `httptest.NewRecorder` rather than mocking response writers.

### Frontend

From `web/`:

```sh
npm test                  # vitest one-shot (same as CI)
npx vitest                # watch mode
npm run lint              # eslint
npx tsc --noEmit          # type check only
npm run build             # full production build (also runs tsc)
```

### Helm chart

```sh
helm lint deploy/helm/periscope
helm template deploy/helm/periscope > /dev/null                    # default values render
helm template deploy/helm/periscope --set audit.enabled=true > /dev/null
helm template deploy/helm/periscope --set watchStreams.kinds=off > /dev/null
```

### Local CI dry-run

The CI workflow lives at [`.github/workflows/ci.yaml`](.github/workflows/ci.yaml). To rehearse what CI will do before pushing:

```sh
golangci-lint run                # backend-lint job
go test ./...                    # backend-test job
cd web && npm ci && npm run lint && npm test && npm run build && cd ..
helm lint deploy/helm/periscope  # helm-lint job
go build -tags embed -trimpath -o bin/periscope ./cmd/periscope    # build-embed job
```

If all five succeed locally, CI will too.

## Coding conventions

### Go

- `gofmt` and `goimports` clean. Standard library first, third-party second, internal last in import groups.
- Error messages: lower case, no trailing punctuation. Wrap with `fmt.Errorf("doing the thing: %w", err)`.
- Avoid `panic` outside `main`. Return errors.
- Use `slog` for logging — structured fields, not `Sprintf`. `slog.WarnContext(ctx, "msg", "key", val)`.
- Always pass `context.Context` as the first parameter when the function does I/O.
- Gate logging on `errors.Is(err, context.Canceled)` before logging request errors — canceled requests are normal client behavior, not warnings.
- `golangci-lint` config is at [`.golangci.yml`](.golangci.yml). Known suppressions are documented inline in the config; remove a suppression when you fix the underlying issue.

### TypeScript / React

- ESLint + TypeScript strict mode. `npm run lint` must be clean.
- React components are functional with hooks. Don't pre-optimise with `useMemo` / `useCallback` unless there's a measured reason.
- **Server state lives in TanStack Query.** No `useEffect` to fetch. No fetch-then-setState. Use `useQuery` / `useMutation` and the existing `queryKeys` factory.
- For watch streams, use `useResource()` — it transparently picks SSE or polling based on server feature flags.
- Imports grouped: builtin React → third-party → app-relative (with `../`) → CSS / asset.
- Prefer `next/link` over `<a href>` for in-app routes (same lint rule on periscopehq.dev).

### Style across both

- Small, focused PRs. If you're tempted to add "while I'm here" cleanups, that's a separate PR.
- Comments explain **why**, not **what**. The code already says what.
- Don't add error handling, fallbacks, or validation for scenarios that can't happen. Trust internal code and framework guarantees. Only validate at system boundaries.
- Default to writing no comments. Only add one when the WHY is non-obvious — a hidden constraint, a subtle invariant, a workaround for a specific bug.
- No backwards-compatibility hacks. If something is unused, delete it.
- Keep tests green. Run them before pushing.

## How to add a new...

### ...K8s resource (list endpoint + page)

1. **Backend types** — add the `Foo`, `FooList`, `FooDetail` Go types to [`internal/k8s/types.go`](internal/k8s/types.go).
2. **Backend list/get** — create `internal/k8s/foos.go` with `ListFoos`, `GetFoo`, `GetFooYAML` and a `fooSummary` projection (mirrors any existing resource file e.g. [`services.go`](internal/k8s/services.go)).
3. **Backend routes** — register `/api/clusters/{cluster}/foos`, `/foos/{ns}/{name}`, `/foos/{ns}/{name}/yaml`, `/foos/{ns}/{name}/events` in `cmd/periscope/main.go` (search for the `services` block and copy the shape).
4. **Frontend types** — add the same types to `web/src/lib/types.ts`.
5. **Frontend wiring** — `web/src/lib/api.ts` (api methods), `web/src/lib/k8sKinds.ts` (KIND_REGISTRY entry), `web/src/lib/listShape.ts` (LIST_ITEMS_KEY entry), `web/src/lib/resources.ts` (RESOURCES catalog, sets sidebar group + label).
6. **Frontend page + describe** — `web/src/pages/FoosPage.tsx` (list view), `web/src/components/detail/describe/FooDescribe.tsx` (detail panel).
7. **Route** — add a lazy-loaded route in `web/src/routes.tsx`.

EndpointSlices is the most recent end-to-end example — see [PR #28](https://github.com/gnana997/periscope/pull/28) (backend) and [PR #30](https://github.com/gnana997/periscope/pull/30) (frontend).

### ...watch stream kind (real-time list updates)

1. Add `WatchFoos` to `internal/k8s/watch.go` using the existing `watchKind[K8sType, OurType]` generic primitive.
2. Append a `kindReg` entry to the `watchKinds` slice in `cmd/periscope/main.go`. That's the **only** other edit on the backend — route registration, `/api/features` enumeration, env-var grammar, and startup logging all read from the registry.
3. Frontend: extend `WatchStreamKind` union and `WATCH_STREAM_KINDS` array in `web/src/lib/types.ts`, and add a `LIST_REFETCH_INTERVAL` entry for the polling fallback cadence.

See [`docs/architecture/watch-streams.md`](docs/architecture/watch-streams.md) for the full architecture.

### ...audit event verb

1. Add the verb to `internal/audit/types.go`'s `Verb` enum.
2. In the relevant handler (e.g. `cmd/periscope/exec_handler.go`), call `audit.Emit(ctx, audit.Event{Actor, Verb, Target, Outcome, …})` at the right point in the request lifecycle.
3. Frontend: add the verb to the `AuditVerb` union in `web/src/lib/types.ts` so the audit page filter picks it up.
4. Update [`docs/setup/audit.md`](docs/setup/audit.md)'s verb list.

### ...env var or Helm value

1. Read it via `os.Getenv` or a small `parseIntEnv` / `parseDurationEnv` helper in `main.go`. Document the default in a comment next to the parse call.
2. Surface it in the Helm chart: add the value to [`deploy/helm/periscope/values.yaml`](deploy/helm/periscope/values.yaml) (with a comment block explaining what it does and when to override), the schema in [`deploy/helm/periscope/values.schema.json`](deploy/helm/periscope/values.schema.json), and the env-var injection in [`deploy/helm/periscope/templates/deployment.yaml`](deploy/helm/periscope/templates/deployment.yaml).
3. Add a row to the flat reference at [`docs/setup/values.md`](docs/setup/values.md) (single grep-friendly page operators reach for during `helm upgrade` — keep it in lockstep with `values.yaml` or it rots).
4. Document the operator-facing rationale in the relevant `docs/setup/*.md` guide.

## Pull request process

### Branch naming

```
<type>/<scope>-<short-description>
```

Examples: `feat/watch-streams-all-kinds`, `fix/helm-perm-fallback`, `docs/refresh-readme`, `chore/codeowners`. Forks are welcome — fork the repo, push to a topic branch on your fork, open the PR against `main`.

### Commit messages

Conventional-commits style. The first line is the most important — keep it under ~70 characters and in present tense.

```
<type>(<scope>): <short summary>

<body — optional, wrap at ~72 chars>
```

**Common types:** `feat`, `fix`, `refactor`, `perf`, `docs`, `test`, `chore`, `ci`.

**Common scopes:** `web`, `backend`, `helm`, `auth`, `audit`, `watch-streams`, `helm-releases`, `k8s`, `editor`, `exec`, `ci`, `docs`, `community`.

Examples:

```
feat(watch-streams): add Tier-A workload-controller SSE feeds
fix(k8s): handle compact-list YAML in stripForEdit
refactor(web): unify YAML editor on EditorSource
docs(setup): refresh deploy.md 10.5 for the expanded watch-streams kinds
chore(helm): relax watchStreams.kinds schema regex
ci: add helm-lint job
```

### What CI runs

Every push and PR triggers [`.github/workflows/ci.yaml`](.github/workflows/ci.yaml). Five jobs:

| Job | What it runs |
|---|---|
| `Backend lint` | `golangci-lint run` against the config in `.golangci.yml` |
| `Backend test` | `go test ./...` |
| `Web (lint, test, build)` | `npm ci`, `npm run lint`, `npm test` (vitest), `npm run build` (tsc + vite) — uploads `web/dist` as an artifact |
| `Helm lint` | `helm lint` + `helm template` smoke renders against three value combos |
| `Build embedded binary` | Downloads `web/dist`, runs `go build -tags embed` to produce a single-binary release artifact |

If a job fails, click the job in the PR's Checks tab to see the log.

### Code review

[`.github/CODEOWNERS`](.github/CODEOWNERS) declares per-path code ownership. Every PR requires at least one approval from a code owner before it can be merged. Security-sensitive paths (auth, authz, audit, exec, credentials) are explicitly listed so future co-maintainers can be added with one-line edits.

### Merge requirements

The `main` branch is protected. To merge a PR:

- **All five CI jobs must pass** (status checks)
- **At least one code-owner approval** is required
- **The PR must be up to date with `main`** before merging
- **Force-pushing is blocked**; use new commits + the merge UI
- **Linear history is enforced** — use squash or rebase merge methods (no merge commits land on `main`)

PRs are typically squash-merged; the maintainer will collapse review-fix commits into the body of the squashed commit message.

### Address review feedback

Push additional commits — the maintainer will squash on merge. Don't force-push to your PR branch (the ruleset blocks it on `main` but it's bad form on PR branches too — it discards review history).

## Documentation contributions

- Setup guides → [`docs/setup/`](docs/setup/)
- Architecture notes → [`docs/architecture/`](docs/architecture/)
- RFCs (proposals) → [`docs/rfcs/`](docs/rfcs/) — number sequentially (`0001-`, `0002-`, …)

Style:
- Lowercase headlines for short phrases (`# audit log` not `# Audit Log`); sentence case for longer ones.
- Code blocks should always have a language hint (` ```yaml`, ` ```go`, ` ```sh`).
- Prefer relative links between docs (`[deploy guide](./deploy.md)`) so they work on both GitHub and on periscopehq.dev.
- Don't embed images unless strictly necessary; ASCII / mermaid blocks render in both contexts.

**Auto-sync to periscopehq.dev:** the marketing site at [periscopehq.dev](https://periscopehq.dev) syncs `docs/**/*.md` from this repo every hour. Frontmatter is auto-generated from the H1 if missing. To force a refresh on the site, redeploy the periscopehq site (or set `FORCE_SYNC=1` in its dev env).

## Release process

Until the project tags a `v0.1` release, releases are informal — see open [issues](https://github.com/gnana997/periscope/issues) for what's queued. The release flow once formalized will be:

1. Bump versions in `web/package.json`, the Helm `Chart.yaml`, and the Go ldflags
2. Tag `vX.Y.Z` on `main`
3. CI's `Build embedded binary` job uploads the artifact
4. Open a GitHub Release with the artifact + auto-generated release notes from PR titles
5. periscopehq.dev's `/changelog` page picks up the new release within 5 minutes (ISR cache)

## License

By contributing, you agree that your contributions will be licensed under the [Apache License 2.0](LICENSE). No CLA — the standard inbound=outbound contribution model.
