# jeff

**Catch code issues before they catch you.**

A read-only Go CLI that semantically checks your files against your rules using [Jev](https://docs.typesafe.ai/introduction), locally or in CI.

## Usage

```sh
# Print the installed release version
jeff --version

# Check files in the current directory or at the provided paths
jeff check [--config PATH] [--output-format text|json] [--no-cache] [PATH...]

# Store or remove the TypeSafe credential
jeff auth login
jeff auth logout

# Install the latest compatible GitHub release over this released binary
jeff update
```

## Installation

### GitHub release (recommended)

Download the archive and `checksums.txt` from the [latest release](https://github.com/Alurith/jeff/releases). Choose `amd64` for Intel/AMD machines and `arm64` for Apple Silicon or Linux ARM.

On Linux, install the binary in `~/.local/bin`:

```sh
VERSION=0.1.0
ARCH=amd64
ASSET="jeff_${VERSION}_linux_${ARCH}.tar.gz"
BASE="https://github.com/Alurith/jeff/releases/download/v${VERSION}"

curl -fLO "$BASE/$ASSET"
curl -fLO "$BASE/checksums.txt"
grep "  ${ASSET}$" checksums.txt | sha256sum -c -

tmp=$(mktemp -d)
tar -xzf "$ASSET" -C "$tmp"
mkdir -p "$HOME/.local/bin"
install -m 0755 "$tmp/jeff" "$HOME/.local/bin/jeff"
rm -rf "$tmp"
```

On macOS use the matching `darwin` archive, replace `sha256sum -c -` with `shasum -a 256 -c -`, and install it in the same `~/.local/bin` directory. Ensure it is on `PATH`:

```sh
export PATH="$HOME/.local/bin:$PATH"
```

On Windows, use PowerShell to verify and install the matching ZIP in `$HOME\bin`, then add that directory to your user `PATH`:

```powershell
$version = "0.1.0"
$arch = "amd64"
$asset = "jeff_${version}_windows_${arch}.zip"
$base = "https://github.com/Alurith/jeff/releases/download/v$version"

Invoke-WebRequest "$base/$asset" -OutFile $asset
Invoke-WebRequest "$base/checksums.txt" -OutFile checksums.txt
$expected = ((Select-String -Path checksums.txt -Pattern ("  " + [regex]::Escape($asset) + "$" )).Line -split '\s+')[0].ToLowerInvariant()
if ((Get-FileHash $asset -Algorithm SHA256).Hash.ToLowerInvariant() -ne $expected) { throw "checksum mismatch" }

$tmp = Join-Path $env:TEMP "jeff-$version"
Remove-Item $tmp -Recurse -Force -ErrorAction Ignore
Expand-Archive $asset -DestinationPath $tmp
$bin = Join-Path $HOME "bin"
New-Item $bin -ItemType Directory -Force | Out-Null
Copy-Item (Join-Path $tmp "jeff.exe") (Join-Path $bin "jeff.exe") -Force
```

### From source

```sh
git clone https://github.com/Alurith/jeff.git
cd jeff
mkdir -p "$HOME/.local/bin"
go build -o "$HOME/.local/bin/jeff" ./cmd/jeff
```

Source builds report `dev` for `jeff --version` and intentionally cannot self-update.

## Updates and releases

`jeff update` is explicit: it selects the latest compatible GitHub Release, verifies its SHA-256 entry in `checksums.txt`, then replaces the installed release binary. The binary directory must be writable. It never updates a `dev`/source build.

To publish a release, first run the real eval workflow manually on `master` and review its test/holdout artifacts. Then push a semantic tag:

```sh
git switch master
git pull --ff-only
git tag -a v0.1.0 -m "v0.1.0"
git push origin v0.1.0
```

The tag runs quality checks and GoReleaser, which publishes checksummed archives for Linux, macOS, and Windows on `amd64` and `arm64`.

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
