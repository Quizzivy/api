# CI

`.github/workflows/ci.yml` runs on every pull request and on every push to
`main`, `develop` and `work/**`. One check, **CI result**, is what the branch
rules require; it passes only when every job did what the plan asked of it.

## What runs

| Job | Reads | Does |
|---|---|---|
| Plan | everything | Tests `scripts/ci/`, then decides which jobs run |
| Contract | `code` | `make gen-check`: fetch `@quizzivy/sdk` at the version `api/sdk-version` names, copy its contract, test data and catalogs into `api/`, regenerate `server/gen`, fail on drift |
| Server lint | `server` | vet, staticcheck, golangci-lint, gofmt |
| Server tests | `server` | unit, integration and end-to-end tiers, the Word converter tests; migrations up, down, up |
| Production image | `image` | Builds the root `Dockerfile`; starts `/app/api` and `/app/import-worker` from it as `nonroot` |
| CI result | — | Fails unless each job above did what the plan said |
| Sonar | everything | Pushes to `develop` only; not part of the result |

## How a job is skipped

A job is skipped only when there is proof that it passed on the same files and
has not failed on them since. Nothing is skipped because of what a diff looks
like.

1. `scripts/ci/plan.mjs` names three sets of files (`SETS`) and the set each job
   reads (`JOBS`). A set is a base (`all` or `none`) with directories and files
   included or excluded; the longest rule that matches a path decides.
2. Plan hashes the mode, blob and path of every file in a job's set. The
   workflow, `.github/actions/` and `scripts/ci/` are in every set, so a change
   to CI itself changes every hash. `scripts/dev/` holds the team's local
   scripts; no job reads it, `plan.test.mjs` fails if one does, and it is in no set.
3. A job's last step records how it ended, as an artifact kept for 30 days:
   `ci-pass-<job>-<hash>` after a pass, `ci-fail-<job>-<hash>` after a failure,
   nothing when it was cancelled. Plan skips a job when the newest pass for its
   hash is newer than the newest failure. Only artifacts uploaded by a run on a
   branch of this repository count; a fork's do not.
4. Each job checks out only its set (a sparse checkout built from the same
   rules). A test that reads a file outside its set fails with "no such file"
   instead of being skipped when that file changes.

So, while the proof stands, a pull request that changes only `docs/plan/` runs
Plan and CI result, a change to the deploy notes alone does not build the
production image, and a merge whose tree already passed as a pull request runs no job again. The proof
does not always stand: a marker expires after 30 days, and a tree nobody has
tested (the base moved under the pull request, for example) has none.

Three cases never use earlier proof:

- **A push to `main`.** The commit that deploys is tested in full.
- **"Re-run all jobs".** That is how to force a full run. "Re-run failed jobs"
  re-runs only what failed.
- **A job that failed on these files since it last passed.** A forced run that
  fails records the failure, and every later run on the same files runs the
  job until it passes again.

What the hash cannot see is anything outside the tree: the runner image, the
`postgres:18` image (which `mirror.gcr.io` serves), the Debian packages the Word converter image installs,
tools fetched at run time. The 30-day expiry and the full run on `main` are
what bring a job back in front of those.

A skipped job writes no toolchain cache. After a pull request changes
`server/go.sum`, its merge is skipped as proven, so
the new Go cache exists only in that pull request's scope. Each later
pull request builds its own on its first run, until a push to the base branch
runs a job for real or the next push to `main` does. That costs the Go jobs
some tens of seconds and no result.

## How long it takes

The timings recorded before the split (`SekiroKenjii/Quizzivy`, 2026-10-03 to
2026-10-14) were dominated by the web jobs, 83% of the job-minutes; those jobs
now run in `Quizzivy/web`. In that period Server tests took a median 4 min 31 s
(p90 6 min 44 s) and Contract 1 min 19 s, so a full run here is about as long
as Server tests. Measure again once this repository has a few weeks of runs.

## Tests in CI

- **A skipped test fails Plan.** `scripts/ci/tests/skipped-tests.test.mjs` fails
  on a Go `t.Skip` whose message neither names the variable it waits for
  (`TEST_*`, `S3_*`, `IMPORT_*`, `APP_DATABASE_URL`) nor is listed in
  `ENVIRONMENT_SKIPS`. A new entry is a test that stops running somewhere, so it
  takes the owner's word. `Quizzivy/web` carries the same rule for web tests.
