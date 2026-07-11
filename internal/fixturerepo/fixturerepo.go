// Package fixturerepo builds known git repositories in temporary directories so
// that tests across the module can exercise real git behaviour without ever
// touching a real repository.
//
// It shells out to the system git binary, exactly as the tool does, but it is
// test-only scaffolding: it is imported solely from _test.go files, so it never
// forms part of the shipped binary.
//
// It deliberately never runs "git push". Remote-tracking state is created with
// "git clone --bare" followed by "git fetch", which reads from a local bare
// repository and writes only local refs. This honours the project's first
// prime directive even in test fixtures.
package fixturerepo

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	defaultAuthorName  = "Fixture Author"
	defaultAuthorEmail = "fixture.author@example.test"
	fixedAuthorDate    = "2001-02-03T04:05:06 +0000"
	fixedCommitterDate = "2001-02-03T04:05:06 +0000"
)

// Repository is a git repository built in a temporary directory for a single test.
type Repository struct {
	testingHandle  testing.TB
	repositoryPath string
	initialBranch  string
}

// CommitSpec describes a single commit to create. Empty identity fields fall
// back to the repository defaults; empty committer fields fall back to the
// author. When Files is empty an empty commit is created.
type CommitSpec struct {
	AuthorName     string
	AuthorEmail    string
	CommitterName  string
	CommitterEmail string
	Message        string
	Files          map[string]string
}

// AnnotatedTagSpec describes an annotated (not lightweight) tag to create on the
// current HEAD.
type AnnotatedTagSpec struct {
	TagName     string
	TaggerName  string
	TaggerEmail string
	Message     string
}

// New initialises an empty git repository in a fresh temporary directory with a
// deterministic default identity and a "main" initial branch.
func New(testingHandle testing.TB) *Repository {
	testingHandle.Helper()
	repositoryPath := testingHandle.TempDir()
	repository := &Repository{
		testingHandle:  testingHandle,
		repositoryPath: repositoryPath,
		initialBranch:  "main",
	}
	repository.runGit(nil, "init", "--initial-branch", repository.initialBranch)
	repository.runGit(nil, "config", "user.name", defaultAuthorName)
	repository.runGit(nil, "config", "user.email", defaultAuthorEmail)
	// Keep commit hashes reproducible and avoid depending on global git config.
	repository.runGit(nil, "config", "commit.gpgsign", "false")
	repository.runGit(nil, "config", "tag.gpgsign", "false")
	return repository
}

// Path returns the absolute path to the repository working tree.
func (repository *Repository) Path() string {
	return repository.repositoryPath
}

// Commit writes the requested files and creates a commit, returning its full hash.
func (repository *Repository) Commit(commitSpec CommitSpec) string {
	repository.testingHandle.Helper()

	for relativePath, fileContent := range commitSpec.Files {
		absolutePath := filepath.Join(repository.repositoryPath, filepath.FromSlash(relativePath))
		if directoryError := os.MkdirAll(filepath.Dir(absolutePath), 0o755); directoryError != nil {
			repository.testingHandle.Fatalf("creating directory for fixture file %q: %v", relativePath, directoryError)
		}
		if writeError := os.WriteFile(absolutePath, []byte(fileContent), 0o644); writeError != nil {
			repository.testingHandle.Fatalf("writing fixture file %q: %v", relativePath, writeError)
		}
	}
	repository.runGit(nil, "add", "--all")

	authorName := valueOrFallback(commitSpec.AuthorName, defaultAuthorName)
	authorEmail := valueOrFallback(commitSpec.AuthorEmail, defaultAuthorEmail)
	committerName := valueOrFallback(commitSpec.CommitterName, authorName)
	committerEmail := valueOrFallback(commitSpec.CommitterEmail, authorEmail)

	commitEnvironment := []string{
		"GIT_AUTHOR_NAME=" + authorName,
		"GIT_AUTHOR_EMAIL=" + authorEmail,
		"GIT_AUTHOR_DATE=" + fixedAuthorDate,
		"GIT_COMMITTER_NAME=" + committerName,
		"GIT_COMMITTER_EMAIL=" + committerEmail,
		"GIT_COMMITTER_DATE=" + fixedCommitterDate,
	}
	repository.runGit(commitEnvironment, "commit", "--allow-empty", "--message", commitSpec.Message)

	commitHash := strings.TrimSpace(string(repository.runGit(nil, "rev-parse", "HEAD")))
	return commitHash
}

