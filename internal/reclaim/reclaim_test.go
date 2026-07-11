package reclaim

import (
	"bytes"
	"strings"
	"testing"

	"alive.name/internal/backup"
	"alive.name/internal/domain"
	"alive.name/internal/gitclient"
)

// Unit tests: the safety gates and the pure command builders. Every refusal
// returns before any temporary file is written, so these need no filesystem.
// The orchestration that writes files and runs filter-repo is under the
// integration tag.

type fakeController struct {
	repositoryPath string
	clean          bool
	cleanErr       error
	signed         bool
	signedErr      error
	remotes        []gitclient.Remote
	remotesErr     error
}

func (controller fakeController) RepositoryPath() string { return controller.repositoryPath }
func (controller fakeController) WorkingTreeIsClean() (bool, error) {
	return controller.clean, controller.cleanErr
}
func (controller fakeController) HasSignedCommits() (bool, error) {
	return controller.signed, controller.signedErr
}
func (controller fakeController) ListRemotes() ([]gitclient.Remote, error) {
	return controller.remotes, controller.remotesErr
}

type fakeFilterRepo struct {
	availableErr  error
	runOutput     string
	runErr        error
	runCalledWith [][]string
}

func (filterRepo *fakeFilterRepo) EnsureAvailable() error { return filterRepo.availableErr }
func (filterRepo *fakeFilterRepo) Run(_ string, arguments []string) (string, error) {
	filterRepo.runCalledWith = append(filterRepo.runCalledWith, arguments)
	return filterRepo.runOutput, filterRepo.runErr
}

func counterReturning(count int) occurrenceCounter {
	return func(domain.OldIdentity) (int, error) { return count, nil }
}

func passedBackup() backup.Record {
	return backup.Record{VerificationStatus: domain.VerificationPassed}
}

func sampleOldIdentity() domain.OldIdentity {
	return domain.OldIdentity{Names: []string{"Old Name"}, Emails: []string{"old@example.test"}}
}

func sampleNewIdentity() domain.NewIdentity {
	return domain.NewIdentity{Name: "New Name", Email: "new@example.test"}
}

func TestHistoryNothingToDo(testingHandle *testing.T) {
	var output bytes.Buffer
	filterRepo := &fakeFilterRepo{}
	plan := Plan{OldIdentity: sampleOldIdentity(), NewIdentity: sampleNewIdentity(), Output: &output}

	historyError := history(fakeController{clean: true}, filterRepo, counterReturning(0), backup.Record{}, plan)
	if historyError != nil {
		testingHandle.Fatalf("nothing-to-do should not error: %v", historyError)
	}
	if !strings.Contains(output.String(), "Nothing to do") {
		testingHandle.Errorf("expected a nothing-to-do message, got %q", output.String())
	}
	if len(filterRepo.runCalledWith) != 0 {
		testingHandle.Error("filter-repo must not run when there is nothing to do")
	}
}

func TestHistoryRefusalGates(testingHandle *testing.T) {
	testCases := []struct {
		caseName   string
		controller fakeController
		filterRepo *fakeFilterRepo
		backup     backup.Record
		plan       Plan
	}{
		{
			caseName:   "unverified backup is refused",
			controller: fakeController{clean: true},
			filterRepo: &fakeFilterRepo{},
			backup:     backup.Record{VerificationStatus: domain.VerificationFailed},
			plan:       Plan{Apply: true},
		},
		{
			caseName:   "missing filter-repo is refused",
			controller: fakeController{clean: true},
			filterRepo: &fakeFilterRepo{availableErr: errString("not found")},
			backup:     passedBackup(),
			plan:       Plan{Apply: true},
		},
		{
			caseName:   "dirty working tree is refused",
			controller: fakeController{clean: false},
			filterRepo: &fakeFilterRepo{},
			backup:     passedBackup(),
			plan:       Plan{Apply: true},
		},
		{
			caseName:   "signed commits without acknowledgement are refused",
			controller: fakeController{clean: true, signed: true},
			filterRepo: &fakeFilterRepo{},
			backup:     passedBackup(),
			plan:       Plan{Apply: true, AcknowledgePushedHistory: true},
		},
		{
			caseName:   "pushed history without acknowledgement is refused",
			controller: fakeController{clean: true, remotes: []gitclient.Remote{{Name: "origin"}}},
			filterRepo: &fakeFilterRepo{},
			backup:     passedBackup(),
			plan:       Plan{Apply: true, AcknowledgeSignedCommits: true},
		},
	}
	for _, testCase := range testCases {
		testingHandle.Run(testCase.caseName, func(subTest *testing.T) {
			testCase.plan.OldIdentity = sampleOldIdentity()
			testCase.plan.NewIdentity = sampleNewIdentity()
			historyError := history(testCase.controller, testCase.filterRepo, counterReturning(3), testCase.backup, testCase.plan)
			if historyError == nil {
				subTest.Fatal("expected a refusal error")
			}
			if len(testCase.filterRepo.runCalledWith) != 0 {
				subTest.Error("filter-repo must not run when a gate refuses")
			}
		})
	}
}