- **The browser suites live in `Quizzivy/web`.** Its live E2E, the Word import
  worker spec and the slow tail (E2E 5, which waits out a real one-minute
  deadline) start this repository's API at a pinned ref. The server's half of
  E2E 5, the attempt deadline, is proved here by the attempts `timeout_test.go`.

## The slow tail

Moved to `Quizzivy/web` with the Playwright suites.

## When a job needs a file outside its set

Add the path to that set's `include` in `scripts/ci/plan.mjs`, in the same pull
request as the test that reads it. `scripts/ci/tests/plan.test.mjs` fails when
a rule no longer matches a tracked file, so a moved file is noticed.

Today's cross-tree reads:

| Reader | File |
|---|---|
| `server/internal/modules/identity/repositories/tests/stored_email_test.go` | `docs/setup/operations.md` |
| Server tests | `api/`, `migrations/`, `fly.toml`, `Dockerfile`, `docker/word-converter/`, `.github/workflows/deploy.yml` |
| Production image | `Dockerfile`, `.dockerignore`, `fly.toml`, `server/`, `migrations/`, `scripts/provision-db.sh` (its set, `image`, holds these and nothing else) |

The reads that crossed into the web before the split now go through the SDK
package (D-082 (ii)): `server/tests/isolation_world_test.go` and the media
library test read `server/tests/testdata/unit5-listening.mp3`; the audit catalogue
test reads `api/catalogs/audit-actions.json` and the join-code test
`api/catalogs/join-code.json`, both copied from the pinned SDK by `make contract`.
The web checks its locale files and its join-code parser against the same
catalogues.

A test must not treat a missing file as a reason to skip itself: in CI that
would hide the mistake this layout exists to show.

## Adding a job

Add it to `JOBS` with its set, give it the same `needs`, `if`, `env.JOB`,
sparse checkout and final `record` step as its neighbours, and add it to the
`needs` of **CI result**. `plan.test.mjs` reads the workflow and fails when a
job is missing from either list or lacks one of those parts; Plan runs that
test on every run. Sonar is the one job outside the result, by intent.

## Runs on a branch

A pull request's newer push cancels its run in progress. On `main`, `develop`
and `work/**` a run in progress is never cancelled, but runs go one at a time
and only the newest waiting one is kept, so a commit pushed while two others
wait may show a cancelled run. Its tree is covered by the run after it.

The result of a push run is named **CI result (push)**. The head commit of a
pull request from `main`, `develop` or a work branch has both runs, and only
the pull request's run tests the merge; the branch rules read that one.

## Pull requests from forks

A fork's pull request runs the workflow, the scripts and the tests from its
own merge commit, so its green **CI result** says only what its own files
chose to check. Read a fork's pull request before merging it, whatever CI
says. Its markers are never proof for another run, and its token cannot write
packages.

## The object store and the Word converter in Server tests

Server tests starts MinIO after the unit tier, so the integration tier runs
with a real object store: the storage client's tests and the import intake,
artifact and PDF pipeline tests store and read real objects. The storage
settings (`S3_*`) are set on the steps that use them, not on the job.
`server/internal/platform/storage/tests/s3_test.go` carries no build tag, so
the unit tier compiles it too, and there it has to skip. The bucket is
`quizzivy-imports`, the private one imports are kept in.

After the integration tier one step builds `docker/word-converter` and the next,
**Import converter tests**, runs the tests that need that image: the
converter's own (`internal/platform/wordconvert/tests`) and the test that takes
a synthetic paper through the real pipeline
(`internal/modules/imports/repositories/tests`). The tests are given the
image's id, because the converter refuses a tag. The step fails before
`go test` when it has no id, rather than skip every test in it and pass.

They run one package at a time (`-p 1`). A conversion takes one Docker-wide
slot, a container with a fixed name, and a conversion that finds the slot taken
answers busy. `go test` runs packages in parallel, the converter tests live in
two packages, and one of them occupies the slot on purpose, so run together
they would pass or fail by timing. In the integration tier these tests skip,
because `TEST_WORD_CONVERTER_IMAGE` is not set there.