// CommitLinearHistory creates commitCount commits on the current branch in a
// single git fast-import pass, rather than spawning a git process per commit.
// It exists so that large-history tests complete in a sane time. Each commit
// touches a counter file so the tree genuinely changes. It must be called on a
// repository with no prior commits.
func (repository *Repository) CommitLinearHistory(commitCount int, authorName, authorEmail string) {
	repository.testingHandle.Helper()
	authorName = valueOrFallback(authorName, defaultAuthorName)
	authorEmail = valueOrFallback(authorEmail, defaultAuthorEmail)

	var importStream bytes.Buffer
	for commitIndex := 1; commitIndex <= commitCount; commitIndex++ {
		commitMessage := fmt.Sprintf("commit number %d", commitIndex)
		fileContent := fmt.Sprintf("counter %d\n", commitIndex)
		commitTimestamp := 1_000_000_000 + commitIndex

		fmt.Fprintf(&importStream, "commit refs/heads/%s\n", repository.initialBranch)
		fmt.Fprintf(&importStream, "mark :%d\n", commitIndex)
		fmt.Fprintf(&importStream, "author %s <%s> %d +0000\n", authorName, authorEmail, commitTimestamp)
		fmt.Fprintf(&importStream, "committer %s <%s> %d +0000\n", authorName, authorEmail, commitTimestamp)
		fmt.Fprintf(&importStream, "data %d\n%s\n", len(commitMessage), commitMessage)
		if commitIndex > 1 {
			fmt.Fprintf(&importStream, "from :%d\n", commitIndex-1)
		}
		fmt.Fprintf(&importStream, "M 100644 inline counter.txt\ndata %d\n%s\n", len(fileContent), fileContent)
		importStream.WriteString("\n")
	}

	importCommand := exec.Command("git", "fast-import", "--quiet")
	importCommand.Dir = repository.repositoryPath
	importCommand.Stdin = &importStream
	importCommand.Env = os.Environ()
	if output, runError := importCommand.CombinedOutput(); runError != nil {
		repository.testingHandle.Fatalf("fixture git fast-import failed: %v: %s", runError, output)
	}
}

// AnnotatedTag creates an annotated tag on the current HEAD.
func (repository *Repository) AnnotatedTag(annotatedTagSpec AnnotatedTagSpec) {
	repository.testingHandle.Helper()
	taggerName := valueOrFallback(annotatedTagSpec.TaggerName, defaultAuthorName)
	taggerEmail := valueOrFallback(annotatedTagSpec.TaggerEmail, defaultAuthorEmail)
	tagEnvironment := []string{
		"GIT_COMMITTER_NAME=" + taggerName,
		"GIT_COMMITTER_EMAIL=" + taggerEmail,
		"GIT_COMMITTER_DATE=" + fixedCommitterDate,
		"GIT_TAGGER_NAME=" + taggerName,
		"GIT_TAGGER_EMAIL=" + taggerEmail,
	}
	repository.runGit(tagEnvironment, "tag", "--annotate", annotatedTagSpec.TagName, "--message", annotatedTagSpec.Message)
}

// PublishToNewBareRemote snapshots the repository's current refs into a new bare
// repository and wires it up as a remote, creating remote-tracking refs via a
// fetch. Commits added after this call are local-only. It never pushes: the
// bare repository is populated by "git clone --bare", which only reads.
//
// It returns the path to the bare remote repository.
func (repository *Repository) PublishToNewBareRemote(remoteName string) string {
	repository.testingHandle.Helper()
	bareRemotePath := filepath.Join(repository.testingHandle.TempDir(), remoteName+".git")
	// clone --bare reads from the source and writes only into bareRemotePath.
	runGitInDirectory(repository.testingHandle, "", nil, "clone", "--bare", repository.repositoryPath, bareRemotePath)
	repository.runGit(nil, "remote", "add", remoteName, bareRemotePath)
	repository.runGit(nil, "fetch", remoteName)
	return bareRemotePath
}

