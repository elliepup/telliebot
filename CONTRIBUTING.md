# Contributing

## Branches

Never commit directly to `main`. Do all work on a branch named `<type>/<short-description>`
(for example `feat/reroll-button` or `fix/anilist-timeout`) and merge it through a pull request.

## Checks

CI ([`.github/workflows/ci.yml`](.github/workflows/ci.yml)) runs on every pull request and push
to `main`. Run the same checks locally before pushing:

```sh
gofmt -l .               # must print nothing
go mod tidy -diff        # must print nothing
go vet ./...
go test -race ./...
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
docker build .
```

`govulncheck` also runs weekly on its own, to catch new advisories against unchanged code.
Dependabot opens weekly PRs for Go modules and the Docker base images, and monthly ones for
GitHub Actions.

## Commit Message Guidelines

Every commit tells a story. A good commit message explains why a change was made, makes
regressions easier to trace, and helps produce a meaningful changelog.

### Format

```
<type>(<scope>): <summary>

[body]

[footer]
```

- Every commit needs a **header** in the format described below.
- Every commit needs a **body** except `docs` commits. Write it in the imperative mood and make it at
  least 20 characters long.
- The **footer** is optional. Use it to reference issues or document breaking changes.

### Header

```
<type>(<scope>): <summary>
  │       │             │
  │       │             └─ imperative mood, lowercase, no period at the end
  │       │
  │       └─ Scope (optional): bot|mood|anilist|tmdb|pick|config|storage|docker|deps
  │
  └─ Type: feat|fix|db|perf|refactor|docs|test|build|ci|chore|security|revert
```

#### Type

| Type       | When to use                                                                     |
|------------|---------------------------------------------------------------------------------|
| `feat`     | Introduces a new feature or user-visible capability                              |
| `fix`      | Patches a bug in existing behavior                                               |
| `db`       | Changes the database schema, migrations, or seed data                            |
| `perf`     | Improves performance without changing observable behavior                        |
| `refactor` | Restructures code without fixing bugs or adding features                         |
| `docs`     | Documentation changes only                                                       |
| `test`     | Adds or corrects tests without touching production code                          |
| `build`    | Changes the build system, tooling, or dependencies (Dockerfile, go.mod)          |
| `ci`       | CI/CD pipeline changes (GitHub Actions workflows and scripts)                    |
| `chore`    | Routine maintenance that does not fit any other category                         |
| `security` | Fixes a security vulnerability. Use it instead of `fix` so security patches stand out in history |
| `revert`   | Reverts a previous commit. The body must reference the reverted SHA and explain why |

If a change spans multiple types, split it into separate commits. If that is not practical, use
the type that best represents the main intent.

#### Scope

Scope is optional but strongly encouraged. Use the single most relevant scope.

| Scope     | What it covers                                                         |
|-----------|------------------------------------------------------------------------|
| `bot`     | Discord session, slash commands, interactions, embeds, buttons         |
| `mood`    | Mood definitions and their genre mappings                              |
| `anilist` | AniList client (anime)                                                 |
| `tmdb`    | TMDB client (movies and shows)                                         |
| `pick`    | Shared picking logic and result types                                  |
| `config`  | Environment and configuration loading                                  |
| `storage` | Persistence: watch history, seen lists                                 |
| `docker`  | Dockerfile and Compose files                                           |
| `deps`    | Dependency upgrades (pair with `build` or `chore`)                     |

#### Summary

- Use the imperative mood: "add", "fix", "remove" (not "added" or "adds")
- Start with a lowercase letter
- No period at the end
- Keep it under 72 characters
- Describe what the commit does. The reason why belongs in the body

### Body

- Separate it from the header with a blank line
- Use the imperative mood, consistent with the summary
- Wrap lines at about 100 characters
- Explain why the change exists, not what changed (the diff already shows that)

### Footer

Link issues with GitHub closing keywords (`Closes #12`, `Fixes #34`), or use `Ref #56` for a
related issue that should stay open.

For breaking changes, start the footer with `BREAKING CHANGE:` and a short summary, then a blank
line and migration steps:

```
BREAKING CHANGE: rename BOT_TOKEN env var to DISCORD_TOKEN

Update your .env to use DISCORD_TOKEN instead. The bot will not start without it.
```

### Examples

```
feat(bot): add reroll button to pick embeds

Picking again used to mean retyping the whole command. The button keeps the
same mood and type, so you can cycle through options with one click.

Closes #3
```

```
fix(anilist): retry once when the API returns 429

AniList's rate limit drops to 30 req/min under load, so a fast series of
rerolls could fail outright. A single short retry covers the common case.
```

```
docs: add Discord app setup steps to README
```

```
build(docker): switch final stage to distroless static image
```

```
revert: revert "feat(tmdb): filter shows by streaming provider"

Reverts commit a1b2c3d.

Provider data is missing for most regions outside the US, which left most
picks empty. Reverting until there is a fallback.
```
