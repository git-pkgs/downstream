# downstream

Tests a library against the projects that depend on it. `downstream` clones a set of dependents, runs their tests against the published version of the library to establish a baseline, replaces the dependency with a local checkout or branch, runs the tests again, and reports which dependents the change breaks.

The dependent set can be discovered automatically from the [ecosyste.ms](https://ecosyste.ms) package index or curated by hand in a `downstream.toml` file committed to the library's repository. Replacement is performed through the [managers](https://github.com/git-pkgs/managers) library, which maps the operation onto each package manager's own override mechanism.

Go modules are supported now; npm, Cargo, Bundler, uv and Composer are handled by the underlying replace operation and will be enabled in `downstream` in a later release.

## Install

```bash
go install github.com/git-pkgs/downstream@latest
```

## Commands

`downstream run` discovers dependents (or reads an existing `downstream.toml`), tests each against the patched library, and prints an aggregate report. Exit status is non-zero only when the patched run introduces failures that were not present in the baseline.

```bash
cd /path/to/your/library
downstream run --upstream-path . --limit 5
```

`downstream discover` queries the ecosyste.ms `dependent_packages` API for the most-used packages that depend on the given package, drops forks and archived or stale repositories, shallow-clones the survivors to score them on test count and on how many source files reference the package, and writes the ranked top N to `downstream.toml`. Re-running against an existing file keeps entries with `source = "manual"` and any per-dependent overrides; previously discovered entries are rescored and new candidates appended with a `(new)` marker.

```bash
downstream discover --limit 5
downstream discover --package github.com/spf13/cobra --no-analyze --stdout
```

`downstream test` runs the baseline/replace/retest loop against either a single dependent given on the command line or the set in `downstream.toml`.

```bash
downstream test --upstream-path . --dependent https://github.com/cli/cli
downstream test --upstream-path . --only cli/cli
```

`downstream list` reads `downstream.toml` back as plain rows, `--json`, or `--github-output` for use in an Actions matrix step.

### Common flags

| flag | |
| --- | --- |
| `--upstream` | module path of the library, optionally `module@ref`; defaults to `[package].name` from config or the module in `./go.mod` |
| `--upstream-path` | local path to the patched library; takes precedence over `@ref` |
| `--config`, `-c` | path to `downstream.toml` (default `./downstream.toml`) |
| `--only` | filter configured dependents by name, slug, glob or substring; repeatable |
| `--workdir` | directory for clones (default: temp dir) |
| `--keep` | retain the workdir after the run |
| `--timeout` | per-test-run timeout (default 30m) |
| `--limit`, `-n` | number of dependents to discover (default 5) |
| `--no-analyze` | skip the clone-and-score phase of `discover` |

## downstream.toml

```toml
[package]
name = "github.com/spf13/cobra"
ecosystem = "go"

# discover: 27 files reference upstream, 412 test files, 142019 dependent repos, 38447 stars
[[dependents]]
name = "github.com/cli/cli"
repo = "https://github.com/cli/cli"
source = "discover"
# ref  = "v2.40.0"             # pin to a tag if the default branch is unstable
# test = "go test ./pkg/..."   # override the auto-narrowed test command
# subdir = "."                 # for monorepos
# skip_baseline = true         # use the dependent's CI status instead of running tests twice

[[dependents]]
name = "github.com/gohugoio/hugo"
repo = "https://github.com/gohugoio/hugo"
source = "manual"              # kept regardless of discover ranking
```

The test command for each dependent defaults to `go test` over the packages whose imports reach the upstream module, computed via `go list -test -json ./...`. A `test` field overrides this.

## GitHub Actions

To run downstream tests as part of a library's CI, commit a `downstream.toml` and add a workflow that fans out one job per dependent:

```yaml
name: downstream

on:
  workflow_dispatch:
  schedule:
    - cron: "0 6 * * 1"
  pull_request:
    types: [labeled]

permissions:
  contents: read

concurrency:
  group: ${{ github.workflow }}-${{ github.ref }}
  cancel-in-progress: true

jobs:
  load:
    if: github.event_name != 'pull_request' || github.event.label.name == 'test-downstream'
    runs-on: ubuntu-latest
    outputs:
      dependents: ${{ steps.load.outputs.dependents }}
    steps:
      - uses: actions/checkout@v5
        with: {persist-credentials: false}
      - uses: actions/setup-go@v6
        with: {go-version: stable}
      - run: go install github.com/git-pkgs/downstream@latest
      - id: load
        run: downstream list --github-output >> "$GITHUB_OUTPUT"

  test:
    needs: load
    runs-on: ubuntu-latest
    strategy:
      fail-fast: false
      matrix:
        dependent: ${{ fromJson(needs.load.outputs.dependents) }}
    steps:
      - uses: actions/checkout@v5
        with: {path: upstream, persist-credentials: false}
      - uses: actions/setup-go@v6
        with: {go-version: stable}
      - run: go install github.com/git-pkgs/downstream@latest
      - env:
          DEP_REPO: ${{ matrix.dependent.repo }}
          DEP_TEST: ${{ matrix.dependent.test }}
        run: |
          downstream test \
            --upstream-path ./upstream \
            --dependent "$DEP_REPO" \
            --test "$DEP_TEST" \
            >> "$GITHUB_STEP_SUMMARY"
```

Triggering on a `test-downstream` label rather than every push keeps the cost manageable; the weekly schedule catches drift in the dependents themselves. A composite action wrapping these steps will be published separately.

## How replacement works

For Go, `downstream` runs `go mod edit -replace <module>=<path>` followed by `go mod tidy`, which redirects the module across the dependent's entire build including transitive consumers. Cargo `[patch.crates-io]` and uv `[tool.uv.sources]` behave the same way; the npm-family `file:` install is direct-only. See the [managers replace documentation](https://github.com/git-pkgs/managers/blob/main/docs/replace.md) for per-ecosystem behaviour and limitations.

## Result classification

| status | meaning |
| --- | --- |
| passed | baseline and patched runs both succeeded |
| failed | baseline succeeded, patched failed; the change introduced a regression |
| broken-baseline | baseline failed before any change was applied; reported but not held against the change |
| error | the dependent could not be set up (clone, replace, or manager detection failed) |

## Development

```bash
go test ./...
golangci-lint run ./...
```

Tests use fixtures under `internal/run/testdata` so the suite is hermetic.

## License

MIT. See [LICENSE](LICENSE).
