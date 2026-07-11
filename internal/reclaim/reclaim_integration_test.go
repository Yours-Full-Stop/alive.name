//go:build integration

package reclaim

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"

	"alive.name/internal/backup"
	"alive.name/internal/domain"
	"alive.name/internal/fixturerepo"
	"alive.name/internal/gitclient"
)

// These tests write real temporary files (mailmap and replace-text), so they are
// integration tests even when the filter-repo tool itself is faked.

func TestIntegrationDryRunRunsFilterRepo(testingHandle *testing.T) {
	var output bytes.Buffer
	filterRepo := &fakeFilterRepo{runOutput: "Parsed 3 commits"}
	plan := Plan{OldIdentity: sampleOldIdentity(), NewIdentity: sampleNewIdentity(), Apply: false, Output: &output}

	historyError := history(fakeController{repositoryPath: testingHandle.TempDir(), clean: true}, filterRepo, counterReturning(3), passedBackup(), plan)
	if historyError != nil {
		testingHandle.Fatalf("dry run: %v", historyError)
	}
	if len(filterRepo.runCalledWith) != 1 || !containsArgument(filterRepo.runCalledWith[0], "--dry-run") {
		testingHandle.Errorf("expected a single --dry-run invocation, got %v", filterRepo.runCalledWith)
	}
	if !strings.Contains(output.String(), "dry run") {
		testingHandle.Errorf("expected a dry-run message, got %q", output.String())
	}
}

func TestIntegrationApplyRunsFilterRepoAndPrintsPush(testingHandle *testing.T) {
	var output bytes.Buffer
	filterRepo := &fakeFilterRepo{}
	controller := fakeController{repositoryPath: testingHandle.TempDir(), clean: true, remotes: []gitclient.Remote{{Name: "origin", FetchURL: "git@example.test:me/repo.git"}}}
	plan := Plan{OldIdentity: sampleOldIdentity(), NewIdentity: sampleNewIdentity(), Apply: true, AcknowledgePushedHistory: true, Output: &output}

	historyError := history(controller, filterRepo, counterReturning(3), passedBackup(), plan)
	if historyError != nil {
		testingHandle.Fatalf("apply: %v", historyError)
	}
	if len(filterRepo.runCalledWith) != 1 || containsArgument(filterRepo.runCalledWith[0], "--dry-run") {
		testingHandle.Errorf("expected a single non-dry-run invocation, got %v", filterRepo.runCalledWith)
	}
	if !strings.Contains(output.String(), "force-with-lease") || !strings.Contains(output.String(), "yours to do") {
		testingHandle.Errorf("expected publish instructions, got %q", output.String())
	}
}

func TestIntegrationDryRunProceedsDespiteSignedWithoutAcknowledgement(testingHandle *testing.T) {
	var output bytes.Buffer
	filterRepo := &fakeFilterRepo{}
	controller := fakeController{repositoryPath: testingHandle.TempDir(), clean: true, signed: true}
	plan := Plan{OldIdentity: sampleOldIdentity(), NewIdentity: sampleNewIdentity(), Apply: false, Output: &output}

	historyError := history(controller, filterRepo, counterReturning(3), passedBackup(), plan)
	if historyError != nil {
		testingHandle.Fatalf("a dry run should proceed despite signed commits: %v", historyError)
	}
	if len(filterRepo.runCalledWith) != 1 {
		testingHandle.Errorf("expected filter-repo to run in dry-run mode, got %v", filterRepo.runCalledWith)
	}
	if !strings.Contains(output.String(), "signed commits") {
		testingHandle.Errorf("expected a signed-commit warning, got %q", output.String())
	}
}

