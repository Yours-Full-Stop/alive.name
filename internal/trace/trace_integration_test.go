//go:build integration

package trace

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"alive.name/internal/domain"
	"alive.name/internal/fixturerepo"
	"alive.name/internal/gitclient"
)

// TestIntegrationWorkingTreeScan reads real files from disk, so it lives with the
// integration tests. It uses the fake reader for the git surface and real files
// for the working tree.
func TestIntegrationWorkingTreeScan(testingHandle *testing.T) {
	testingHandle.Run("finds a tracked file", func(subTest *testing.T) {
		repositoryPath := subTest.TempDir()
		if writeError := os.WriteFile(filepath.Join(repositoryPath, "notes.txt"), []byte("a note mentioning "+oldNameForTests+" here"), 0o644); writeError != nil {
			subTest.Fatalf("writing file: %v", writeError)
		}
		reader := &fakeReader{trackedFiles: []string{"notes.txt"}, repositoryPath: repositoryPath}
		traceReport, traceError := traceWithReader(reader, domain.OldIdentity{Names: []string{oldNameForTests}}, Options{IncludeWorkingTreeFileContents: true})
		if traceError != nil {
			subTest.Fatalf("trace: %v", traceError)
		}
		found := false
		for _, occurrence := range traceReport.Occurrences {
			if occurrence.Location == domain.LocationFileContent && occurrence.FilePath == "notes.txt" && occurrence.CommitHash == "" && occurrence.Zone == domain.ZoneLocalRewritable {
				found = true
			}
		}
		if !found {
			subTest.Errorf("expected a local working-tree occurrence, got %+v", traceReport.Occurrences)
		}
	})

	testingHandle.Run("missing tracked file is skipped", func(subTest *testing.T) {
		reader := &fakeReader{trackedFiles: []string{"deleted.txt"}, repositoryPath: subTest.TempDir()}
		traceReport, traceError := traceWithReader(reader, oldNameIdentity(), Options{IncludeWorkingTreeFileContents: true})
		if traceError != nil {
			subTest.Fatalf("trace should skip missing files: %v", traceError)
		}
		if len(traceReport.Occurrences) != 0 {
			subTest.Errorf("expected nothing, got %d", len(traceReport.Occurrences))
		}
	})
}

// These integration tests run the full trace pipeline against real repositories,
// confirming that the parsing the unit tests assume matches real git output.

func requireGitBinary(testingHandle *testing.T) {
	testingHandle.Helper()
	if _, lookupError := exec.LookPath("git"); lookupError != nil {
		testingHandle.Skip("git binary not available; skipping trace integration test")
	}
}

func newRealClient(testingHandle *testing.T, repositoryPath string) *gitclient.GitClient {
	testingHandle.Helper()
	client, constructionError := gitclient.NewGitClient(repositoryPath)
	if constructionError != nil {
		testingHandle.Fatalf("constructing client: %v", constructionError)
	}
	return client
}

func TestIntegrationTraceFindsAcrossLocations(testingHandle *testing.T) {
	requireGitBinary(testingHandle)
	repository := fixturerepo.New(testingHandle)
	repository.Commit(fixturerepo.CommitSpec{
		AuthorName:  oldNameForTests,
		AuthorEmail: oldEmailForTests,
		Message:     "initial work by " + oldNameForTests,
		Files:       map[string]string{"README.md": "authored by " + oldNameForTests + "\n"},
	})
	repository.AnnotatedTag(fixturerepo.AnnotatedTagSpec{
		TagName:     "v1.0.0",
		TaggerName:  oldNameForTests,
		TaggerEmail: oldEmailForTests,
		Message:     "released by " + oldNameForTests,
	})

	traceReport, traceError := OldNameAcrossRepository(
		newRealClient(testingHandle, repository.Path()),
		oldNameIdentity(),
		Options{IncludeHistoricalFileContents: true, IncludeWorkingTreeFileContents: true},
	)
	if traceError != nil {
		testingHandle.Fatalf("trace: %v", traceError)
	}
	present := locationsPresent(traceReport.Occurrences)
	for _, expectedLocation := range []domain.OccurrenceLocation{
		domain.LocationAuthorName,
		domain.LocationAuthorEmail,
		domain.LocationCommitMessage,
		domain.LocationAnnotatedTagMetadata,
		domain.LocationFileContent,
	} {
		if !present[expectedLocation] {
			testingHandle.Errorf("expected an occurrence at %v against real git", expectedLocation)
		}
	}
}

func TestIntegrationTraceControlledRemoteZone(testingHandle *testing.T) {
	requireGitBinary(testingHandle)
	repository := fixturerepo.New(testingHandle)
	repository.Commit(fixturerepo.CommitSpec{
		AuthorName:  oldNameForTests,
		AuthorEmail: oldEmailForTests,
		Message:     "shared work by " + oldNameForTests,
	})
	repository.PublishToNewBareRemote("origin")

	traceReport, traceError := OldNameAcrossRepository(
		newRealClient(testingHandle, repository.Path()),
		domain.OldIdentity{Names: []string{oldNameForTests}},
		Options{},
	)
	if traceError != nil {
		testingHandle.Fatalf("trace: %v", traceError)
	}
	if traceReport.OccurrenceCountByZone[domain.ZoneControlledRemote] == 0 {
		testingHandle.Errorf("expected controlled-remote occurrences against real remote, zones: %+v", traceReport.OccurrenceCountByZone)
	}
	if !traceReport.RepositoryHasAnyRemote {
		testingHandle.Error("expected RepositoryHasAnyRemote to be true")
	}
}
