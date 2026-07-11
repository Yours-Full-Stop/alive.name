package trace

import (
	"errors"
	"strings"
	"testing"

	"alive.name/internal/domain"
	"alive.name/internal/gitclient"
)

// Unit tests: the scanning logic is driven through a fake repositoryReader with
// in-memory data. Nothing here touches git or the filesystem. Working-tree
// scanning (which reads real files) and end-to-end behaviour are under the
// integration tag.

// fakeReader is a scripted repositoryReader.
type fakeReader struct {
	commits          []gitclient.CommitMetadata
	annotatedTags    []gitclient.AnnotatedTag
	objectEntries    []gitclient.ObjectEntry
	objectTypes      map[string]string
	objectContents   map[string][]byte
	trackedFiles     []string
	repositoryPath   string
	remotes          []gitclient.Remote
	reachableCommits []string
	fetchError       error

	commitsError    error
	tagsError       error
	objectsError    error
	filesError      error
	objectTypeError error
	contentError    error
}

func (reader *fakeReader) ListRemotes() ([]gitclient.Remote, error) { return reader.remotes, nil }
func (reader *fakeReader) ListCommitsReachableFromRemoteTrackingRefs() ([]string, error) {
	return reader.reachableCommits, nil
}
func (reader *fakeReader) FetchAllRemotes() error { return reader.fetchError }
func (reader *fakeReader) ListAllCommitsWithMetadata() ([]gitclient.CommitMetadata, error) {
	return reader.commits, reader.commitsError
}
func (reader *fakeReader) ListAnnotatedTags() ([]gitclient.AnnotatedTag, error) {
	return reader.annotatedTags, reader.tagsError
}
func (reader *fakeReader) ListAllObjectsWithPaths() ([]gitclient.ObjectEntry, error) {
	return reader.objectEntries, reader.objectsError
}
func (reader *fakeReader) ReadObjectType(objectHash string) (string, error) {
	if reader.objectTypeError != nil {
		return "", reader.objectTypeError
	}
	if objectType, present := reader.objectTypes[objectHash]; present {
		return objectType, nil
	}
	return "blob", nil
}
func (reader *fakeReader) ReadObjectContent(objectHash string) ([]byte, error) {
	if reader.contentError != nil {
		return nil, reader.contentError
	}
	return reader.objectContents[objectHash], nil
}
func (reader *fakeReader) ListTrackedWorkingTreeFiles() ([]string, error) {
	return reader.trackedFiles, reader.filesError
}
func (reader *fakeReader) RepositoryPath() string { return reader.repositoryPath }

const (
	oldNameForTests  = "Old Name"
	oldEmailForTests = "old.name@example.test"
)

func oldNameIdentity() domain.OldIdentity {
	return domain.OldIdentity{Names: []string{oldNameForTests}, Emails: []string{oldEmailForTests}}
}

func locationsPresent(occurrences []domain.DeadnameOccurrence) map[domain.OccurrenceLocation]bool {
	present := make(map[domain.OccurrenceLocation]bool)
	for _, occurrence := range occurrences {
		present[occurrence.Location] = true
	}
	return present
}

func TestTraceFindsAcrossLocations(testingHandle *testing.T) {
	const blobHash = "blobhash1"
	reader := &fakeReader{
		commits: []gitclient.CommitMetadata{{
			CommitHash:     "commit1",
			AuthorName:     oldNameForTests,
			AuthorEmail:    oldEmailForTests,
			CommitterName:  oldNameForTests,
			CommitterEmail: oldEmailForTests,
			Message:        "initial work by " + oldNameForTests,
		}},
		annotatedTags: []gitclient.AnnotatedTag{{
			TagName:          "v1.0.0",
			TaggerName:       oldNameForTests,
			TaggerEmail:      oldEmailForTests,
			Message:          "released by " + oldNameForTests,
			TargetCommitHash: "commit1",
		}},
		objectEntries:  []gitclient.ObjectEntry{{ObjectHash: blobHash, Path: "README.md"}},
		objectContents: map[string][]byte{blobHash: []byte("authored by " + oldNameForTests)},
	}

	traceReport, traceError := traceWithReader(reader, oldNameIdentity(), Options{IncludeHistoricalFileContents: true})
	if traceError != nil {
		testingHandle.Fatalf("trace: %v", traceError)
	}

	present := locationsPresent(traceReport.Occurrences)
	for _, expectedLocation := range []domain.OccurrenceLocation{
		domain.LocationAuthorName,
		domain.LocationAuthorEmail,
		domain.LocationCommitterName,
		domain.LocationCommitterEmail,
		domain.LocationCommitMessage,
		domain.LocationAnnotatedTagMetadata,
		domain.LocationFileContent,
	} {
		if !present[expectedLocation] {
			testingHandle.Errorf("expected an occurrence at %v, but none was found", expectedLocation)
		}
	}
	if traceReport.OccurrenceCountByZone[domain.ZoneControlledRemote] != 0 {
		testingHandle.Errorf("expected everything local without a remote, got %d controlled-remote", traceReport.OccurrenceCountByZone[domain.ZoneControlledRemote])
	}
	if traceReport.RepositoryHasAnyRemote {
		testingHandle.Error("expected RepositoryHasAnyRemote to be false")
	}
}

