# jeff

**Catch code issues before they catch you.**

A read-only Go CLI that semantically checks your files against your rules using [Jev](https://docs.typesafe.ai/introduction), locally or in CI.

## Usage

```sh
# Check files in the current directory or at the provided paths
jeff check [--output-format text|json] [--no-cache] [PATH...]

# Store or remove the TypeSafe credential
jeff auth login
jeff auth logout
```

## Authentication and configuration

Set `TYPESAFE_API_KEY` or store a credential with `jeff auth login`. Stored credentials use the operating system's keyring; `jeff auth logout` removes the saved credential.

Use `TYPESAFE_BASE_URL` to point to a compatible endpoint during testing.

## Current rules

| Code | Rule | Description |
| --- | --- | --- |
| `GEN001` | Unclear responsibility | Finds files that mix unrelated responsibilities. |
| `GEN002` | Misleading naming | Finds important names that do not match their behavior or purpose. |
| `GEN003` | Excessive responsibility | Finds components responsible for too many distinct concerns. |
| `GEN004` | Low cohesion | Finds unrelated concepts, data, or dependencies grouped together. |
| `GEN005` | Hidden side effects | Finds significant side effects that are not apparent from the API. |
| `GEN006` | Weak error handling | Finds errors that may be hidden, ignored, or handled unsafely. |
| `GEN007` | Poor error context | Finds errors that lack useful diagnostic context. |
| `GEN008` | Missing input validation | Finds external or untrusted input used without adequate validation. |
| `GEN009` | Implicit assumptions | Finds important assumptions that are neither enforced nor documented. |
| `GEN010` | Unnecessary complexity | Finds implementations that are more complex than necessary. |
| `GEN011` | Premature abstraction | Finds abstractions that add complexity without a clear benefit. |
| `GEN012` | Inappropriate coupling | Finds unnecessary coupling between distinct components or concerns. |
| `GEN013` | Abstraction leak | Finds abstractions that expose implementation details to callers. |
| `GEN014` | Duplicated domain knowledge | Finds the same business rule or domain knowledge represented in multiple places. |
| `GEN015` | Redundant comments | Finds comments that restate code without adding useful information. |
| `GEN016` | Missing rationale | Finds non-obvious behavior without an explanation of why it exists. |
| `GEN017` | Fragile control flow | Finds control flow that is unnecessarily difficult to reason about. |
| `GEN018` | Invalid state representable | Finds designs that make invalid or contradictory state easy to represent. |
| `GEN019` | Poor boundary separation | Finds core logic mixed with infrastructure or external-system concerns. |
| `GEN020` | Difficult to test | Finds structures that make important behavior unnecessarily difficult to test. |

## CLI and CI output

Jeff is designed for both interactive CLI use and automation:

- text output highlights failed checks and prints a summary;
- `--output-format json` emits structured results and errors for CI;
- `0` means all applicable checks passed;
- `1` means a conclusive check found a violation and no error occurred;
- `2` means a usage, configuration, input, provider, internal, or inconclusive result.

Jev reference documentation is available in [`docs/references/jev`](./docs/references/jev).
