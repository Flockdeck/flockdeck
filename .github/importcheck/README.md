# Boundary checks

Files that keep the Flockdeck server and its web interface apart, for the layout the separation plan ends in:

    api/        the contract: catalog, schemas, fixtures
    ui/         the interface, its own Go module
    internal/   the server
    main.go     the one file that imports both

None of it is on yet. It is written for the layout after step 3, when the interface moves into `ui/`, and each piece is either harmless today or not named so that GitHub runs it.

| File | What it is | Runs today |
|---|---|---|
| `importcheck.go`, `importcheck_test.go` | The import check: Go in `ui/` must not import the server, the server must not import `ui/` (except `main.go` and `cmd/`), JavaScript in `ui/` must not import outside `ui/` (except `api/fixtures/`), `api/` holds no code. Its own Go module, so `go test ./...` at the root does not run it. | Only by hand. With no `ui/` directory it reports nothing. |
| `cmd/outside` | Reads changed paths and prints those a `ui:` pull request may not touch. | Only by hand. |
| `importcheck.yml.disabled` | The workflow: the import check, the "ui: PR touches only ui/ and api/fixtures/" rule, ESLint. | No: the name does not end in `.yml`. |
| `eslint.config.mjs` | The ESLint rule `no-restricted-imports` for `ui/src` and `ui/test`. | Only by the workflow, once enabled. |
| `CODEOWNERS.proposed` | Proposed owners, with api/ needing a person from each side. | No: GitHub reads only `.github/CODEOWNERS`. |

## Running the import check by hand

    cd .github/importcheck
    go test ./...

`TestThisRepository` runs the check on the real tree. Before step 3 it passes because there is no `ui/`.

## Enabling it, after step 3 has landed

1. Run `go test ./...` here. Fix what it finds.
2. Move `importcheck.yml.disabled` to `.github/workflows/importcheck.yml`.
3. Copy the lines of `CODEOWNERS.proposed` to `.github/CODEOWNERS` with real team names, and require code-owner review on `main`.
4. Check the paths in `eslint.config.mjs` and `importcheck.yml.disabled` against the real layout.
5. Make the "importcheck" jobs required checks.

The module path of the interface is assumed to be `github.com/jmwri/flockdeck/ui`. If step 3 names it otherwise, change `UIModule` in `importcheck.go`.