// TestIntegrationHistoryWithRealClientFakeFilterRepo exercises the exported entry
// point and the real trace-based occurrence counter against a real repository,
// while faking the destructive filter-repo tool so it need not be installed.
func TestIntegrationHistoryWithRealClientFakeFilterRepo(testingHandle *testing.T) {
	if _, lookupError := exec.LookPath("git"); lookupError != nil {
		testingHandle.Skip("git not available")
	}
	repository := fixturerepo.New(testingHandle)
	repository.Commit(fixturerepo.CommitSpec{
		AuthorName:  "Old Name",
		AuthorEmail: "old@example.test",
		Message:     "work by Old Name",
		Files:       map[string]string{"README.md": "written by Old Name"},
	})
	client, clientError := gitclient.NewGitClient(repository.Path())
	if clientError != nil {
		testingHandle.Fatalf("client: %v", clientError)
	}

	var output bytes.Buffer
	filterRepo := &fakeFilterRepo{runOutput: "would rewrite 1 commit"}
	plan := Plan{
		OldIdentity: domain.OldIdentity{Names: []string{"Old Name"}, Emails: []string{"old@example.test"}},
		NewIdentity: domain.NewIdentity{Name: "New Name", Email: "new@example.test"},
		Apply:       false,
		Output:      &output,
	}
	if historyError := HistoryWithFilterRepoRunner(client, filterRepo, passedBackup(), plan); historyError != nil {
		testingHandle.Fatalf("HistoryWithFilterRepoRunner: %v", historyError)
	}
	if len(filterRepo.runCalledWith) != 1 || !containsArgument(filterRepo.runCalledWith[0], "--dry-run") {
		testingHandle.Errorf("expected one dry-run filter-repo call, got %v", filterRepo.runCalledWith)
	}
}

// TestIntegrationHistoryNothingToDoWithRealClient confirms the nothing-to-do path
// against a real repository where the old name is absent.
func TestIntegrationHistoryNothingToDoWithRealClient(testingHandle *testing.T) {
	if _, lookupError := exec.LookPath("git"); lookupError != nil {
		testingHandle.Skip("git not available")
	}
	repository := fixturerepo.New(testingHandle)
	repository.Commit(fixturerepo.CommitSpec{Message: "unrelated work", Files: map[string]string{"a.txt": "content"}})
	client, clientError := gitclient.NewGitClient(repository.Path())
	if clientError != nil {
		testingHandle.Fatalf("client: %v", clientError)
	}

	var output bytes.Buffer
	filterRepo := &fakeFilterRepo{}
	plan := Plan{
		OldIdentity: domain.OldIdentity{Names: []string{"Absent Name"}},
		NewIdentity: sampleNewIdentity(),
		Output:      &output,
	}
	if historyError := HistoryWithFilterRepoRunner(client, filterRepo, passedBackup(), plan); historyError != nil {
		testingHandle.Fatalf("expected nothing-to-do, got: %v", historyError)
	}
	if len(filterRepo.runCalledWith) != 0 || !strings.Contains(output.String(), "Nothing to do") {
		testingHandle.Errorf("expected a nothing-to-do result, got calls %v and output %q", filterRepo.runCalledWith, output.String())
	}
}

// TestIntegrationRealFilterRepoRewrite exercises the whole path against the real
// git filter-repo tool. It skips when filter-repo is not installed.
func TestIntegrationRealFilterRepoRewrite(testingHandle *testing.T) {
	if _, lookupError := exec.LookPath("git"); lookupError != nil {
		testingHandle.Skip("git not available")
	}
	if versionError := (defaultFilterRepoRunner{}).EnsureAvailable(); versionError != nil {
		testingHandle.Skipf("git filter-repo not available: %v", versionError)
	}

	repository := fixturerepo.New(testingHandle)
	repository.Commit(fixturerepo.CommitSpec{
		AuthorName:  "Old Name",
		AuthorEmail: "old@example.test",
		Message:     "work by Old Name",
		Files:       map[string]string{"README.md": "written by Old Name"},
	})

	client, clientError := gitclient.NewGitClient(repository.Path())
	if clientError != nil {
		testingHandle.Fatalf("client: %v", clientError)
	}
	backupRecord, backupError := backup.CreateVerified(client, backup.Options{StateDirectoryPath: testingHandle.TempDir()})
	if backupError != nil {
		testingHandle.Fatalf("backup: %v", backupError)
	}

	var output bytes.Buffer
	plan := Plan{
		OldIdentity: domain.OldIdentity{Names: []string{"Old Name"}, Emails: []string{"old@example.test"}},
		NewIdentity: domain.NewIdentity{Name: "New Name", Email: "new@example.test"},
		Apply:       true,
		Output:      &output,
	}
	if historyError := History(client, backupRecord, plan); historyError != nil {
		testingHandle.Fatalf("History: %v", historyError)
	}

	authorNames, _ := exec.Command("git", "-C", repository.Path(), "log", "--all", "--format=%an").Output()
	if strings.Contains(string(authorNames), "Old Name") {
		testingHandle.Errorf("expected the old name to be gone from author metadata, got %q", authorNames)
	}
	if !strings.Contains(string(authorNames), "New Name") {
		testingHandle.Errorf("expected the new name in author metadata, got %q", authorNames)
	}
}
