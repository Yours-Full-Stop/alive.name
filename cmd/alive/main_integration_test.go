//go:build integration

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"alive.name/internal/fixturerepo"
)

// Integration tests: they build a real fixture repository and drive the actual
// cobra commands end to end.

func requireGitBinary(testingHandle *testing.T) {
	testingHandle.Helper()
	if _, lookupError := exec.LookPath("git"); lookupError != nil {
		testingHandle.Skip("git not available")
	}
}

func executeCommand(testingHandle *testing.T, arguments ...string) (string, error) {
	testingHandle.Helper()
	return executeCommandWithInput(testingHandle, "", arguments...)
}

func executeCommandWithInput(testingHandle *testing.T, input string, arguments ...string) (string, error) {
	testingHandle.Helper()
	rootCommand := newRootCommand()
	var output bytes.Buffer
	rootCommand.SetOut(&output)
	rootCommand.SetErr(&output)
	rootCommand.SetIn(strings.NewReader(input))
	rootCommand.SetArgs(arguments)
	executeError := rootCommand.Execute()
	return output.String(), executeError
}

func TestIntegrationGuideFlowAcceptMendDeclineDeep(testingHandle *testing.T) {
	requireGitBinary(testingHandle)
	repository := fixturerepo.New(testingHandle)
	repository.Commit(fixturerepo.CommitSpec{AuthorName: "Old Name", AuthorEmail: "old@example.test", Message: "work by Old Name"})

	// Answers: old names, old emails, new name, new email, then accept mend (y),
	// then decline the deep rewrite (n).
	scriptedInput := strings.Join([]string{"Old Name", "old@example.test", "New Name", "new@example.test", "y", "n"}, "\n") + "\n"

	output, executeError := executeCommandWithInput(testingHandle, scriptedInput, "guide", "--repo", repository.Path())
	if executeError != nil {
		testingHandle.Fatalf("guide: %v", executeError)
	}
	if !strings.Contains(output, "Wrote a .mailmap") && !strings.Contains(output, "wrote a .mailmap") {
		testingHandle.Errorf("expected the mailmap to be written in the guided flow:\n%s", output)
	}
	if !strings.Contains(output, "fine place to stop") {
		testingHandle.Errorf("expected a kind stop after declining the deep rewrite:\n%s", output)
	}
	if _, statError := os.Stat(filepath.Join(repository.Path(), ".mailmap")); statError != nil {
		testingHandle.Errorf("expected a .mailmap file: %v", statError)
	}
}

func TestIntegrationTraceCommand(testingHandle *testing.T) {
	requireGitBinary(testingHandle)
	repository := fixturerepo.New(testingHandle)
	repository.Commit(fixturerepo.CommitSpec{
		AuthorName:  "Old Name",
		AuthorEmail: "old@example.test",
		Message:     "work by Old Name",
		Files:       map[string]string{"README.md": "written by Old Name"},
	})

	output, executeError := executeCommand(testingHandle, "trace", "--repo", repository.Path(), "--old-name", "Old Name", "--old-email", "old@example.test", "--deep")
	if executeError != nil {
		testingHandle.Fatalf("trace command: %v", executeError)
	}
	if !strings.Contains(output, "occurrence") || !strings.Contains(output, "Old Name") {
		testingHandle.Errorf("unexpected trace output:\n%s", output)
	}
}

func TestIntegrationMendCommand(testingHandle *testing.T) {
	requireGitBinary(testingHandle)
	repository := fixturerepo.New(testingHandle)
	repository.Commit(fixturerepo.CommitSpec{AuthorName: "Old Name", AuthorEmail: "old@example.test", Message: "work"})

	output, executeError := executeCommand(testingHandle, "mend", "--repo", repository.Path(),
		"--old-name", "Old Name", "--old-email", "old@example.test",
		"--new-name", "New Name", "--new-email", "new@example.test")
	if executeError != nil {
		testingHandle.Fatalf("mend command: %v", executeError)
	}
	if !strings.Contains(output, "Wrote .mailmap") {
		testingHandle.Errorf("unexpected mend output:\n%s", output)
	}
	mailmapBytes, readError := os.ReadFile(filepath.Join(repository.Path(), ".mailmap"))
	if readError != nil {
		testingHandle.Fatalf("reading .mailmap: %v", readError)
	}
	if !strings.Contains(string(mailmapBytes), "new@example.test") {
		testingHandle.Errorf("unexpected .mailmap content:\n%s", mailmapBytes)
	}
}

