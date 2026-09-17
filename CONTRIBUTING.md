# Contributing

## Building and testing

```bash
go tool task ci     # vet, lint, test, build, helm — everything CI runs
go tool task build   # just the binary
go tool task test    # go test -race ./...
go tool task lint    # golangci-lint
go tool task helm    # lint + render the chart, round-trip rendered config through the strict loader
```

`Task` is a go.mod tool dependency, invoked via `go tool task` — no separate install needed beyond Go itself. golangci-lint is expected on `PATH`.

## Commit messages

This repo uses [Conventional Commits](https://www.conventionalcommits.org/) — not a style preference, the release pipeline depends on it. [`svu`](https://github.com/caarlos0/svu) derives the next version directly from commit types:

- `fix:` → patch release.
- `feat:` → minor release.
- `feat!:`, `fix!:`, or a `BREAKING CHANGE:` footer → would normally be a major bump, but is capped at minor while the project is pre-1.0 (`go tool task release` passes `--v0`). [AGENTS.md](AGENTS.md#versioning) explains the mapping and what leaving beta actually involves.

A commit that doesn't follow the convention doesn't break anything locally, but it does mean `svu` can't classify it correctly when a release is cut — please use the prefixes above.

## Making changes

1. `docs/content/design.md` states the invariants the engine holds to, organized by the parts it operates on. Read it before touching engine internals. A change that contradicts one of those invariants needs the page updated in the same PR: edit the claim in place rather than appending a paragraph next to it. If the new invariant can't be stated in a sentence or two, that's a sign the change wants discussion before code.
2. Config changes (`internal/config`): keep parsing strict (`yaml.KnownFields(true)`) and validate at load time — `meridian validate` should catch every possible startup failure without needing credentials.
3. Engine changes (`internal/sync`): the five hardening guards (zombie-resurrection, mass-delete, hash change-detection, windowed orphan-GC, tombstone tolerance) are correctness requirements, not nice-to-haves — a change touching reconciliation should come with a test for whichever guard it interacts with.
4. Helm chart changes (`deploy/meridian`): `go tool task helm` lints and renders the chart in both config modes (inline `config.*` and `existingConfigMap`) and round-trips the rendered `rules.yaml` through the real config loader — treat a helm-task failure the same as a Go test failure.

## Opening a pull request

- Keep PRs focused — one logical change per PR is easier to review and easier for `svu` to classify.
- Add or update tests for anything behavioral.
- If your change affects `rules.yaml` schema, the CLI, metrics, or deployment, update the corresponding page under `docs/` in the same PR — docs and code should never drift apart.
- Docs prose has a house style, written down under [Documentation voice](AGENTS.md#documentation-voice). It's four bullets long, and the point of all four is that the documentation should stay pleasant for a human to read.

## License

By contributing, you agree your contributions are licensed under this project's [MIT license](LICENSE).
