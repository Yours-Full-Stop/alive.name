//go:build integration

package gitclient

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"alive.name/internal/fixturerepo"
)

// These are integration tests: they execute the real git binary against
// throwaway fixture repositories. Run them with:
//
//	go test -tags integration ./...

const (
	oldNameForTests  = "Old Name"
	oldEmailForTests = "old.name@example.test"
)

func requireGitBinary(testingHandle *testing.T) {
	testingHandle.Helper()
	if _, lookupError := exec.LookPath("git"); lookupError != nil {
		testingHandle.Skip("git binary not available; skipping gitclient integration test")
	}
}

func newClientForRepository(testingHandle *testing.T, repositoryPath string) *GitClient {
	testingHandle.Helper()
	client, constructionError := NewGitClient(repositoryPath)
	if constructionError != nil {
		testingHandle.Fatalf("constructing client: %v", constructionError)
	}
	return client
}

func TestIntegrationReadersAgainstFixtureRepository(testingHandle *testing.T) {
	requireGitBinary(testingHandle)
	repository := fixturerepo.New(testingHandle)
	repository.Commit(fixturerepo.CommitSpec{
		AuthorName:  oldNameForTests,
		AuthorEmail: oldEmailForTests,
		Message:     "initial work by " + oldNameForTests,
		Files:       map[string]string{"README.md": "written by " + oldNameForTests + "\n"},
	})
	repository.AnnotatedTag(fixturerepo.AnnotatedTagSpec{
		TagName:     "v1.0.0",
		TaggerName:  oldNameForTests,
		TaggerEmail: oldEmailForTests,
		Message:     "release tagged by " + oldNameForTests,
	})
	remotePath := repository.PublishToNewBareRemote("origin")
	client := newClientForRepository(testingHandle, repository.Path())

	if ensureError := client.EnsureGitAvailable(); ensureError != nil {
		testingHandle.Fatalf("EnsureGitAvailable: %v", ensureError)
	}

	commits, commitsError := client.ListAllCommitsWithMetadata()
	if commitsError != nil {
		testingHandle.Fatalf("ListAllCommitsWithMetadata: %v", commitsError)
	}
	if len(commits) != 1 || commits[0].AuthorName != oldNameForTests || commits[0].AuthorEmail != oldEmailForTests {
		testingHandle.Fatalf("unexpected commits: %+v", commits)
	}

	annotatedTags, tagsError := client.ListAnnotatedTags()
	if tagsError != nil {
		testingHandle.Fatalf("ListAnnotatedTags: %v", tagsError)
	}
	if len(annotatedTags) != 1 || annotatedTags[0].TagName != "v1.0.0" || annotatedTags[0].TaggerName != oldNameForTests {
		testingHandle.Fatalf("unexpected tags: %+v", annotatedTags)
	}
	if !strings.Contains(annotatedTags[0].Message, oldNameForTests) {
		testingHandle.Errorf("tag message %q did not contain %q", annotatedTags[0].Message, oldNameForTests)
	}

	remotes, remotesError := client.ListRemotes()
	if remotesError != nil {
		testingHandle.Fatalf("ListRemotes: %v", remotesError)
	}
	if len(remotes) != 1 || remotes[0].Name != "origin" {
		testingHandle.Fatalf("expected a single remote named origin, got %+v", remotes)
	}

	remoteReachableCommits, remoteReachableError := client.ListCommitsReachableFromRemoteTrackingRefs()
	if remoteReachableError != nil {
		testingHandle.Fatalf("ListCommitsReachableFromRemoteTrackingRefs: %v", remoteReachableError)
	}
	if len(remoteReachableCommits) != 1 {
		testingHandle.Errorf("expected 1 remote-reachable commit, got %d", len(remoteReachableCommits))
	}

	if integrityError := client.VerifyRepositoryIntegrity(); integrityError != nil {
		testingHandle.Errorf("VerifyRepositoryIntegrity: %v", integrityError)
	}

	isClean, cleanError := client.WorkingTreeIsClean()
	if cleanError != nil {
		testingHandle.Fatalf("WorkingTreeIsClean: %v", cleanError)
	}
	if !isClean {
		testingHandle.Errorf("expected a clean working tree")
	}

	bundlePath := filepath.Join(testingHandle.TempDir(), "all-refs.bundle")
	if bundleError := client.CreateAllRefsBundle(bundlePath); bundleError != nil {
		testingHandle.Fatalf("CreateAllRefsBundle: %v", bundleError)
	}
	if verifyError := client.VerifyBundle(bundlePath); verifyError != nil {
		testingHandle.Errorf("VerifyBundle: %v", verifyError)
	}

	mirrorPath := filepath.Join(testingHandle.TempDir(), "mirror.git")
	if mirrorError := client.CloneMirrorOfRemote(remotePath, mirrorPath); mirrorError != nil {
		testingHandle.Errorf("CloneMirrorOfRemote: %v", mirrorError)
	}
}