func TestIntegrationBackupCreateListAndRemove(testingHandle *testing.T) {
	requireGitBinary(testingHandle)
	repository := fixturerepo.New(testingHandle)
	repository.Commit(fixturerepo.CommitSpec{Message: "work", Files: map[string]string{"a.txt": "one"}})
	stateDirectory := testingHandle.TempDir()

	createOutput, createError := executeCommand(testingHandle, "backup", "create", "--repo", repository.Path(), "--state-dir", stateDirectory)
	if createError != nil {
		testingHandle.Fatalf("backup create: %v", createError)
	}
	if !strings.Contains(createOutput, "verified") {
		testingHandle.Errorf("unexpected create output:\n%s", createOutput)
	}

	listOutput, listError := executeCommand(testingHandle, "backup", "list", "--state-dir", stateDirectory)
	if listError != nil {
		testingHandle.Fatalf("backup list: %v", listError)
	}
	if !strings.Contains(listOutput, "alive-backup-") || !strings.Contains(listOutput, "passed") {
		testingHandle.Errorf("unexpected list output:\n%s", listOutput)
	}

	identifier := strings.Fields(listOutput)[0]
	removeOutput, removeError := executeCommand(testingHandle, "backup", "rm", identifier, "--state-dir", stateDirectory)
	if removeError != nil {
		testingHandle.Fatalf("backup rm: %v", removeError)
	}
	if !strings.Contains(removeOutput, "Removed") {
		testingHandle.Errorf("unexpected rm output:\n%s", removeOutput)
	}
}

func TestIntegrationBackupRestoreAndGarbageCollect(testingHandle *testing.T) {
	requireGitBinary(testingHandle)
	repository := fixturerepo.New(testingHandle)
	repository.Commit(fixturerepo.CommitSpec{Message: "work", Files: map[string]string{"a.txt": "one"}})
	stateDirectory := testingHandle.TempDir()

	if _, createError := executeCommand(testingHandle, "backup", "create", "--repo", repository.Path(), "--state-dir", stateDirectory); createError != nil {
		testingHandle.Fatalf("backup create: %v", createError)
	}
	listOutput, _ := executeCommand(testingHandle, "backup", "list", "--state-dir", stateDirectory)
	identifier := strings.Fields(listOutput)[0]

	restoreDestination := filepath.Join(testingHandle.TempDir(), "restored")
	restoreOutput, restoreError := executeCommand(testingHandle, "backup", "restore", identifier, "--state-dir", stateDirectory, "--destination", restoreDestination)
	if restoreError != nil {
		testingHandle.Fatalf("backup restore: %v", restoreError)
	}
	if !strings.Contains(restoreOutput, "Restored to") {
		testingHandle.Errorf("unexpected restore output:\n%s", restoreOutput)
	}
	if _, statError := os.Stat(filepath.Join(restoreDestination, "a.txt")); statError != nil {
		testingHandle.Errorf("expected the restored file: %v", statError)
	}

	// A remote restore with no mirror should report that clearly, not push.
	if _, remoteError := executeCommand(testingHandle, "backup", "restore", identifier, "--state-dir", stateDirectory, "--remote"); remoteError == nil {
		testingHandle.Error("expected an error restoring a remote from a backup with no mirror")
	}

	gcOutput, gcError := executeCommand(testingHandle, "backup", "gc", "--state-dir", stateDirectory, "--older-than", "0s")
	if gcError != nil {
		testingHandle.Fatalf("backup gc: %v", gcError)
	}
	if !strings.Contains(gcOutput, "Removed") {
		testingHandle.Errorf("unexpected gc output:\n%s", gcOutput)
	}
}

func TestIntegrationReclaimReportsMissingFilterRepoAfterBackup(testingHandle *testing.T) {
	requireGitBinary(testingHandle)
	if versionError := exec.Command("git", "filter-repo", "--version").Run(); versionError == nil {
		testingHandle.Skip("git filter-repo is installed; this test checks the missing-tool path")
	}
	repository := fixturerepo.New(testingHandle)
	repository.Commit(fixturerepo.CommitSpec{AuthorName: "Old Name", AuthorEmail: "old@example.test", Message: "work by Old Name"})

	output, executeError := executeCommand(testingHandle, "reclaim", "--repo", repository.Path(),
		"--old-name", "Old Name", "--old-email", "old@example.test",
		"--new-name", "New Name", "--new-email", "new@example.test",
		"--state-dir", testingHandle.TempDir())
	if executeError == nil {
		testingHandle.Fatal("expected an error when filter-repo is missing")
	}
	if !strings.Contains(output, "Backup verified") {
		testingHandle.Errorf("expected a verified backup before the failure, got:\n%s", output)
	}
}