func TestBuildReplaceTextExpressions(testingHandle *testing.T) {
	testingHandle.Run("names and emails that change", func(subTest *testing.T) {
		expressions := buildReplaceTextExpressions(sampleOldIdentity(), sampleNewIdentity())
		if !strings.Contains(expressions, "Old Name==>New Name") || !strings.Contains(expressions, "old@example.test==>new@example.test") {
			subTest.Errorf("unexpected expressions: %q", expressions)
		}
	})
	testingHandle.Run("no-op mappings are skipped", func(subTest *testing.T) {
		expressions := buildReplaceTextExpressions(
			domain.OldIdentity{Names: []string{"Same Name"}, Emails: []string{"same@example.test"}},
			domain.NewIdentity{Name: "Same Name", Email: "same@example.test"},
		)
		if expressions != "" {
			subTest.Errorf("expected no expressions for an unchanged identity, got %q", expressions)
		}
	})
}

func TestBuildFilterRepoArguments(testingHandle *testing.T) {
	testingHandle.Run("dry run includes --dry-run", func(subTest *testing.T) {
		arguments := buildFilterRepoArguments("/tmp/mailmap", "/tmp/replace", false)
		if !containsArgument(arguments, "--dry-run") || !containsArgument(arguments, "--force") || !containsArgument(arguments, "--mailmap") {
			subTest.Errorf("unexpected dry-run arguments: %v", arguments)
		}
	})
	testingHandle.Run("apply omits --dry-run", func(subTest *testing.T) {
		arguments := buildFilterRepoArguments("/tmp/mailmap", "/tmp/replace", true)
		if containsArgument(arguments, "--dry-run") {
			subTest.Errorf("apply must not include --dry-run: %v", arguments)
		}
	})
	testingHandle.Run("no replace-text path omits the flag", func(subTest *testing.T) {
		arguments := buildFilterRepoArguments("/tmp/mailmap", "", true)
		if containsArgument(arguments, "--replace-text") {
			subTest.Errorf("expected no --replace-text flag: %v", arguments)
		}
	})
	testingHandle.Run("never contains push", func(subTest *testing.T) {
		arguments := buildFilterRepoArguments("/tmp/mailmap", "/tmp/replace", true)
		if containsArgument(arguments, "push") {
			subTest.Error("filter-repo arguments must never contain push")
		}
	})
}

func TestBuildPublishInstructions(testingHandle *testing.T) {
	testingHandle.Run("with a remote", func(subTest *testing.T) {
		instructions := buildPublishInstructions([]gitclient.Remote{{Name: "origin", FetchURL: "git@example.test:me/repo.git"}})
		if !strings.Contains(instructions, "force-with-lease") || !strings.Contains(instructions, "origin") {
			subTest.Errorf("unexpected instructions: %q", instructions)
		}
		if !strings.Contains(instructions, "yours to do") {
			subTest.Error("instructions should make clear the push is the user's to run")
		}
	})
	testingHandle.Run("without a remote", func(subTest *testing.T) {
		instructions := buildPublishInstructions(nil)
		if !strings.Contains(instructions, "No remote") {
			subTest.Errorf("expected a no-remote note, got %q", instructions)
		}
	})
}

func containsArgument(arguments []string, wanted string) bool {
	for _, argument := range arguments {
		if argument == wanted {
			return true
		}
	}
	return false
}

// errString is a tiny inline error helper.
type errString string

func (message errString) Error() string { return string(message) }
