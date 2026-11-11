# Contributing to PaperBoy

PaperBoy is a small CLI that fetches RSS and Atom feeds and writes a weekly grouped Markdown list of their articles.
Bug reports, fixes and improvements are welcome.

## Reporting issues

Open a GitHub issue at <https://github.com/ivuorinen/paperboy/issues>. For a feed that renders wrongly, include the
feed URL and the lines of output it produced.

## Development setup

`go.mod` declares the language floor (its `go` line) and the toolchain CI builds with (its `toolchain` line). With the
default `GOTOOLCHAIN=auto`, any Go release from 1.21 on downloads that toolchain by itself.

```console
git clone https://github.com/ivuorinen/paperboy.git
cd paperboy
make build
cp examples/config.yaml examples/template.md .
./paperboy
```

`make build` stamps the version into the binary; pass `VERSION=1.2.3` to set it.

## Code style

- Format Go code with `gofmt`. CI fails on any file `gofmt -l .` lists.
- `make lint` runs `go vet` and `golangci-lint` (v2, installed separately). CI runs the same linters.
- Follow `.editorconfig`: tabs for Go and the Makefile, two spaces elsewhere, lines up to 160 columns.
  `editorconfig-checker` reports violations locally; CI does not run it.

## Testing

```console
make test                       # go test ./...
go test -run TestMdText ./...   # one test
```

The suite runs offline: feeds come from inline fixtures and `net/http/httptest` servers. A fix needs a test that
fails without it.

## Commit convention

Commits follow [Conventional Commits](https://www.conventionalcommits.org/): `type(scope): subject`, for example
`fix(feed): skip items without a publish date`. Mark an incompatible change to the config keys, the template format
or the output format with `!` and a `BREAKING CHANGE:` footer. Renovate opens the dependency updates.

## Pull requests

Open pull requests against `main`. Both jobs of the "Test Build and Run" workflow must pass:

- `test-build-and-run`: vet, gofmt, golangci-lint, tests, build, and a smoke run against the example feeds.
- `vulncheck`: govulncheck, failing on any vulnerability the code actually reaches.

## License

PaperBoy is MIT licensed (see [LICENSE.md](LICENSE.md)). Contributions are accepted under the same license.
