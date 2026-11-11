# Quizzivy API: agent working rules

The standing rules live in [`Quizzivy/docs` `AGENTS.md`](https://github.com/Quizzivy/docs/blob/develop/AGENTS.md):
sources of truth, non-negotiables, code style, the composition root,
authentication, migrations, tests, the high-risk areas. They apply here
unchanged. This file adds what is specific to this repository since the split
(D-082, 2026-10-30).

## The contract is pinned, not authored here

- `api/openapi.yaml`, `api/testdata/` and `api/catalogs/` are written by
  `make contract` from `@quizzivy/sdk` at `api/sdk-version`. Never edit them:
  CI's Contract job fails on any difference from the pinned package.
- Adding or changing an endpoint starts with a PR in `Quizzivy/sdk`. Work
  against it locally with `make gen CONTRACT=../sdk/api`; the strict server
  interface does not build until every new operation has a handler.
- Data both sides agree on (the audit action sentences, the join-code format,
  the pasted-text counts, the content fixtures) is in that package too. A
  server change to any of them needs the matching sdk change in the same
  release; the tests here compare the server with the pinned copy.
- `api/oapi-codegen.yaml` is this repository's own: how the Go code is generated.

## CI

`docs/setup/ci.md` describes it. Plan (`scripts/ci/plan.mjs`) skips a job
whose files already passed. Each job checks out only its set, so a test that
reads a file outside it needs that path in the set's `include`, in the same PR.
The live E2E runs from `Quizzivy/web`, against this repository at the ref in
that repository's `api-ref`.

## Deploys

Merging to `main` deploys the API (`.github/workflows/deploy.yml`) once the
repository variable `API_HOST` is set; until production switches over from
`SekiroKenjii/Quizzivy` it is unset and nothing deploys from here. A
successful deploy dispatches `promote-sdk` to `Quizzivy/sdk` with the version
in `api/sdk-version`. Deploys, secrets, tags and merges to `main` are the
owner's.
