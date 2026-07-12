# alive.name

[![CI](https://github.com/Yours-Full-Stop/alive.name/actions/workflows/ci.yml/badge.svg)](https://github.com/Yours-Full-Stop/alive.name/actions/workflows/ci.yml)

*your name, alive.*

An old name can stay buried in a git history for years: in commit metadata, in
messages, in the files themselves. **alive.name** finds every place it still
lives and helps you make the repository yours again: safely, on your own machine,
with a verified backup before anything is ever touched.

It is part of [Yours full stop](https://github.com/Yours-Full-Stop): free, open
tools that take the locks off the things that were always yours.

**alive.name never pushes and never commits for you.** It prepares, explains, and
hands you the exact commands. The last step is always yours.

---

## Install

You need [Go](https://go.dev/) 1.26 or newer and `git` on your `PATH`.

```bash
go install alive.name/cmd/alive@latest
```

Or build from a clone:

```bash
go build -o alive ./cmd/alive
```

The `reclaim` command (history rewriting) additionally needs
[git-filter-repo](https://github.com/newren/git-filter-repo):
`pip install git-filter-repo`. Nothing else requires it.

---

## The gentlest way in

Just run it. With no command, `alive` walks you through everything one calm step
at a time: find the old name, see where it lives, offer the safe fix, and only
if you choose it (and confirm twice) make a verified backup and rewrite history.

```bash
alive
```

Everything the walkthrough does, you can also do directly with the commands below.

---

## What it does

alive.name sorts every occurrence into three plain-language zones:

- **Only here, on your machine**: local history you can rewrite freely.
- **On a remote you control**: also on a remote you configured. Rewriting means
  a force-push, which is always yours to run.
- **Beyond your reach**: forks, other people's clones, and archives. These can't
  be reached programmatically, so the tool doesn't pretend to; it points you to
  GitHub's sensitive-data-removal process instead.

### `alive trace`: see where the old name lives (safe, read-only)

```bash
alive trace --old-name "Old Name" --old-email old@example.test --deep
```

Scans commit metadata, messages, annotated tags, and (with `--deep`) file
contents. Matching is case-insensitive by default (`--case-sensitive` to force
exact case). `--fetch` refreshes remote state first (a read, never a push).

### `alive mend`: the safe, reversible fix

```bash
alive mend --old-name "Old Name" --old-email old@example.test \
           --new-name "New Name" --new-email new@example.test
```

Writes a `.mailmap` so git tools display your new name everywhere. **History is
untouched.** It does not commit the file; that's yours to do.

### `alive reclaim`: remove the name from history (destructive, gated)

```bash
# Dry run by default, shows what would change, alters nothing:
alive reclaim --old-name "Old Name" --old-email old@example.test \
              --new-name "New Name" --new-email new@example.test

# To actually rewrite, opt in explicitly:
alive reclaim ... --apply --acknowledge-signed --acknowledge-pushed
```

Before it touches anything, `reclaim` makes a **verified backup** and refuses to
proceed without one. It refuses when tracked files have uncommitted changes
(commit or stash them first), warns about signed commits and already-published
history, and is a dry run unless you pass `--apply`. After a rewrite it does
**not** push; it prints the exact publish commands for you to run.

**Untracked files are left alone, on purpose.** A history rewrite only touches
committed history, so files git is not tracking (build output, editor folders
like `.vscode` or `.idea`, anything gitignored) cannot be affected and do not
block the rewrite. When any are present, `reclaim` lists them first so you can
see exactly what it is leaving untouched; it never deletes or modifies them.

**One thing to watch:** `reclaim` also replaces the old name inside **file
contents**, matched literally (not as a whole word). A short or common old name
can therefore land inside unrelated words: searching for `Sam` would rewrite the
`Sam` in `Sample`. `alive` warns you at input if an old name looks broad, and the
default dry run shows exactly what would change. **Read the dry run before you
`--apply`**, and if a name is short, pair it with an email or use a fuller
spelling to narrow it.

### `alive backup`: the backup lifecycle

```bash
alive backup create               # full copy + all-refs bundle, verified
alive backup list
alive backup restore <id> --destination ./restored
alive backup restore <id> --remote     # prints the push command; never runs it
alive backup rm <id>
alive backup gc --older-than 720h
```

By default a backup is a full recursive copy (including `.git`) plus an all-refs
bundle, stored **outside** your working tree and verified against the source. A
`.aliveignore` file at the repository root (using `.gitignore` syntax) trims bulk
from the copy, but `.git` is never excluded. Use `--bundle-only` for very large
repositories.

### `alive cleanup`: clear the name from your machine's caches

```bash
alive cleanup                 # detects your shell and OS
alive cleanup --shell bash    # force guidance for a specific shell
```

Typing the old name into a command (`alive trace --old-name …`) leaves it in your
**shell history** and a few other local caches. `cleanup` prints tailored,
copy-pasteable instructions for clearing each: shell history (PowerShell, bash,
zsh, fish, cmd), terminal scrollback, backups, post-rewrite git leftovers, and
`.mailmap`. It is honest about what it can't reach. It never edits anything for
you, and never asks for the name (it uses a placeholder), so the cleanup step
itself can't leak it.

---

## A note on your shell history

The **interactive `alive`** asks for the old name at a prompt, so it never enters
your shell history. The flag-based commands (`--old-name …`) *do* put it there;
those commands remind you, and `alive cleanup` tells you how to clear it. If
privacy matters to you, prefer the guided flow.

---

## Running in Docker (optional)

alive.name runs fine as a native binary; Docker is just an optional way to get
everything in one shot, including the Python-based `git-filter-repo` that
`reclaim` needs. You never have to use it.

**Two bind mounts are required.** They are not optional, because leaving either
out breaks the tool in a way that matters:

- `-v "/path/to/working/repo:/repo"`: the git repository to operate on.
- `-v "/path/to/backups:/backups"`: a **host** directory where backups are kept.

If you skip the `/backups` mount, any backup would be written *inside* the
container and lost the moment it is removed, which defeats the whole safety model.
So the container **refuses** any backup-creating command (`reclaim`, `backup`,
and the guided walkthrough) unless `/backups` is mounted.

Pull the prebuilt image (published to the GitHub Container Registry on each
release), or build it yourself:

```bash
docker pull ghcr.io/yours-full-stop/alive.name:latest   # prebuilt
# or
docker build -t alive .                                 # from source
```

The examples below use the local `alive` tag; substitute
`ghcr.io/yours-full-stop/alive.name:latest` if you pulled the prebuilt image.

Run it (Linux/macOS):

```bash
# See where the old name lives:
docker run --rm \
  -v "/path/to/working/repo:/repo" \
  -v "/path/to/backups:/backups" \
  alive trace --old-name "Old Name" --old-email old@example.test --deep

# The guided walkthrough (needs -it for the prompts):
docker run --rm -it \
  -v "/path/to/working/repo:/repo" \
  -v "/path/to/backups:/backups" \
  alive
```

On Windows PowerShell, use your own paths (backslashes for the host side):

```powershell
docker run --rm -it -v "C:\path\to\working\repo:/repo" -v "C:\path\to\backups:/backups" alive
```

Or use the provided `docker-compose.yml`, which refuses to run unless both paths
are set, so the required mounts can never be forgotten:

```bash
ALIVE_REPO="/path/to/working/repo" ALIVE_BACKUPS="/path/to/backups" docker compose run --rm alive
```

Notes:

- The image sets `ALIVE_STATE_DIR=/backups`, so backups go to the mount by
  default with no `--state-dir` needed.
- It still never pushes and never commits. `reclaim` prints the publish commands
  for you to run on the host.
- On Linux, files written into the backup mount are owned by the container's
  user (root). Pass `--user "$(id -u):$(id -g)"` if you want host ownership.

---

## Safety, by design

- **Backup before anything destructive.** No code path that alters a repository
  runs until a backup exists and has been verified (integrity check, plus a
  reference-list and HEAD match against the source).
- **No push, structurally.** The one package that runs git has no push capability
  and an allowlist that rejects `push`. Remote restore and post-rewrite publishing
  print commands for you to run.
- **No commits.** The tool never commits on your behalf.
- **Dry run by default** on the destructive path, with explicit confirmation to
  apply.

---

## Third-party libraries

- [`github.com/spf13/cobra`](https://github.com/spf13/cobra): CLI framework
  (Apache-2.0).
- [`github.com/sabhiram/go-gitignore`](https://github.com/sabhiram/go-gitignore):
  `.aliveignore` matching with `.gitignore` semantics (MIT). gitignore matching is
  not reimplemented; this well-maintained library is used for it.

---

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md), which also describes the testing split
(fast unit tests vs. `-tags integration` real-git tests) and the project's
non-negotiable safety invariants. Please read the boundary section there first.

---

## License

MIT. See [LICENSE](LICENSE).

---

Your name is yours. Your work is yours.

Yours. Full stop. 🩷