`APP_DATABASE_URL`, the DSN of `quizzivy_app`, is set on the Server tests job
for `TestProfileAndPreferencesPersistWithTheActualAppRoleAndCheckedRollback`,
which proves profile writes through the role the API connects as and skips
without it. The step **App-role profile test ran** runs it once with `-v` and
fails unless it passed, so removing the variable cannot turn it back into a
silent skip. `make test-api-integration` sets it from `APP_DSN`.

`TEST_MAINTENANCE_DATABASE_URL`, the DSN of `quizzivy_maintenance`, is set on the
same job for the tests in `internal/core/maintenance/tests` that run the commands
as the role. Each makes a database of its own with the migrate role's `CREATEDB`
(`scripts/provision-db.sh` gives it), migrates it and drops it, so no test shares
rows with another. Where `CI` is set and the variable is not, those tests fail
instead of skipping, for the reason the paragraph above gives. The role's limit
of two connections is cluster-wide, so they do not run in parallel with one
another.

`TEST_SUPERUSER_DATABASE_URL`, the DSN of the service container's superuser, is set on the
same job for `TestTheProvisioningScriptRunsAsAManagedAdminThatIsNotASuperuser` in
`internal/platform/db/tests`. It makes a role with `CREATEROLE` and `CREATEDB` that is not a
superuser, as a managed provider's admin is, runs `scripts/provision-db.sh` as it against a
database of its own and drops both, because the script's node tests use a stub `psql` and cannot
see a statement a non-superuser may not run. Where `CI` is set and the variable is not, it fails
instead of skipping.

`.github/workflows/retention-sweep.yml` is the nightly sweep, not a CI job: the
planner lists it with the files whose change runs no code job, and
`scripts/ci/tests/retention-sweep.test.mjs`, which `Plan` runs, pins its
schedule, its one-at-a-time group, the 15-minute limit, that the environment
secret is read by the sweep step alone and that no expression is written into a
script (`docs/setup/operations.md`, "Retention and anonymization").

A test that needs the object store or the image skips when its variable is
unset, and `go test` reports a package whose tests all skipped as `ok`. The
image id is the one such value that comes from another step, and an expression
that names a renamed step or a mistyped output is empty without an error; that
is why **Import converter tests** checks it first. The storage settings are
written on the steps themselves. The steps do not print each test; to see by
name which of these tests ran, add `-v` to the step's `go test` for one run, as
the pull request that added the steps did.

## The production image

Every other job runs the code on the runner's file system, so a start-up check
that depends on the image's own (INC-5: `/home/nonroot/imports` shipped at 0755
and the API refused it, on Fly only) passed every test. **Production image**
builds the root `Dockerfile` and runs `scripts/ci/smoke-production-image.sh`,
which does what a deploy does:

1. Checks that the image's user is `nonroot:nonroot`.
2. Runs `/app/migrate -dir /app/migrations up` from the image, as Fly's
   `release_command` does, against the job's `postgres:18` service.
3. Starts `/app/api` and `/app/import-worker` from the image, each in its own
   container on the host network and with no `--user`, so they run as the
   image's. The environment is `fly.toml`'s `[env]` read by the script, plus
   throwaway values for the secrets `docs/setup/dns.md` lists, and the worker's
   wake address and URL moved from `fly-local-6pn` to `localhost`.
4. Waits up to 60 seconds for `GET /livez` to answer and for the worker to log
   `import worker started`, and fails, printing both logs, when either
   container exits first or the time runs out.

MinIO is not started: neither process reaches the object store while it starts.
The job's set is `image`, the files above plus `scripts/ci/`; a change to the
docs or the contract alone does not run it. It took 1 min 22 s on the first run, most
of it the Go build inside the image; the smoke test itself takes seconds. The
base image `golang:1.27-alpine` comes from the mirror, like the others.

To run it locally, with the services of `docs/team/environment.md`: build the
image, create a database for the run with
`QUIZZIVY_DB_NAME=smoke scripts/provision-db.sh`, then
`SMOKE_DATABASE=smoke scripts/ci/smoke-production-image.sh <image>`.

