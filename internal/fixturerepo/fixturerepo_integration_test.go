//go:build integration

package fixturerepo

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// These integration tests exercise the fixture builder's real git behaviour.

func requireGitBinary(testingHandle *testing.T) {
	testingHandle.Helper()
	if _, lookupError := exec.LookPath("git"); lookupError != nil {
		testingHandle.Skip("git binary not available; skipping fixturerepo integration test")
	}
}

func runGitCapture(testingHandle *testing.T, workingDirectory string, arguments ...string) string {
	testingHandle.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = workingDirectory
	output, runError := command.Output()
	if runError != nil {
		testingHandle.Fatalf("git %s in %q failed: %v", strings.Join(arguments, " "), workingDirectory, runError)
	}
	return strings.TrimSpace(string(output))
}

func TestIntegrationCommitHappyPath(testingHandle *testing.T) {
	requireGitBinary(testingHandle)
	repository := New(testingHandle)
	commitHash := repository.Commit(CommitSpec{
		AuthorName:  "Old Name",
		AuthorEmail: "old.name@example.test",
		Message:     "the first commit",
		Files:       map[string]string{"README.md": "hello"},
	})
	if len(commitHash) != 40 {
		testingHandle.Fatalf("expected a 40-character commit hash, got %q", commitHash)
	}
	if authorName := runGitCapture(testingHandle, repository.Path(), "log", "-1", "--format=%an"); authorName != "Old Name" {
		testingHandle.Errorf("author name: expected %q, got %q", "Old Name", authorName)
	}
	if authorEmail := runGitCapture(testingHandle, repository.Path(), "log", "-1", "--format=%ae"); authorEmail != "old.name@example.test" {
		testingHandle.Errorf("author email: expected %q, got %q", "old.name@example.test", authorEmail)
	}
	if subject := runGitCapture(testingHandle, repository.Path(), "log", "-1", "--format=%s"); subject != "the first commit" {
		testingHandle.Errorf("subject: expected %q, got %q", "the first commit", subject)
	}
	if _, statError := os.Stat(filepath.Join(repository.Path(), "README.md")); statError != nil {
		testingHandle.Errorf("expected README.md to exist: %v", statError)
	}
}

func TestIntegrationNegativePaths(testingHandle *testing.T) {
	requireGitBinary(testingHandle)

	testingHandle.Run("empty commit with no files still commits", func(subTest *testing.T) {
		repository := New(subTest)
		if commitHash := repository.Commit(CommitSpec{Message: "empty commit"}); len(commitHash) != 40 {
			subTest.Fatalf("expected a commit hash, got %q", commitHash)
		}
	})

	testingHandle.Run("commit after publish is local-only", func(subTest *testing.T) {
		repository := New(subTest)
		repository.Commit(CommitSpec{Message: "published commit"})
		repository.PublishToNewBareRemote("origin")
		localOnlyHash := repository.Commit(CommitSpec{Message: "local only commit"})

		remoteReachable := runGitCapture(subTest, repository.Path(), "rev-list", "--remotes")
		if strings.Contains(remoteReachable, localOnlyHash) {
			subTest.Errorf("commit %s made after publish should not be on the remote", localOnlyHash)
		}
		if remoteReachable == "" {
			subTest.Error("expected the published commit to be reachable from the remote")
		}
	})
}

