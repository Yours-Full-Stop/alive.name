package domain

import "testing"

func TestOccurrenceZoneString(testingHandle *testing.T) {
	testCases := []struct {
		caseName       string
		category       string // "happy" | "negative" | "edge" | "error"
		zone           OccurrenceZone
		expectedString string
	}{
		{caseName: "local zone renders", category: "happy", zone: ZoneLocalRewritable, expectedString: "local-rewritable"},
		{caseName: "controlled remote zone renders", category: "happy", zone: ZoneControlledRemote, expectedString: "controlled-remote"},
		{caseName: "unreachable zone renders", category: "happy", zone: ZoneUnreachable, expectedString: "unreachable"},
		{caseName: "out of range zone renders sentinel", category: "edge", zone: OccurrenceZone(99), expectedString: "unknown-zone"},
		{caseName: "negative valued zone renders sentinel", category: "edge", zone: OccurrenceZone(-1), expectedString: "unknown-zone"},
	}
	for _, testCase := range testCases {
		testingHandle.Run(testCase.caseName, func(subTest *testing.T) {
			actualString := testCase.zone.String()
			if actualString != testCase.expectedString {
				subTest.Fatalf("zone %d: expected %q, got %q", int(testCase.zone), testCase.expectedString, actualString)
			}
		})
	}
}

func TestOccurrenceLocationString(testingHandle *testing.T) {
	testCases := []struct {
		caseName       string
		category       string
		location       OccurrenceLocation
		expectedString string
	}{
		{caseName: "author name renders", category: "happy", location: LocationAuthorName, expectedString: "author-name"},
		{caseName: "author email renders", category: "happy", location: LocationAuthorEmail, expectedString: "author-email"},
		{caseName: "committer name renders", category: "happy", location: LocationCommitterName, expectedString: "committer-name"},
		{caseName: "committer email renders", category: "happy", location: LocationCommitterEmail, expectedString: "committer-email"},
		{caseName: "commit message renders", category: "happy", location: LocationCommitMessage, expectedString: "commit-message"},
		{caseName: "annotated tag metadata renders", category: "happy", location: LocationAnnotatedTagMetadata, expectedString: "annotated-tag-metadata"},
		{caseName: "file content renders", category: "happy", location: LocationFileContent, expectedString: "file-content"},
		{caseName: "out of range location renders sentinel", category: "edge", location: OccurrenceLocation(99), expectedString: "unknown-location"},
	}
	for _, testCase := range testCases {
		testingHandle.Run(testCase.caseName, func(subTest *testing.T) {
			actualString := testCase.location.String()
			if actualString != testCase.expectedString {
				subTest.Fatalf("location %d: expected %q, got %q", int(testCase.location), testCase.expectedString, actualString)
			}
		})
	}
}

func TestVerificationStatusString(testingHandle *testing.T) {
	testCases := []struct {
		caseName       string
		category       string
		status         VerificationStatus
		expectedString string
	}{
		{caseName: "not yet run renders", category: "happy", status: VerificationNotYetRun, expectedString: "not-yet-run"},
		{caseName: "passed renders", category: "happy", status: VerificationPassed, expectedString: "passed"},
		{caseName: "failed renders", category: "happy", status: VerificationFailed, expectedString: "failed"},
		{caseName: "out of range status renders sentinel", category: "edge", status: VerificationStatus(42), expectedString: "unknown-verification-status"},
	}
	for _, testCase := range testCases {
		testingHandle.Run(testCase.caseName, func(subTest *testing.T) {
			actualString := testCase.status.String()
			if actualString != testCase.expectedString {
				subTest.Fatalf("status %d: expected %q, got %q", int(testCase.status), testCase.expectedString, actualString)
			}
		})
	}
}
