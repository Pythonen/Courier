# Releasing Courier

Courier releases are built by GoReleaser when a semantic-version tag beginning
with `v` is pushed. A release contains macOS, Linux, and Windows archives for
AMD64 and ARM64, a SHA-256 checksum file, and an automatically generated
Homebrew cask.

## One-time Homebrew setup

Before the first release:

1. Create a public repository named `Pythonen/homebrew-tap` and initialize its
   default branch.
2. Create a fine-grained GitHub personal access token that can write repository
   contents in `Pythonen/homebrew-tap`.
3. Add that token to the Courier repository as an Actions secret named
   `HOMEBREW_TAP_TOKEN`.
4. If the tap protects its default branch, allow the token to push cask updates
   or adjust the publication strategy before releasing.

The workflow's built-in `GITHUB_TOKEN` creates the release in Courier. The
separate token is required because GitHub's workflow token cannot write to a
different repository. The workflow checks for this secret before building so a
missing credential cannot leave a partially published release.

Courier does not currently have an Apple Developer signing identity. The
generated cask therefore removes the quarantine attribute from the installed
binary on macOS, following GoReleaser's documented fallback for unsigned
binaries. Replace that hook with signing and notarization when credentials are
available.

## Publish a release

Start from an up-to-date, green `main` branch, choose the next semantic version,
then create and push an annotated tag:

```bash
git switch main
git pull --ff-only origin main
go test ./...
git tag -a v0.1.0 -m "Courier v0.1.0"
git push origin v0.1.0
```

The release workflow tests the tagged commit before publishing anything. Stable
tags such as `v0.1.0` update the Homebrew tap. Prerelease tags such as
`v0.2.0-rc.1` create a GitHub prerelease but do not update the tap.

After the first stable release, users can install or upgrade Courier with:

```bash
brew install --cask Pythonen/tap/courier
brew upgrade --cask courier
```

## Validate locally

With GoReleaser installed, validate the configuration and build release-shaped
artifacts without publishing them:

```bash
goreleaser check
goreleaser release --snapshot --clean
./dist/courier_darwin_arm64_v8.0/courier --version
```

Snapshot output directories are implementation details and may vary between
GoReleaser versions; `dist/artifacts.json` is the authoritative artifact index.
