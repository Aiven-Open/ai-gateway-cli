# Contributing

Contributions are welcome. Open an issue first to discuss a larger change.

## Development

The [README](README.md#development) lists the checks to run before opening a
pull request. Add or update tests with every change in behaviour.

## Commits

Each commit is one logical change that builds and passes the checks on its
own, so that it can be reviewed and reverted alone.

- The subject is `component: summary`, where the component is the package or
  area changed (`cli`, `codex`, `npm`, `ci`). Write it in the imperative, lower
  case after the colon, without a full stop or issue key. Aim for 50
  characters and never exceed 72.
- The body says why the change is needed, the alternative you rejected if
  there was one, and any work still needed. It does not explain the code.
  Keep it to about 120 words and wrap it at 72 columns. A change that needs
  no explanation has no body.
- A GitHub issue, when there is one, goes on its own line at the end of the
  body, as `#123`, with nothing else on that line.
- A message must make sense to someone reading `git blame` years later,
  without the pull request or issue: leave out review rounds, test runs,
  people's names and other notes on how the change was made.
- History shows the final design. Fold review fixes into the commit they fix
  rather than adding fixup commits, and do not add code that a later commit in
  the same pull request replaces. If a commit cannot build alone, add a small
  enabling commit before it rather than temporary code.

## Pull requests

The title says what the pull request changes and carries no issue key. The
description says why. Pull requests are merged with a merge commit, so the
commits stay as written.

## Security

Report vulnerabilities as described in [SECURITY.md](SECURITY.md), not in a
public issue.

## Releases

Maintainers release as described in [RELEASING.md](RELEASING.md).