## The Word import worker in E2E (live API)

Moved to `Quizzivy/web` with the live E2E. It builds the converter image with
this repository's `.github/actions/word-converter` at the pinned ref, so one
build recipe still serves both.

## MinIO

`.github/workflows/minio-image.yml` builds `docker/minio` once per version of
that directory and pushes it to `ghcr.io/<owner>/quizzivy-minio:<hash>`. The
package is public, as the repository is; its labels name the licence and where
the source commits are listed. `.github/actions/minio` pulls that tag and
falls back to building from source when it is not published, which is what a
pull request that changes `docker/minio` does on its first run.

## Where CI pulls images from

Docker Hub limits anonymous pulls per address, and a hosted runner shares its
address with others, so a pull fails intermittently with `toomanyrequests` and
no test runs. CI therefore pulls every Docker Hub image from Google's mirror,
`mirror.gcr.io/library/<name>`, which takes no login and has no such limit:

- The `postgres:18` service of **Server tests** and **Production image** names
  `mirror.gcr.io/library/postgres:18`. It is the same official image, still
  PostgreSQL 18, and the tag still follows the 18.x minor release. It is not
  pinned by digest: a digest the mirror has not cached would fall through to
  Docker Hub, and the floating tag is what CI ran before. To pin one, append
  `@sha256:<digest>` to the image in both jobs and bump it deliberately.
- `scripts/ci/mirror-images.sh` pulls a base from the mirror and tags it under
  its Docker Hub name, so the build that follows finds it in the local store.
  `.github/actions/word-converter`, which Server tests uses, runs it for
  `debian:trixie-slim` before building the Word converter; `.github/actions/minio` and `minio-image.yml` run it for
  `golang:1.27-alpine` and `alpine:3.22` before building MinIO. A base the
  mirror cannot serve prints a warning and is pulled from Docker Hub as before.
  A new `FROM` on a Docker Hub image adds its name to the call beside the build.
- MinIO itself comes from `ghcr.io`, and nothing else in the workflows pulls from
  Docker Hub.

If the mirror ever fails too, the alternative is a Docker Hub login from a
repository secret, which raises the limit for the account; it needs Thuong to
create a read-only access token and add it as a secret, and is not built.

## Third-party actions

An action from outside `actions/` is pinned to a full commit SHA, with the tag
it was resolved from in a trailing comment (`uses: owner/name@<sha> # v1.2.3`).
A tag or branch moves, and the deploy workflow holds `FLY_API_TOKEN`, so a moved
ref would run someone else's code with it. To update one, resolve the new tag
to its commit (the peeled one, `git ls-remote <repo> 'refs/tags/<tag>^{}'`, when
the tag is annotated), check that the action file exists there, and change the
SHA and the comment together. The `actions/*` actions stay on a major tag.

## Branch rules

Three rulesets, under Settings › Rules:

| Ruleset | Branches | Rule |
|---|---|---|
| `pull requests only` | `main`, `develop`, `work/**` | No force push; changes arrive by pull request |
| `no deletion` | `main`, `develop` | The branch cannot be deleted |
| `ci result` | `develop`, `work/**` | **CI result** must pass, except when a branch is created, so `work/redesign-r<k>` can be cut from `develop` |

This repository started with these rules unset: they are the owner's to create
under Settings › Rules, the same three, with `main` in `ci result` from the
start, since `main` here already carries this workflow.

A repository admin can merge a pull request past a failing check. That is for
the case where CI itself is broken, and the pull request should say so.

## The wiki

The wiki and its workflow moved to `Quizzivy/docs`, with the documentation.

## The project board

The project-board sync moved to `Quizzivy/docs`. Its rules still apply to this
repository's issues and pull requests: label the issue, and name it in the pull
request body with `Task: #N` or `Part of: #N`.

## Deploy

`.github/workflows/deploy.yml` runs when CI succeeds for a **push** to `main`,
and deploys only when that commit is still the tip of `main`.

- The back-merge pull request from `main` to `develop` also has `main` as its
  head branch; its CI run does not deploy.
- Re-running an old CI run of `main` to green does not put that release back
  into production.
