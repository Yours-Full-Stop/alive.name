//go:build integration

package mend

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"alive.name/internal/domain"
	"alive.name/internal/fixturerepo"
)

// TestWriteMailmapFile touches the real filesystem, so it lives with the
// integration tests.
func TestWriteMailmapFile(testingHandle *testing.T) {
	testingHandle.Run("happy path writes .mailmap", func(subTest *testing.T) {
		repositoryPath := subTest.TempDir()
		if writeError := WriteMailmapFile(repositoryPath, "New Name <new@example.test> <old@example.test>\n"); writeError != nil {
			subTest.Fatalf("WriteMailmapFile: %v", writeError)
		}
		writtenBytes, readError := os.ReadFile(filepath.Join(repositoryPath, ".mailmap"))
		if readError != nil {
			subTest.Fatalf("reading written file: %v", readError)
		}
		if !strings.Contains(string(writtenBytes), "old@example.test") {
			subTest.Errorf("unexpected file content: %q", writtenBytes)
		}
	})

	testingHandle.Run("unwritable path is an error", func(subTest *testing.T) {
		missingRepository := filepath.Join(subTest.TempDir(), "does-not-exist")
		if writeError := WriteMailmapFile(missingRepository, "content"); writeError == nil {
			subTest.Error("expected an error writing into a non-existent directory")
		}
	})
}

// TestIntegrationMailmapIsHonouredByGit confirms that the generated .mailmap
// actually changes how git displays the identity, the whole point of mend.
func TestIntegrationMailmapIsHonouredByGit(testingHandle *testing.T) {
	if _, lookupError := exec.LookPath("git"); lookupError != nil {
		testingHandle.Skip("git binary not available")
	}
	repository := fixturerepo.New(testingHandle)
	repository.Commit(fixturerepo.CommitSpec{
		AuthorName:  "Old Name",
		AuthorEmail: "old@example.test",
		Message:     "a commit",
	})

	mailmapContent, generateError := GenerateMailmap(
		domain.OldIdentity{Names: []string{"Old Name"}, Emails: []string{"old@example.test"}},
		domain.NewIdentity{Name: "New Name", Email: "new@example.test"},
	)
	if generateError != nil {
		testingHandle.Fatalf("GenerateMailmap: %v", generateError)
	}
	if writeError := WriteMailmapFile(repository.Path(), mailmapContent); writeError != nil {
		testingHandle.Fatalf("WriteMailmapFile: %v", writeError)
	}

	logCommand := exec.Command("git", "log", "-1", "--format=%aN <%aE>", "--use-mailmap")
	logCommand.Dir = repository.Path()
	output, runError := logCommand.Output()
	if runError != nil {
		testingHandle.Fatalf("git log: %v", runError)
	}
	displayed := strings.TrimSpace(string(output))
	if displayed != "New Name <new@example.test>" {
		testingHandle.Errorf("expected mailmap to remap the identity, got %q", displayed)
	}
}
