# Handoff: Jeff eval harness

- Plan: `docs/plans/evals-harness.md`
- Repository: `/home/alessandro/Work/tries/jeff`
- Branch: `master`
- Revision: `HEAD` (commit message: `feat(evals): add versioned evaluation harness`)
- Status: `implemented-unverified`

## Completed

- Added the versioned harness under `evals/`: strict datasets/config, real Jeff subprocess runner, synthetic provider, real HTTPS metering proxy, usage/attempt/latency/cost metrics, threshold overrides, baseline comparison, gates and sanitized reports.
- Added dev/test/holdout manifests and `.github/workflows/evals.yml` with PR synthetic smoke plus protected `workflow_dispatch` real test/holdout jobs.
- Real runs load `TYPESAFE_API_KEY` from the environment first and fall back to the Jeff system keyring via `credentials.LoadAPIKey()`.
- Removed the project-level `typesafe-ai` skill package, its `.claude`/`.pi` links and `skills-lock.json`.

## Evidence

From `/home/alessandro/Work/tries/jeff`:

- `go test ./... -count=1` — pass
- `go vet ./...` — pass
- `git diff --check` — pass
- Dataset validation for dev/test/holdout — pass
- Synthetic smoke: dev 16 cases and holdout 14 cases — pass
- Real local smoke: `/tmp/jeff-eval-real-hT20VN/`, 16 cases, 15 provider attempts, 0 unavailable, calculated cost `$0.00066339`, exit `1` because quality gates failed (not an execution error).

## Known blockers / next actions

1. Do not create `evals/baselines/synthetic-smoke.json` from the current dirty checkout. First establish a clean commit containing the current catalog/eval changes, then generate and review an approved synthetic baseline from that clean revision.
2. The release workflow was not executed locally; it needs the protected `evals-release` environment and `TYPESAFE_API_KEY_EVALS` secret on `main`.
3. The worktree still contains unrelated pre-existing changes. Do not reset or clean them blindly; inspect `git status` before continuing.
4. Resume by reading the complete plan, then reconcile `HEAD`/status and run the affected checks before changing the baseline or CI workflow.
