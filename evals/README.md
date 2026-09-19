# Jeff eval harness

The harness validates versioned datasets and runs the real `jeff` CLI in isolated temporary workspaces.

## Dataset validation

A manifest is strict YAML; JSON is accepted because it is a YAML subset. Each case has a stable `id`, target `rule`, `language`, gold `label`, `difficulty`, optional `pair` metadata, optional `allow_activations` rule codes, and exactly one source form:

```yaml
source:
  filename: gen001/example.go
  file: fixtures/example.go.src
```

or:

```yaml
source:
  filename: gen001/example.go
  inline: |
    package example
```

Paths are relative to the manifest, sources are UTF-8 without NUL bytes, and the validator checks the embedded rule catalog and selectors. Complete datasets require a clean/violation pair for every catalog rule. Dev, test, and holdout manifests must be validated together so IDs and source hashes cannot overlap.

```sh
go run ./evals/cmd/jeff-eval validate \
  --dataset evals/datasets/dev/cases.yaml \
  --dataset evals/datasets/test/cases.yaml \
  --dataset evals/datasets/holdout/cases.yaml
```

The holdout split is frozen separately and is never used by the pull-request smoke job.

## Synthetic smoke

The smoke provider is local and deterministic. It forces a dummy credential, uses a loopback endpoint, verifies the exact state/model/question payload sent by Jeff, and never calls TypeSafe.

```sh
go run ./evals/cmd/jeff-eval run \
  --dataset evals/datasets/dev/cases.yaml \
  --profile smoke \
  --provider synthetic \
  --out /tmp/jeff-eval-smoke
```

The runner builds the real Jeff binary when `--jeff-bin` is omitted. It writes `results.json` and `summary.md`; outputs contain no source, raw provider payload, or credentials. Use an empty output directory for every run.

For a real run, the harness uses `TYPESAFE_API_KEY` when set and otherwise loads the key from the same system keyring as `jeff` (`jeff auth login`). It uses a metering proxy (the proxy forwards only `/v1/systemone`, adds no retries or redirects, and never logs the request body):

```sh
TYPESAFE_API_KEY=... go run ./evals/cmd/jeff-eval run \
  --dataset evals/datasets/dev/cases.yaml \
  --profile smoke --provider real --out /tmp/jeff-eval-real
```

The result records attempts, wall/provider latency, provider usage, reported cost, calculated cost, and an explicit `cost_status`; missing usage or pricing produces `cost_usd: null`, never zero. Use `--baseline PATH` only with an approved `results.json`; incompatible metadata exits `2` and a failed gate exits `1`. No command updates a baseline automatically.

`evals/config.yaml` configures the harness. The runner generates a separate `jeff.toml` inside each temporary workspace. `repetitions` defaults to `1`; real release profiles use `3`. A dirty checkout is exploratory only and must not produce an approved baseline.
