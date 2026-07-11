package report

import (
	"strings"
	"testing"

	"alive.name/internal/domain"
	"alive.name/internal/trace"
)

func TestRenderTraceReport(testingHandle *testing.T) {
	testCases := []struct {
		caseName            string
		category            string
		report              trace.Report
		expectedSubstrings  []string
		forbiddenSubstrings []string
	}{
		{
			caseName: "mixed zones render all sections",
			category: "happy",
			report: trace.Report{
				Occurrences: []domain.DeadnameOccurrence{
					{CommitHash: "aaaaaaaa1111", Location: domain.LocationAuthorName, Zone: domain.ZoneLocalRewritable, MatchedText: "Old Name", SurroundingContext: "Old Name"},
					{CommitHash: "bbbbbbbb2222", Location: domain.LocationCommitMessage, Zone: domain.ZoneControlledRemote, MatchedText: "Old Name", SurroundingContext: "work by Old Name"},
				},
				OccurrenceCountByZone: map[domain.OccurrenceZone]int{
					domain.ZoneLocalRewritable:  1,
					domain.ZoneControlledRemote: 1,
				},
				RepositoryHasAnyRemote: true,
			},
			expectedSubstrings: []string{
				"Found 2 occurrences",
				"Only here, on your machine",
				"On a remote you control",
				"force-push",
				"Beyond your reach",
				githubSensitiveDataURL,
				"--fetch",
			},
		},
		{
			caseName: "all local renders without remote sections",
			category: "negative",
			report: trace.Report{
				Occurrences: []domain.DeadnameOccurrence{
					{CommitHash: "aaaaaaaa1111", Location: domain.LocationAuthorName, Zone: domain.ZoneLocalRewritable, MatchedText: "Old Name", SurroundingContext: "Old Name"},
				},
				OccurrenceCountByZone:  map[domain.OccurrenceZone]int{domain.ZoneLocalRewritable: 1},
				RepositoryHasAnyRemote: false,
			},
			expectedSubstrings:  []string{"Found 1 occurrence", "Only here, on your machine"},
			forbiddenSubstrings: []string{"Beyond your reach", "On a remote you control", githubSensitiveDataURL},
		},
		{
			caseName:            "zero occurrences renders nothing-found",
			category:            "edge",
			report:              trace.Report{},
			expectedSubstrings:  []string{"Nothing found"},
			forbiddenSubstrings: []string{"Beyond your reach"},
		},
		{
			caseName: "refreshed remote state is stated as current",
			category: "edge",
			report: trace.Report{
				Occurrences:                     []domain.DeadnameOccurrence{{CommitHash: "cc", Location: domain.LocationCommitMessage, Zone: domain.ZoneControlledRemote, SurroundingContext: "x"}},
				OccurrenceCountByZone:           map[domain.OccurrenceZone]int{domain.ZoneControlledRemote: 1},
				RepositoryHasAnyRemote:          true,
				RemoteTrackingRefsWereRefreshed: true,
			},
			expectedSubstrings:  []string{"refreshed"},
			forbiddenSubstrings: []string{"--fetch"},
		},
		{
			caseName: "warnings are surfaced",
			category: "edge",
			report: trace.Report{
				Occurrences:           []domain.DeadnameOccurrence{{CommitHash: "dd", Location: domain.LocationAuthorName, Zone: domain.ZoneLocalRewritable, SurroundingContext: "x"}},
				OccurrenceCountByZone: map[domain.OccurrenceZone]int{domain.ZoneLocalRewritable: 1},
				Warnings:              []string{"a scan warning worth reading"},
			},
			expectedSubstrings: []string{"a scan warning worth reading"},
		},
	}
	for _, testCase := range testCases {
		testingHandle.Run(testCase.caseName, func(subTest *testing.T) {
			rendered := RenderTraceReport(testCase.report)
			for _, expectedSubstring := range testCase.expectedSubstrings {
				if !strings.Contains(rendered, expectedSubstring) {
					subTest.Errorf("expected rendered report to contain %q\n---\n%s", expectedSubstring, rendered)
				}
			}
			for _, forbiddenSubstring := range testCase.forbiddenSubstrings {
				if strings.Contains(rendered, forbiddenSubstring) {
					subTest.Errorf("expected rendered report NOT to contain %q\n---\n%s", forbiddenSubstring, rendered)
				}
			}
		})
	}
}

// TestRenderZeroValueReportDoesNotPanic covers the error-adjacent case of a
// malformed (zero-value, nil-map) report.
func TestRenderZeroValueReportDoesNotPanic(testingHandle *testing.T) {
	rendered := RenderTraceReport(trace.Report{})
	if !strings.Contains(rendered, "Nothing found") {
		testingHandle.Errorf("zero-value report should render nothing-found, got: %q", rendered)
	}
}
