# Contributing to alive.name

*your name, alive.*

alive.name finds an old name buried in a git history and helps you make it yours again: safely, on your own machine, with a verified backup before anything is ever touched. It is part of [Yours full stop](https://github.com/Yours-Fullstop): free, open tools that take the locks off the things that were always yours.

Thank you for being here. This document covers two things: how to help build it, and one boundary that keeps the project alive for the long run. The boundary comes first, because it matters most.

---

## A boundary, stated with care

People often find a tool like this on a hard day. If that is you: you are welcome here, your name is yours, and building this tool for you is the whole point.

But please read this gently. **alive.name is software, not a support service, and its maintainers are not a crisis line.** The issue tracker is for the tool: bugs, features, questions about how it works. It is not a place where someone will be on the other end for a personal emergency, and it would not be fair to you to pretend otherwise.

If you are in distress and need a person, please reach one. Talk to someone you trust, or contact a support service in your area.

> Maintainers: replace this line with the specific, vetted resources you want to point people to (for example a trans-focused peer-support line and a general crisis line for your region). Keep it short and real; do not let it rot. If you would rather not host resource links, say so plainly and point to a maintained directory instead.

None of this is a lack of care. It is the opposite. A maintainer who tries to be everyone's support line burns out, and then the tool dies and helps no one. Holding this boundary is how the work survives to keep helping. If you open an issue that is really a personal crisis, expect a kind pointer back to this section rather than silence, and please do not read that as rejection.

**For maintainers:** decide your own limits before the first issue lands, and write them down. What will you field, what will you not, how fast do you reply, when do you step back. Your wellbeing is load-bearing infrastructure for this project. Protect it on purpose.

---

## Who this is for

This project is built by and for people the systems tend to neglect: trans and nonbinary people, disabled and neurodivergent people, anyone whose own history was made hard to hold. Nothing about us without us. Contributions, lived experience, and design feedback from the people this tool serves are the most valuable thing you can bring.

Be kind. Assume good faith. Harassment, deadnaming, gatekeeping, or cruelty of any kind end your welcome here. [Link your Code of Conduct once it exists.]

---

## Ways to contribute

- **Report a bug** or **request a feature** in the issue tracker. Clear steps and what you expected help enormously.
- **Improve the docs.** Plain, kind, accurate wording is a real contribution, especially for the parts a non-engineer reads.
- **Send a pull request.** Fork, branch, and open a PR against `main`. Small and focused is easier to review than large and sweeping.
- **Test on real repositories** and tell us where it was confusing or scary. The tool is used at vulnerable moments; friction is a bug.
- **Share resource links** for the boundary section above, if you know good, current ones.

You are of course free to commit and push to your own fork however you like. The rules below are about what the *tool* is allowed to do, not how you work.

---

## The non-negotiables

These are design invariants. A contribution that breaks one will not be merged, however good it otherwise is.

1. **The tool never pushes, and never commits on the user's behalf.** No pull request may add a code path that runs `git push` or `git commit` for the user. Publishing and committing are always the user's own deliberate act. The tool prepares, explains, and hands back the exact commands; the human runs them.
2. **The no-push guarantee is structural.** The `gitclient` package has no push capability and must never gain one. The subcommand allowlist must never include `push`. There is a test that asserts this; do not weaken it.
3. **Backup before anything destructive.** No code path that alters a repository may run before a verified backup exists. Do not add a flag or shortcut that skips or weakens verification.
4. **The safety gates stay.** Dry-run by default on the destructive path, explicit confirmation, the dirty-tree refusal, the signed-commit and pushed-history warnings. Convenience never earns their removal.
5. **The tool must be accessible.** A tool that liberates identity has to be usable by the people it is for, including screen-reader users. Accessibility regressions are bugs.

---

## Code standards

- **Go**, formatted with `gofmt`, clean under `go vet`, and passing `go test -race`.
- **Verbose, descriptive names over abbreviations.** Name things for what they are: `candidateCommit`, not `c`; `matchedField`, not `m`. The only accepted short forms are the idiomatic `err`, `ctx`, and a plain `index` in a trivial numeric loop.
- **Every error is handled and wrapped with context.** Never discard a return with `_ =`. Never swallow an error silently.
- **Structured logging** (`log/slog`), never `fmt.Println`, except for intentional user-facing CLI output.
- **No shared mutable global state.** If shared state is genuinely needed, guard it with a mutex and prove it under `-race`.

---

## Building and running it locally

You will need:

- **Go 1.26+**.
- **git** on your `PATH`: the tool shells out to it, and the integration tests use it.
- **A C compiler**, only if you want to run the race detector. `go test -race`
  requires cgo. On Windows, install one (for example `scoop install gcc`) and
  point Go at it once:
  ```bash
  go env -w CGO_ENABLED=1
  go env -w CC="C:/Users/<you>/scoop/apps/gcc/current/bin/gcc.exe"   # adjust the path
  ```
  Check it with `go env CGO_ENABLED CC`.
- **Python + git-filter-repo**, only to actually *run* `alive reclaim` (the
  destructive rewrite). Not needed to build the project or run its tests.

Running the checks:

```bash
go test ./...                            # unit suite: fast, the everyday command
go test -tags integration ./...          # integration suite: real git + filesystem
go test -race ./...                      # unit, race detector
go test -tags integration -race ./...    # integration, race detector
gofmt -l internal                        # formatting check (prints nothing when clean)
go vet ./...  &&  go vet -tags integration ./...
```

---

## Unit tests and integration tests

Tests are split into two suites with a **hard, unambiguous boundary**. Please keep
new tests on the right side of it.

**Unit tests** live in `*_test.go` with no build tag. They are pure logic and
fakes only, and must touch **no real external resource: not `git`, and not the
filesystem** (no `t.TempDir()`, no `os` file operations). They are deterministic
and fast, and they are what `go test ./...` runs by default. The packages expose
seams so this is always possible:

| Package      | Seam for faking the real world                                            |
|--------------|---------------------------------------------------------------------------|
| `gitclient`  | `CommandRunner` interface: inject canned git output                      |
| `trace`      | `repositoryReader` interface: inject in-memory commits, tags, blobs      |
| `backup`     | `sourceRepository` / `repositoryVerifier` interfaces + a verifier factory |
| `classifier` | `RemoteReachabilitySource` interface                                      |

Construct objects directly to avoid incidental I/O, for example build a client
as `&GitClient{repositoryPath: "x", runner: fake}` rather than through the real
constructor, which stats the path.

**Integration tests** live in `*_integration_test.go` and begin with
`//go:build integration`. They cover anything that touches a real resource: the
real `git` binary, the `fixturerepo` builder, **or the real filesystem** (temp
dirs, file copies, reads). A test that fakes git but still copies real files
(such as `backup.createVerified`) is an integration test and belongs here. They
are opt-in via `-tags integration`, and real-git tests should `Skip` (not fail)
when `git` is absent.

This split exists because conflating the two made every "unit" test spawn real
git processes: slow, non-deterministic, and exposed to antivirus scanning of
throwaway `.git` directories. Keep them apart and the default suite stays quick.

---

## Tests are required, not optional

Every unit ships with tests covering all four categories:

- **Happy path:** correct input, expected result.
- **Negative path:** valid but unhappy input (no matches found, empty repository).
- **Edge cases:** boundaries (empty strings, unicode names, huge histories, detached HEAD, shallow clones, symlinks).
- **Error handling:** git missing, permission denied, corrupt repository, verification failure.

Coverage floor is **80%**, aiming close to **100%**, with pragmatism allowed on trivial plumbing. The safety-critical paths (backup, verification, the no-push guarantee, the destructive gate) are exercised exhaustively regardless of the global number. A change that lowers coverage on a safety path will be asked to add tests before merge.

Because of the split above, coverage is judged on the **full suite**
(`go test -tags integration -cover ./...`). Packages that are mostly filesystem
or git orchestration, such as `backup` and `fixturerepo`, will show low *unit-only*
coverage by design, since that logic lives in the integration suite; that is
expected, not a regression. The safety-critical verification logic is written to
be pure, so it stays in the fast unit suite where it is covered on every run.

---

## What to expect from review

Reviews aim to be kind, specific, and honest. You may be asked for tests, for clearer names, or for a smaller scope. None of that is a judgement of you. If a change is declined, the reason will be given plainly. Maintainers reply as capacity allows, not instantly; see the boundary section for why that is by design.

---

Your name is yours. Your work is yours. Thanks for helping make that true for other people too.

Yours. Full stop. 🩷
