# Releasing Courier

Courier releases are built by GoReleaser when a semantic-version tag beginning
with `v` is pushed. A release contains macOS, Linux, and Windows archives for
AMD64 and ARM64, a SHA-256 checksum file, and an automatically generated
Homebrew cask.

The release workflow requires the `HOMEBREW_TAP_TOKEN` Actions secret so it can
publish cask updates to `Pythonen/homebrew-tap`.

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