func TestTraceNegativeAbsent(testingHandle *testing.T) {
	reader := &fakeReader{
		commits: []gitclient.CommitMetadata{{CommitHash: "c1", AuthorName: "Someone Else", Message: "unrelated"}},
	}
	traceReport, traceError := traceWithReader(reader, domain.OldIdentity{Names: []string{"Absent Name"}}, Options{})
	if traceError != nil {
		testingHandle.Fatalf("trace: %v", traceError)
	}
	if len(traceReport.Occurrences) != 0 {
		testingHandle.Errorf("expected no occurrences, got %d", len(traceReport.Occurrences))
	}
}

func TestTraceEdgeCases(testingHandle *testing.T) {
	testingHandle.Run("substring inside a larger word is reported with context", func(subTest *testing.T) {
		const blobHash = "b1"
		reader := &fakeReader{
			objectEntries:  []gitclient.ObjectEntry{{ObjectHash: blobHash, Path: "notes.txt"}},
			objectContents: map[string][]byte{blobHash: []byte("the Announcement went out today")},
		}
		traceReport, traceError := traceWithReader(reader, domain.OldIdentity{Names: []string{"Ann"}}, Options{IncludeHistoricalFileContents: true})
		if traceError != nil {
			subTest.Fatalf("trace: %v", traceError)
		}
		if len(traceReport.Occurrences) == 0 || traceReport.Occurrences[0].SurroundingContext == "" {
			subTest.Fatalf("expected a substring match with context, got %+v", traceReport.Occurrences)
		}
	})

	testingHandle.Run("unicode name in a commit message", func(subTest *testing.T) {
		const unicodeName = "Óld Támé"
		reader := &fakeReader{commits: []gitclient.CommitMetadata{{CommitHash: "c1", Message: "written by " + unicodeName}}}
		traceReport, traceError := traceWithReader(reader, domain.OldIdentity{Names: []string{unicodeName}}, Options{})
		if traceError != nil {
			subTest.Fatalf("trace: %v", traceError)
		}
		if len(traceReport.Occurrences) == 0 {
			subTest.Error("expected the unicode name to be found")
		}
	})

	testingHandle.Run("case sensitivity toggle", func(subTest *testing.T) {
		reader := &fakeReader{commits: []gitclient.CommitMetadata{{CommitHash: "c1", AuthorName: "Old Name", Message: "work"}}}

		insensitive, insensitiveError := traceWithReader(reader, domain.OldIdentity{Names: []string{"old name"}}, Options{})
		if insensitiveError != nil {
			subTest.Fatalf("insensitive: %v", insensitiveError)
		}
		if len(insensitive.Occurrences) == 0 {
			subTest.Error("case-insensitive should match a differently-cased name")
		}

		sensitive, sensitiveError := traceWithReader(reader, domain.OldIdentity{Names: []string{"old name"}}, Options{CaseSensitive: true})
		if sensitiveError != nil {
			subTest.Fatalf("sensitive: %v", sensitiveError)
		}
		if len(sensitive.Occurrences) != 0 {
			subTest.Errorf("case-sensitive should not match, got %d", len(sensitive.Occurrences))
		}
	})

	testingHandle.Run("empty repository yields nothing", func(subTest *testing.T) {
		traceReport, traceError := traceWithReader(&fakeReader{}, oldNameIdentity(), Options{IncludeHistoricalFileContents: true})
		if traceError != nil {
			subTest.Fatalf("trace: %v", traceError)
		}
		if len(traceReport.Occurrences) != 0 {
			subTest.Errorf("expected nothing, got %d", len(traceReport.Occurrences))
		}
	})
}

func TestTraceControlledRemoteZone(testingHandle *testing.T) {
	reader := &fakeReader{
		commits:          []gitclient.CommitMetadata{{CommitHash: "sharedcommit", AuthorName: oldNameForTests, Message: "shared work by " + oldNameForTests}},
		remotes:          []gitclient.Remote{{Name: "origin"}},
		reachableCommits: []string{"sharedcommit"},
	}
	traceReport, traceError := traceWithReader(reader, domain.OldIdentity{Names: []string{oldNameForTests}}, Options{})
	if traceError != nil {
		testingHandle.Fatalf("trace: %v", traceError)
	}
	if traceReport.OccurrenceCountByZone[domain.ZoneControlledRemote] == 0 {
		testingHandle.Errorf("expected controlled-remote occurrences, zones: %+v", traceReport.OccurrenceCountByZone)
	}
	if !traceReport.RepositoryHasAnyRemote {
		testingHandle.Error("expected RepositoryHasAnyRemote to be true")
	}
}

