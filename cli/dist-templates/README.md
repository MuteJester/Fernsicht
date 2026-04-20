# Distribution templates

These files are rendered per release by
[`.github/workflows/cli-release.yml`](../../.github/workflows/cli-release.yml)
and uploaded as release assets. Maintainers then copy each rendered
file into the appropriate package-manager tap repo.

## Files

| Template | Renders to | Goes into |
|---|---|---|
| `fernsicht.rb.tmpl` | `fernsicht.rb` | `MuteJester/homebrew-fernsicht` |
| `fernsicht.json.tmpl` | `fernsicht.json` | `MuteJester/scoop-fernsicht` |

## Placeholders

`sed` substitutions performed at render time:

| Token | Example | Source |
|---|---|---|
| `__VERSION__` | `0.1.0` | tag (without `cli/v` prefix) |
| `__TAG__` | `cli/v0.1.0` | full GitHub tag |
| `__SHA_DARWIN_AMD64__` | hex digest | `SHA256SUMS` |
| `__SHA_DARWIN_ARM64__` | hex digest | `SHA256SUMS` |
| `__SHA_LINUX_AMD64__` | hex digest | `SHA256SUMS` |
| `__SHA_LINUX_ARM64__` | hex digest | `SHA256SUMS` |
| `__SHA_WINDOWS_AMD64__` | hex digest | `SHA256SUMS` |

**Don't** put any of these literal strings inside template comments —
sed will substitute them too. If you need to mention a placeholder
in documentation, put it in this README rather than the templates.

## Tap setup (one-time, done by maintainer)

### Homebrew

1. Create a public GitHub repo named `homebrew-fernsicht` under the
   `MuteJester` org.
2. Add a `Formula/` directory.
3. After the first release, download `fernsicht.rb` from the release
   assets and commit it as `Formula/fernsicht.rb`.
4. Verify with `brew tap MuteJester/fernsicht && brew install
   MuteJester/fernsicht/fernsicht`.

### Scoop

1. Create a public GitHub repo named `scoop-fernsicht` under the
   `MuteJester` org.
2. Place `fernsicht.json` at the repo root.
3. Verify with `scoop bucket add fernsicht
   https://github.com/MuteJester/scoop-fernsicht && scoop install
   fernsicht`.

## Future polish

A follow-up workflow can auto-PR the rendered files into the tap
repos after each release, so the manual copy step disappears. This
needs a personal-access-token with cross-repo write scope; deferred
until the tap repos exist and the manual flow is verified once.
