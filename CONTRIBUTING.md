# Contributing

Use the [issue forms](https://github.com/lmilojevicc/brewnicle/issues/new/choose) for bugs and feature requests. Discuss substantial changes before implementing them. For vulnerabilities, follow the [security policy](SECURITY.md) instead of opening a public issue.

## Development

See the [development guide](docs/development.md) for setup, tests, architecture, and isolated TUI testing. Keep changes focused and follow the existing Go conventions. Add regression tests for fixes and update documentation when behavior changes.

Before submitting code, format changed Go files with `gofmt` and run:

```sh
go test ./...
go test -race ./...
go vet ./...
```

Live integration tests are optional and use external services; see the development guide. For UI changes, check wide and narrow terminals and `NO_COLOR` using the isolated smoke-test instructions. Do not confirm real package installations during automated tests.

## Pull requests

- Explain the change and link related issues.
- List validation performed and anything not tested. Include a screenshot or recording for visible UI changes, with private details removed.
- Avoid unrelated refactoring, formatting, or dependency updates.
- Be respectful: discuss the code, avoid personal attacks, and do not share anyone's private information.

## License

Contributions are licensed under the project's [MIT license](LICENSE). Third-party code and assets retain their own licenses; preserve applicable notices.