func TestTraceDeepScanContentDetails(testingHandle *testing.T) {
	testingHandle.Run("long match yields ellipsised context", func(subTest *testing.T) {
		const blobHash = "big"
		longContent := strings.Repeat("padding words ", 20) + oldNameForTests + strings.Repeat(" trailing words", 20)
		reader := &fakeReader{
			objectEntries:  []gitclient.ObjectEntry{{ObjectHash: blobHash, Path: "big.txt"}},
			objectContents: map[string][]byte{blobHash: []byte(longContent)},
		}
		traceReport, traceError := traceWithReader(reader, domain.OldIdentity{Names: []string{oldNameForTests}}, Options{IncludeHistoricalFileContents: true})
		if traceError != nil {
			subTest.Fatalf("trace: %v", traceError)
		}
		if len(traceReport.Occurrences) == 0 || !strings.Contains(traceReport.Occurrences[0].SurroundingContext, "…") {
			subTest.Errorf("expected ellipsised context, got %+v", traceReport.Occurrences)
		}
	})

	testingHandle.Run("binary blob is skipped", func(subTest *testing.T) {
		const blobHash = "bin"
		reader := &fakeReader{
			objectEntries:  []gitclient.ObjectEntry{{ObjectHash: blobHash, Path: "blob.bin"}},
			objectContents: map[string][]byte{blobHash: []byte("\x00\x01 " + oldNameForTests + " \x00")},
		}
		traceReport, traceError := traceWithReader(reader, domain.OldIdentity{Names: []string{oldNameForTests}}, Options{IncludeHistoricalFileContents: true})
		if traceError != nil {
			subTest.Fatalf("trace: %v", traceError)
		}
		if len(traceReport.Occurrences) != 0 {
			subTest.Errorf("expected the binary blob to be skipped, got %+v", traceReport.Occurrences)
		}
	})

	testingHandle.Run("non-blob objects are skipped in deep scan", func(subTest *testing.T) {
		reader := &fakeReader{
			objectEntries: []gitclient.ObjectEntry{{ObjectHash: "treehash", Path: "somedir"}},
			objectTypes:   map[string]string{"treehash": "tree"},
		}
		traceReport, traceError := traceWithReader(reader, oldNameIdentity(), Options{IncludeHistoricalFileContents: true})
		if traceError != nil {
			subTest.Fatalf("trace: %v", traceError)
		}
		if len(traceReport.Occurrences) != 0 {
			subTest.Errorf("expected tree objects to be skipped, got %+v", traceReport.Occurrences)
		}
	})
}

func TestTraceRefreshFetch(testingHandle *testing.T) {
	testingHandle.Run("refresh succeeds", func(subTest *testing.T) {
		reader := &fakeReader{remotes: []gitclient.Remote{{Name: "origin"}}}
		traceReport, traceError := traceWithReader(reader, oldNameIdentity(), Options{RefreshRemoteTrackingRefs: true})
		if traceError != nil {
			subTest.Fatalf("trace: %v", traceError)
		}
		if !traceReport.RemoteTrackingRefsWereRefreshed {
			subTest.Error("expected refreshed flag to be set")
		}
	})

	testingHandle.Run("refresh failure warns but does not abort", func(subTest *testing.T) {
		reader := &fakeReader{remotes: []gitclient.Remote{{Name: "origin"}}, fetchError: errors.New("network down")}
		traceReport, traceError := traceWithReader(reader, oldNameIdentity(), Options{RefreshRemoteTrackingRefs: true})
		if traceError != nil {
			subTest.Fatalf("trace should not abort on fetch failure: %v", traceError)
		}
		if len(traceReport.Warnings) == 0 {
			subTest.Error("expected a warning about the failed refresh")
		}
	})
}

func TestTraceErrorPaths(testingHandle *testing.T) {
	testCases := []struct {
		caseName string
		reader   *fakeReader
		options  Options
	}{
		{caseName: "commit listing error", reader: &fakeReader{commitsError: errors.New("boom")}, options: Options{}},
		{caseName: "tag listing error", reader: &fakeReader{tagsError: errors.New("boom")}, options: Options{}},
		{caseName: "object listing error", reader: &fakeReader{objectsError: errors.New("boom")}, options: Options{IncludeHistoricalFileContents: true}},
		{caseName: "object type error", reader: &fakeReader{objectEntries: []gitclient.ObjectEntry{{ObjectHash: "h", Path: "f"}}, objectTypeError: errors.New("boom")}, options: Options{IncludeHistoricalFileContents: true}},
		{caseName: "object content error", reader: &fakeReader{objectEntries: []gitclient.ObjectEntry{{ObjectHash: "h", Path: "f"}}, contentError: errors.New("boom")}, options: Options{IncludeHistoricalFileContents: true}},
		{caseName: "tracked files error", reader: &fakeReader{filesError: errors.New("boom")}, options: Options{IncludeWorkingTreeFileContents: true}},
	}
	for _, testCase := range testCases {
		testingHandle.Run(testCase.caseName, func(subTest *testing.T) {
			if _, traceError := traceWithReader(testCase.reader, oldNameIdentity(), testCase.options); traceError == nil {
				subTest.Error("expected an error")
			}
		})
	}
}
