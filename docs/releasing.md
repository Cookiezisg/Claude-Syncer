# Releasing

```sh
git tag v0.2.0
git push origin v0.2.0
```

The [Release workflow](../.github/workflows/release.yml) runs the tests,
builds a universal (Apple Silicon + Intel) binary with the version baked in,
and publishes it with checksums as a GitHub Release with generated notes.

## How it reaches Homebrew

The formula lives in [Cookiezisg/homebrew-tap](https://github.com/Cookiezisg/homebrew-tap)
(`Formula/claude-syncer.rb`). That repository's own `bump` workflow checks
for a new Claude-Syncer release every six hours (or when run by hand from its
Actions tab), then updates the formula's version and source checksum. It uses
only the tap's own token, so this repository holds no credential for the tap.

Users then get it with `brew upgrade`. Installed agents pick up the new
binary on their next pass. Users who have not upgraded get one notification
within a day. The check reads the version in the published formula, so it
never announces a release before Homebrew can install it.

The formula builds from source, so no binary is downloaded through Homebrew
and nothing needs signing or notarizing. The template for a fresh formula is
[`packaging/homebrew/claude-syncer.rb.in`](../packaging/homebrew/claude-syncer.rb.in)
(`scripts/render-formula.sh VERSION SHA256`).

## Checking a formula change before releasing

```sh
brew tap-new --no-git syncer-test/local
scripts/render-formula.sh 0.0.0 "$(shasum -a 256 < some.tar.gz | cut -d' ' -f1)" \
  > "$(brew --repository)/Library/Taps/syncer-test/homebrew-local/Formula/claude-syncer.rb"
# point its url at file:///path/to/some.tar.gz, then:
brew install --build-from-source syncer-test/local/claude-syncer
brew test syncer-test/local/claude-syncer
brew audit --strict --formula syncer-test/local/claude-syncer
brew uninstall claude-syncer && brew untap syncer-test/local
```

## Compatibility with Claude Desktop

Claude-Syncer reads a private, undocumented format. After a Claude Desktop
update, run `claude-syncer sync --dry-run` and check that the plan makes
sense, and that `claude-syncer status` reports no records left alone. If the
format changed, update [desktop-internals.md](desktop-internals.md) with what
was verified, together with the code.
