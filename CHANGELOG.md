# Changelog

All notable changes to alive.name are recorded here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project aims to
follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.1] - 2026-07-13

### Changed

- `alive reclaim`'s working-tree gate now refuses only on uncommitted changes to
  **tracked** files. A history rewrite cannot touch untracked or ignored files
  (build output, editor folders like `.vscode` or `.idea`, anything gitignored),
  so their presence no longer blocks a rewrite. When any are present, `reclaim`
  lists them first so you see exactly what is being left untouched, and the
  refusal message now reads "commit or stash them first", which is accurate for
  tracked changes.

### Fixed

- The Docker image sets `core.autocrlf=input` so a repository checked out on
  Windows (CRLF) and mounted into the Linux container is no longer misread as a
  dirty working tree. Previously this made `reclaim` refuse on a repository that
  was actually clean.

## [0.1.0] - 2026-07-10

The first pre-release, cut so testers have a stable reference point rather than a
moving `main`. The core flows are complete; the version stays below 1.0 while the
tool is tested on real repositories.

### Added

- `alive trace`: read-only scan for an old name across commit metadata, messages,
  annotated tags, and (with `--deep`) file contents, rendered as a calm
  three-zone report (local, controlled-remote, and the unreachable narrative).
- `alive mend`: writes a `.mailmap` so git tools display the new name. History is
  untouched; it does not commit the file.
- `alive reclaim`: the gated history rewrite via `git filter-repo`. Dry run by
  default, requires a verified backup, refuses a dirty working tree, and warns on
  signed commits and already-published history. Never pushes; prints the publish
  commands for you to run.
- `alive backup` (`create`, `list`, `restore`, `rm`, `gc`): verified backups
  stored outside the working tree, with `.aliveignore` support, an all-refs
  bundle, and an optional remote mirror. Remote restore prints a command rather
  than pushing.
- `alive cleanup`: advisory guidance for clearing the old name from shell history,
  terminal scrollback, backups, and other local caches. It never edits anything
  and never handles the name itself.
- Bare `alive` and `alive guide`: the guided, held-by-the-hand walkthrough.
- Optional Docker image bundling `git` and `git-filter-repo`, with required
  `/repo` and `/backups` bind mounts enforced so backups are never ephemeral.
- `ALIVE_STATE_DIR` environment variable to set the default backup location.

### Safety

- The tool never pushes and never commits on your behalf; it prepares and prints
  the exact commands, and you run them.
- The no-push guarantee is structural: the one package that runs git has no push
  capability, and its subcommand allowlist rejects `push`.

[Unreleased]: https://github.com/Yours-Full-Stop/alive.name/compare/v0.1.1...HEAD
[0.1.1]: https://github.com/Yours-Full-Stop/alive.name/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/Yours-Full-Stop/alive.name/releases/tag/v0.1.0
