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

Courier uses golden snapshot tests for TUI rendering in `view_golden_test.go`.
The expected outputs are stored in `testdata/*.golden`.

### When to update snapshots

Update snapshots only when UI output changes intentionally (layout, spacing, labels, borders, etc.).

### Required steps after intentional UI changes

1. Regenerate snapshots:

```bash
go test ./... -update
```

2. Re-run tests without update mode:

```bash
go test ./...
```

3. Review and commit both code and updated `testdata/*.golden` files.

### If golden tests fail unexpectedly

- Do not immediately run `-update`.
- First inspect the diff and confirm the change is expected.
- If the output drift is unintended, fix the bug and keep existing golden files.

## CI

GitHub Actions runs tests and lint on pull requests and pushes to `main`.
Your PR should be green before merge.