// DetachHead moves HEAD to the current commit in a detached state.
func (repository *Repository) DetachHead() {
	repository.testingHandle.Helper()
	repository.runGit(nil, "checkout", "--detach", "HEAD")
}

// WriteWorkingTreeFile writes an uncommitted file into the working tree, leaving
// the tree dirty. Useful for exercising WorkingTreeIsClean.
func (repository *Repository) WriteWorkingTreeFile(relativePath, fileContent string) {
	repository.testingHandle.Helper()
	absolutePath := filepath.Join(repository.repositoryPath, filepath.FromSlash(relativePath))
	if writeError := os.WriteFile(absolutePath, []byte(fileContent), 0o644); writeError != nil {
		repository.testingHandle.Fatalf("writing working-tree file %q: %v", relativePath, writeError)
	}
}

// CorruptAnObject damages a loose object so that "git fsck" reports the
// repository as broken. It fails the test if no loose object can be found.
func (repository *Repository) CorruptAnObject() {
	repository.testingHandle.Helper()
	looseObjectsRoot := filepath.Join(repository.repositoryPath, ".git", "objects")
	corruptionTarget, findError := firstLooseObjectPath(looseObjectsRoot)
	if findError != nil {
		repository.testingHandle.Fatalf("finding a loose object to corrupt: %v", findError)
	}
	// Git stores loose objects as read-only, so make the file writable before
	// overwriting it. On Windows this clears the read-only attribute.
	if chmodError := os.Chmod(corruptionTarget, 0o644); chmodError != nil {
		repository.testingHandle.Fatalf("making object %q writable: %v", corruptionTarget, chmodError)
	}
	if writeError := os.WriteFile(corruptionTarget, []byte("this is not a valid git object"), 0o644); writeError != nil {
		repository.testingHandle.Fatalf("corrupting object %q: %v", corruptionTarget, writeError)
	}
}

// firstLooseObjectPath returns the path of the first loose object under a git
// objects directory (a file whose parent directory is a two-character fan-out
// bucket). It returns an error when none can be found, so callers and tests can
// handle that case without aborting.
func firstLooseObjectPath(looseObjectsRoot string) (string, error) {
	var looseObjectPath string
	walkError := filepath.Walk(looseObjectsRoot, func(path string, info os.FileInfo, walkStepError error) error {
		if walkStepError != nil {
			return walkStepError
		}
		if info.IsDir() {
			return nil
		}
		parentDirectoryName := filepath.Base(filepath.Dir(path))
		if len(parentDirectoryName) == 2 && looseObjectPath == "" {
			looseObjectPath = path
		}
		return nil
	})
	if walkError != nil {
		return "", fmt.Errorf("walking loose objects under %q: %w", looseObjectsRoot, walkError)
	}
	if looseObjectPath == "" {
		return "", fmt.Errorf("no loose object found under %q", looseObjectsRoot)
	}
	return looseObjectPath, nil
}

// runGit runs git inside the repository working tree, failing the test on error.
func (repository *Repository) runGit(extraEnvironment []string, arguments ...string) []byte {
	repository.testingHandle.Helper()
	return runGitInDirectory(repository.testingHandle, repository.repositoryPath, extraEnvironment, arguments...)
}

func runGitInDirectory(testingHandle testing.TB, workingDirectory string, extraEnvironment []string, arguments ...string) []byte {
	testingHandle.Helper()
	command := exec.Command("git", arguments...)
	if workingDirectory != "" {
		command.Dir = workingDirectory
	}
	command.Env = append(os.Environ(), extraEnvironment...)
	standardOutput, runError := command.Output()
	if runError != nil {
		standardError := ""
		if exitError, isExitError := runError.(*exec.ExitError); isExitError {
			standardError = strings.TrimSpace(string(exitError.Stderr))
		}
		testingHandle.Fatalf("fixture git %s failed: %v: %s", strings.Join(arguments, " "), runError, standardError)
	}
	return standardOutput
}

func valueOrFallback(candidate, fallback string) string {
	if strings.TrimSpace(candidate) == "" {
		return fallback
	}
	return candidate
}