func TestIntegrationEdgeCases(testingHandle *testing.T) {
	requireGitBinary(testingHandle)

	testingHandle.Run("unicode author is preserved", func(subTest *testing.T) {
		repository := New(subTest)
		repository.Commit(CommitSpec{AuthorName: "Óld Support Támé", AuthorEmail: "u@example.test", Message: "unicode"})
		if authorName := runGitCapture(subTest, repository.Path(), "log", "-1", "--format=%an"); authorName != "Óld Support Támé" {
			subTest.Errorf("expected unicode author, got %q", authorName)
		}
	})

	testingHandle.Run("nested file is tracked", func(subTest *testing.T) {
		repository := New(subTest)
		repository.Commit(CommitSpec{Message: "nested", Files: map[string]string{"a/b/c.txt": "deep"}})
		if trackedFiles := runGitCapture(subTest, repository.Path(), "ls-files"); !strings.Contains(trackedFiles, "a/b/c.txt") {
			subTest.Errorf("expected nested file to be tracked, got %q", trackedFiles)
		}
	})

	testingHandle.Run("large linear history via fast-import", func(subTest *testing.T) {
		repository := New(subTest)
		repository.CommitLinearHistory(50, "Old Name", "old.name@example.test")
		count := runGitCapture(subTest, repository.Path(), "rev-list", "--count", "--all")
		if count != "50" {
			subTest.Errorf("expected 50 commits, got %q", count)
		}
	})

	testingHandle.Run("detached HEAD", func(subTest *testing.T) {
		repository := New(subTest)
		repository.Commit(CommitSpec{Message: "one"})
		repository.DetachHead()
		if currentBranch := runGitCapture(subTest, repository.Path(), "rev-parse", "--abbrev-ref", "HEAD"); currentBranch != "HEAD" {
			subTest.Errorf("expected detached HEAD, got branch %q", currentBranch)
		}
	})

	testingHandle.Run("working tree file dirties the tree", func(subTest *testing.T) {
		repository := New(subTest)
		repository.Commit(CommitSpec{Message: "one", Files: map[string]string{"tracked.txt": "x"}})
		repository.WriteWorkingTreeFile("untracked.txt", "dirty")
		if status := runGitCapture(subTest, repository.Path(), "status", "--porcelain"); status == "" {
			subTest.Error("expected a dirty working tree")
		}
	})

	testingHandle.Run("annotated tag is created", func(subTest *testing.T) {
		repository := New(subTest)
		repository.Commit(CommitSpec{Message: "one"})
		repository.AnnotatedTag(AnnotatedTagSpec{TagName: "v1", TaggerName: "Old Name", TaggerEmail: "o@example.test", Message: "release"})
		if objectType := runGitCapture(subTest, repository.Path(), "cat-file", "-t", "v1"); objectType != "tag" {
			subTest.Errorf("expected an annotated tag object, got %q", objectType)
		}
	})

	testingHandle.Run("corruption breaks fsck", func(subTest *testing.T) {
		repository := New(subTest)
		repository.Commit(CommitSpec{Message: "one", Files: map[string]string{"file.txt": "content"}})
		repository.CorruptAnObject()
		fsckCommand := exec.Command("git", "fsck", "--full")
		fsckCommand.Dir = repository.Path()
		if fsckError := fsckCommand.Run(); fsckError == nil {
			subTest.Error("expected fsck to fail on a corrupted repository")
		}
	})
}

func TestIntegrationFirstLooseObjectPathFindsReal(testingHandle *testing.T) {
	requireGitBinary(testingHandle)
	repository := New(testingHandle)
	repository.Commit(CommitSpec{Message: "one", Files: map[string]string{"file.txt": "content"}})
	looseObjectsRoot := filepath.Join(repository.Path(), ".git", "objects")
	looseObjectPath, findError := firstLooseObjectPath(looseObjectsRoot)
	if findError != nil || looseObjectPath == "" {
		testingHandle.Fatalf("expected to find a loose object, got %q (err %v)", looseObjectPath, findError)
	}
}

// TestIntegrationFirstLooseObjectPathErrorsWhenNoneExist touches the real
// filesystem (an empty directory), so it lives with the integration tests.
func TestIntegrationFirstLooseObjectPathErrorsWhenNoneExist(testingHandle *testing.T) {
	if _, findError := firstLooseObjectPath(testingHandle.TempDir()); findError == nil {
		testingHandle.Error("expected an error when there are no loose objects")
	}
}