func TestIntegrationAdditionalReaders(testingHandle *testing.T) {
	requireGitBinary(testingHandle)
	repository := fixturerepo.New(testingHandle)
	repository.Commit(fixturerepo.CommitSpec{
		Message: "work",
		Files:   map[string]string{"README.md": "hello", "docs/guide.md": "guide"},
	})
	repository.PublishToNewBareRemote("origin")
	client := newClientForRepository(testingHandle, repository.Path())

	objectHashes, objectsError := client.ListAllObjectHashes()
	if objectsError != nil || len(objectHashes) == 0 {
		testingHandle.Fatalf("ListAllObjectHashes: %v (%d hashes)", objectsError, len(objectHashes))
	}

	trackedFiles, filesError := client.ListTrackedWorkingTreeFiles()
	if filesError != nil {
		testingHandle.Fatalf("ListTrackedWorkingTreeFiles: %v", filesError)
	}
	foundReadme := false
	for _, trackedFile := range trackedFiles {
		if trackedFile == "README.md" {
			foundReadme = true
		}
	}
	if !foundReadme {
		testingHandle.Errorf("expected README.md among tracked files, got %v", trackedFiles)
	}

	if fetchError := client.FetchAllRemotes(); fetchError != nil {
		testingHandle.Errorf("FetchAllRemotes: %v", fetchError)
	}
}

func TestIntegrationEmptyRepository(testingHandle *testing.T) {
	requireGitBinary(testingHandle)
	repository := fixturerepo.New(testingHandle)
	client := newClientForRepository(testingHandle, repository.Path())

	commits, commitsError := client.ListAllCommitsWithMetadata()
	if commitsError != nil || len(commits) != 0 {
		testingHandle.Fatalf("expected no commits, got %d (err %v)", len(commits), commitsError)
	}
	objectHashes, objectsError := client.ListAllObjectHashes()
	if objectsError != nil || len(objectHashes) != 0 {
		testingHandle.Fatalf("expected no objects, got %d (err %v)", len(objectHashes), objectsError)
	}
	remotes, remotesError := client.ListRemotes()
	if remotesError != nil || len(remotes) != 0 {
		testingHandle.Fatalf("expected no remotes, got %d (err %v)", len(remotes), remotesError)
	}
}

func TestIntegrationDetachedHeadAndUnicode(testingHandle *testing.T) {
	requireGitBinary(testingHandle)
	const unicodeName = "Óld Támé"
	repository := fixturerepo.New(testingHandle)
	repository.Commit(fixturerepo.CommitSpec{Message: "first"})
	repository.Commit(fixturerepo.CommitSpec{AuthorName: unicodeName, AuthorEmail: oldEmailForTests, Message: "second by unicode author"})
	repository.DetachHead()

	client := newClientForRepository(testingHandle, repository.Path())
	commits, commitsError := client.ListAllCommitsWithMetadata()
	if commitsError != nil {
		testingHandle.Fatalf("ListAllCommitsWithMetadata: %v", commitsError)
	}
	foundUnicodeAuthor := false
	for _, commitMetadata := range commits {
		if commitMetadata.AuthorName == unicodeName {
			foundUnicodeAuthor = true
		}
	}
	if !foundUnicodeAuthor {
		testingHandle.Errorf("expected unicode author %q among %d commits", unicodeName, len(commits))
	}
}

