# jeff

**Catch code issues before they catch you.**

A read-only Go CLI that semantically checks your files against your rules using [Jev](https://docs.typesafe.ai/introduction), locally or in CI.

## Usage

```sh
# Check files in the current directory or at the provided paths
jeff check [--config PATH] [--output-format text|json] [--no-cache] [PATH...]

# Store or remove the TypeSafe credential
jeff auth login
jeff auth logout
```

## Authentication and configuration

Set `TYPESAFE_API_KEY` or store a credential with `jeff auth login`. Stored credentials use the operating system's keyring; `jeff auth logout` removes the saved credential.

Use `TYPESAFE_BASE_URL` to point to a compatible HTTPS endpoint during testing. Plain HTTP is accepted only for localhost/loopback endpoints.

## Project configuration

Jeff reads `jeff.toml` from the invocation directory. Use `--config PATH` to select another file. Paths are relative to the project root:

```toml
src = ["src", "lib"]
exclude = ["docs", "generated/**"]
include = ["scripts/**/*.go"]
rule-files = ["rules/*.yml"]
cache-dir = ".cache/jeff"
output-format = "json"
jev-version = "jev-1.13.0"
```

`src` limits directory discovery when no paths are passed on the command line. `include` adds matching files outside those directories; `exclude` wins over it. Discovery patterns are positive globs (`*`, `?`, character classes, and `**`); negated `!` patterns are rejected. General exclusions (for example `.git`, `vendor`, `node_modules`, build and cache directories) are always active, and `.gitignore` is still respected. A path passed explicitly on the command line keeps discovery behavior, but the source-file safety filter still applies.

`rule-files` loads external YAML rule fragments using the same schema as the embedded catalog. `JEFF_CACHE_DIR` overrides `cache-dir`; `--output-format` overrides the TOML value. The default output is `text` and the default cache is `.jeff-cache`.

For privacy, rules apply by default only to recognized source files. Certificates and keys (`.pem`, `.key`, `.crt`), `.env`, YAML/YML, JSON, TOML, TFVars, Markdown/TXT, hidden paths, and default-excluded directories are not sent. An external YAML rule can explicitly set `allow-non-source: true` when it intentionally needs to inspect a non-source file.

## Current rules

| Code | Rule | Description |
| --- | --- | --- |
| `GEN001` | Failure-path integrity | Finds failure paths that can leave state, output, or contracts semantically inconsistent. |
| `GEN002` | Side-effect transparency | Finds relevant side effects that are not reasonably signaled by the surrounding interface. |
| `GEN003` | Within-file semantic duplication | Finds domain policies, decisions, or invariants duplicated within a file with divergence risk. |
| `GEN004` | Concrete naming/behavior mismatch | Finds names, signatures, or interfaces that concretely contradict observable behavior. |

## CLI and CI output

Jeff is designed for both interactive CLI use and automation:

- text output highlights failed checks and prints a summary;
- `--output-format json` emits structured results and errors for CI;
- `0` means all applicable checks passed;
- `1` means a conclusive check found a violation and no error occurred;
- `2` means a usage, configuration, input, provider, internal, or inconclusive result.
