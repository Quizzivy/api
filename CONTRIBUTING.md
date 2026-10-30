# Contributing

Read [`AGENTS.md`](AGENTS.md) first, then the standing rules in
[`Quizzivy/docs`](https://github.com/Quizzivy/docs/blob/develop/AGENTS.md).

## Branch model

Gitflow, as before the split.

| Branch | Purpose |
|---|---|
| `main` | Released only, tagged `vX.Y.Z`. Merging to `main` deploys the API. |
| `develop` | Integration. Every feature PR targets this. |
| `feature/<task>-<slug>` | One task. Branch off `develop`. |
| `release/<version>` | Release PR into `main`, then back-merged into `develop`. |
| `hotfix/<slug>` | Off `main`, merged to both. |

Merge with a merge commit, never a squash. The required check is **CI result**.

## A change that touches the API

1. The contract PR in [`Quizzivy/sdk`](https://github.com/Quizzivy/sdk).
2. This repository's PR, built with `make gen CONTRACT=../sdk/api`, with
   `make test` green on that contract. Link it from the sdk PR: the sdk PR
   merges only after this one is green (D-082 (iii)). CI checks only the
   version `api/sdk-version` names, so until step 3 the PR's own CI shows the
   contract job red on drift; say in the PR that the local run is the proof.
3. After the sdk merge, pin the published `-dev` version in `api/sdk-version`,
   `make gen`, and merge.
4. The web PR pins the same version.

## A release

The release PR into `main` pins the release `X.Y.Z` in `api/sdk-version`
(published by the owner's tag `vX.Y.Z` in `Quizzivy/sdk`). Once the API from
`main` is live, the deploy asks `Quizzivy/sdk` to make that version `latest`
(D-081, D-082 (iv)).

## Before opening a PR

- `make lint`, `make test-api-unit`, and `make gen-check`.
- Database changes: `make migrate-redo` (up, down, up) is clean.
- The PR template's boxes.