func TestIntegrationShallowCloneReadsCleanly(testingHandle *testing.T) {
	requireGitBinary(testingHandle)
	source := fixturerepo.New(testingHandle)
	source.Commit(fixturerepo.CommitSpec{Message: "first"})
	source.Commit(fixturerepo.CommitSpec{Message: "second"})
	source.Commit(fixturerepo.CommitSpec{Message: "third"})

	shallowPath := filepath.Join(testingHandle.TempDir(), "shallow")
	cloneCommand := exec.Command("git", "clone", "--depth", "1", "file://"+filepath.ToSlash(source.Path()), shallowPath)
	if cloneOutput, cloneError := cloneCommand.CombinedOutput(); cloneError != nil {
		testingHandle.Skipf("could not create shallow clone (%v): %s", cloneError, cloneOutput)
	}

	client := newClientForRepository(testingHandle, shallowPath)
	commits, commitsError := client.ListAllCommitsWithMetadata()
	if commitsError != nil {
		testingHandle.Fatalf("ListAllCommitsWithMetadata on shallow clone: %v", commitsError)
	}
	if len(commits) != 1 {
		testingHandle.Errorf("expected a single commit in shallow clone, got %d", len(commits))
	}
}

func TestIntegrationLargeHistoryCompletes(testingHandle *testing.T) {
	requireGitBinary(testingHandle)
	const commitCount = 1000
	repository := fixturerepo.New(testingHandle)
	repository.CommitLinearHistory(commitCount, oldNameForTests, oldEmailForTests)

	client := newClientForRepository(testingHandle, repository.Path())
	commits, commitsError := client.ListAllCommitsWithMetadata()
	if commitsError != nil {
		testingHandle.Fatalf("ListAllCommitsWithMetadata on large history: %v", commitsError)
	}
	if len(commits) != commitCount {
		testingHandle.Errorf("expected %d commits, got %d", commitCount, len(commits))
	}
}

func TestIntegrationGitPresentButNotRunnable(testingHandle *testing.T) {
	requireGitBinary(testingHandle)
	failingRunner := &scriptedGitRunner{respond: func(GitInvocation) (string, string, error) {
		return "", "simulated git failure", errors.New("boom")
	}}
	client, constructionError := NewGitClientWithRunner(testingHandle.TempDir(), failingRunner)
	if constructionError != nil {
		testingHandle.Fatalf("constructing client with runner: %v", constructionError)
	}
	ensureError := client.EnsureGitAvailable()
	if ensureError == nil || !strings.Contains(ensureError.Error(), "could not be run") {
		testingHandle.Fatalf("expected a clear 'could not be run' error, got: %v", ensureError)
	}
}

// TestIntegrationNewGitClientPathValidation covers the constructor branches that
// stat the real filesystem.
func TestIntegrationNewGitClientPathValidation(testingHandle *testing.T) {
	testingHandle.Run("non-existent path", func(subTest *testing.T) {
		if _, constructionError := NewGitClient(filepath.Join(subTest.TempDir(), "does-not-exist")); constructionError == nil {
			subTest.Error("expected error for a non-existent path")
		}
	})
	testingHandle.Run("path is a file not a directory", func(subTest *testing.T) {
		filePath := filepath.Join(subTest.TempDir(), "a-file")
		if writeError := os.WriteFile(filePath, []byte("x"), 0o644); writeError != nil {
			subTest.Fatalf("writing file: %v", writeError)
		}
		if _, constructionError := NewGitClient(filePath); constructionError == nil {
			subTest.Error("expected error when the path is a file")
		}
	})
}

func TestIntegrationErrorConditions(testingHandle *testing.T) {
	requireGitBinary(testingHandle)

	testingHandle.Run("non-repository path", func(subTest *testing.T) {
		client := newClientForRepository(subTest, subTest.TempDir())
		if _, listError := client.ListAllCommitsWithMetadata(); listError == nil {
			subTest.Fatal("expected error listing commits in a non-repository directory")
		}
	})

	testingHandle.Run("corrupt repository fails fsck", func(subTest *testing.T) {
		repository := fixturerepo.New(subTest)
		repository.Commit(fixturerepo.CommitSpec{Message: "a commit", Files: map[string]string{"file.txt": "content"}})
		repository.CorruptAnObject()
		client := newClientForRepository(subTest, repository.Path())
		if integrityError := client.VerifyRepositoryIntegrity(); integrityError == nil {
			subTest.Fatal("expected VerifyRepositoryIntegrity to fail on a corrupt repository")
		}
	})

	testingHandle.Run("unreadable object surfaces error", func(subTest *testing.T) {
		repository := fixturerepo.New(subTest)
		repository.Commit(fixturerepo.CommitSpec{Message: "a commit"})
		client := newClientForRepository(subTest, repository.Path())
		if _, readError := client.ReadObjectContent("0000000000000000000000000000000000000000"); readError == nil {
			subTest.Fatal("expected error reading a non-existent object")
		}
	})
}
