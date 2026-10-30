# Quizzivy API

The Go backend of Quizzivy: the API (`server/cmd/api`), the Word import worker
(`server/cmd/import-worker`), maintenance (`server/cmd/maintenance`), the
migrations and the seed. PostgreSQL 18 (Neon in production), Cloudflare R2.

| Repository | What it holds |
|---|---|
| [`Quizzivy/sdk`](https://github.com/Quizzivy/sdk) | the API contract, `@quizzivy/sdk`. **A contract change starts there.** |
| `Quizzivy/api` (this one) | the server, built against the contract version pinned in `api/sdk-version` |
| [`Quizzivy/web`](https://github.com/Quizzivy/web) | the React app, pinned to the same SDK |
| [`Quizzivy/docs`](https://github.com/Quizzivy/docs) | spec, plan, standing rules (`AGENTS.md`) |
| [`Quizzivy/infra`](https://github.com/Quizzivy/infra) | the VPS: Compose, Caddy, deploy scripts |

## Quick start

```bash
cp .env.example .env
make doctor      # what is installed, what is still blocked
make up          # postgres:18 + MinIO
make migrate
make seed
make dev         # api on :8080 (and the Word worker when imports are configured)
make help
```

Use `localhost`, never `127.0.0.1`, so the cookies behave as in production.

## The contract

`api/` is **not edited here**. `make contract` writes `api/openapi.yaml`,
`api/testdata/` and `api/catalogs/` from `@quizzivy/sdk` at the version in
`api/sdk-version`, and `make gen` regenerates `server/gen/` from it. CI runs
`make gen-check` and fails when either drifts (D-082).

The registry needs a token even to read. Locally, add to `~/.npmrc` a GitHub
token with `read:packages`:

```ini
@quizzivy:registry=https://npm.pkg.github.com
//npm.pkg.github.com/:_authToken=<token>
```

To change the API: open the PR in `Quizzivy/sdk`, then here run
`make gen CONTRACT=../sdk/api` against your sdk checkout, implement the handlers
(the strict server does not build without them) and open this PR. Once the sdk
PR is merged and its `-dev` build is published, put that version in
`api/sdk-version`, `make gen`, and commit. The sdk PR merges only when this one
is green on its contract (D-082 (iii)).

## More

- [`AGENTS.md`](AGENTS.md): the rules for this repository, on top of the standing rules in `Quizzivy/docs`.
- [`CONTRIBUTING.md`](CONTRIBUTING.md): branches and pull requests.
- [`docs/setup/`](docs/setup/): deploy, DNS, operations, CI.
