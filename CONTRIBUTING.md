# Contributing

Thanks for contributing to Courier.

## Development workflow

- Run all tests before opening a PR:

```bash
go test ./...
```

- Run lint locally when possible:

```bash
golangci-lint run
```

## Golden snapshot tests

Courier uses golden snapshot tests for TUI rendering in
`internal/tui/view_golden_test.go`. The expected outputs are stored in
`internal/tui/testdata/view_*.golden`.

### When to update snapshots

Update snapshots only when UI output changes intentionally (layout, spacing, labels, borders, etc.).

### Required steps after intentional UI changes

1. Regenerate snapshots:

```bash
go test ./internal/tui -run TestViewGolden -update
```

2. Review the snapshot diff before proceeding:

```bash
git diff -- internal/tui/testdata
```

3. Re-run the golden test without update mode, then run the full suite:

```bash
go test ./internal/tui -run TestViewGolden
go test ./...
```

4. Commit both the intentional UI change and the updated
   `internal/tui/testdata/view_*.golden` files.

### If golden tests fail unexpectedly

- Do not immediately run `-update`.
- First inspect the diff and confirm the change is expected.
- If the output drift is unintended, fix the bug and keep existing golden files.

## CI

GitHub Actions runs tests and lint on pull requests and pushes to `main`.
Your PR should be green before merge.

Release maintainer setup and tagging instructions are in
[`docs/RELEASING.md`](./docs/RELEASING.md).
