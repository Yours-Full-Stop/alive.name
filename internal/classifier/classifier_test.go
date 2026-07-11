package classifier

import (
	"errors"
	"strings"
	"testing"

	"alive.name/internal/domain"
	"alive.name/internal/gitclient"
)

// fakeRemoteSource is a scripted RemoteReachabilitySource for the classifier.
type fakeRemoteSource struct {
	remotes            []gitclient.Remote
	reachableCommits   []string
	fetchError         error
	fetchWasCalled     bool
	listRemotesError   error
	listReachableError error
}

func (source *fakeRemoteSource) ListRemotes() ([]gitclient.Remote, error) {
	return source.remotes, source.listRemotesError
}

func (source *fakeRemoteSource) ListCommitsReachableFromRemoteTrackingRefs() ([]string, error) {
	return source.reachableCommits, source.listReachableError
}

func (source *fakeRemoteSource) FetchAllRemotes() error {
	source.fetchWasCalled = true
	return source.fetchError
}

func TestClassifierZoneAssignment(testingHandle *testing.T) {
	const remoteReachableCommit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const localOnlyCommit = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	testCases := []struct {
		caseName              string
		category              string
		source                *fakeRemoteSource
		refresh               bool
		commitToClassify      string
		expectedZone          domain.OccurrenceZone
		expectHasRemote       bool
		expectMultipleRemotes bool
		expectRefreshedFlag   bool
		expectWarning         bool
		expectFetchCalled     bool
	}{
		{
			caseName:         "commit on remote classifies as controlled-remote",
			category:         "happy",
			source:           &fakeRemoteSource{remotes: []gitclient.Remote{{Name: "origin"}}, reachableCommits: []string{remoteReachableCommit}},
			commitToClassify: remoteReachableCommit,
			expectedZone:     domain.ZoneControlledRemote,
			expectHasRemote:  true,
		},
		{
			caseName:         "local-only commit classifies as local",
			category:         "happy",
			source:           &fakeRemoteSource{remotes: []gitclient.Remote{{Name: "origin"}}, reachableCommits: []string{remoteReachableCommit}},
			commitToClassify: localOnlyCommit,
			expectedZone:     domain.ZoneLocalRewritable,
			expectHasRemote:  true,
		},
		{
			caseName:         "no remotes means everything local and no narrative",
			category:         "negative",
			source:           &fakeRemoteSource{},
			commitToClassify: remoteReachableCommit,
			expectedZone:     domain.ZoneLocalRewritable,
			expectHasRemote:  false,
		},
		{
			caseName:              "multiple remotes flagged and refresh succeeds",
			category:              "edge",
			source:                &fakeRemoteSource{remotes: []gitclient.Remote{{Name: "origin"}, {Name: "backup"}}, reachableCommits: []string{remoteReachableCommit}},
			refresh:               true,
			commitToClassify:      remoteReachableCommit,
			expectedZone:          domain.ZoneControlledRemote,
			expectHasRemote:       true,
			expectMultipleRemotes: true,
			expectRefreshedFlag:   true,
			expectFetchCalled:     true,
		},
		{
			caseName:            "failed fetch warns but does not abort",
			category:            "error",
			source:              &fakeRemoteSource{remotes: []gitclient.Remote{{Name: "origin"}}, reachableCommits: []string{remoteReachableCommit}, fetchError: errors.New("network down")},
			refresh:             true,
			commitToClassify:    remoteReachableCommit,
			expectedZone:        domain.ZoneControlledRemote,
			expectHasRemote:     true,
			expectRefreshedFlag: false,
			expectWarning:       true,
			expectFetchCalled:   true,
		},
	}
	for _, testCase := range testCases {
		testingHandle.Run(testCase.caseName, func(subTest *testing.T) {
			builtClassifier, warnings, constructionError := New(testCase.source, testCase.refresh)
			if constructionError != nil {
				subTest.Fatalf("unexpected construction error: %v", constructionError)
			}
			if zone := builtClassifier.ZoneForCommit(testCase.commitToClassify); zone != testCase.expectedZone {
				subTest.Errorf("zone: expected %v, got %v", testCase.expectedZone, zone)
			}
			if builtClassifier.RepositoryHasAnyRemote() != testCase.expectHasRemote {
				subTest.Errorf("has-remote: expected %v, got %v", testCase.expectHasRemote, builtClassifier.RepositoryHasAnyRemote())
			}
			if builtClassifier.RepositoryHasMultipleRemotes() != testCase.expectMultipleRemotes {
				subTest.Errorf("multiple-remotes: expected %v, got %v", testCase.expectMultipleRemotes, builtClassifier.RepositoryHasMultipleRemotes())
			}
			if builtClassifier.RemoteTrackingRefsWereRefreshed() != testCase.expectRefreshedFlag {
				subTest.Errorf("refreshed flag: expected %v, got %v", testCase.expectRefreshedFlag, builtClassifier.RemoteTrackingRefsWereRefreshed())
			}
			if (len(warnings) > 0) != testCase.expectWarning {
				subTest.Errorf("warning presence: expected %v, got %v", testCase.expectWarning, warnings)
			}
			if testCase.source.fetchWasCalled != testCase.expectFetchCalled {
				subTest.Errorf("fetch called: expected %v, got %v", testCase.expectFetchCalled, testCase.source.fetchWasCalled)
			}
		})
	}
}

func TestClassifierZoneForEmptyCommitHash(testingHandle *testing.T) {
	source := &fakeRemoteSource{remotes: []gitclient.Remote{{Name: "origin"}}, reachableCommits: []string{"aaaa"}}
	builtClassifier, _, constructionError := New(source, false)
	if constructionError != nil {
		testingHandle.Fatalf("unexpected error: %v", constructionError)
	}
	if zone := builtClassifier.ZoneForCommit(""); zone != domain.ZoneLocalRewritable {
		testingHandle.Errorf("empty commit hash should classify as local, got %v", zone)
	}
}

func TestClassifierConstructionErrors(testingHandle *testing.T) {
	testCases := []struct {
		caseName string
		source   *fakeRemoteSource
	}{
		{caseName: "list remotes error", source: &fakeRemoteSource{listRemotesError: errors.New("boom")}},
		{caseName: "list reachable error", source: &fakeRemoteSource{listReachableError: errors.New("boom")}},
	}
	for _, testCase := range testCases {
		testingHandle.Run(testCase.caseName, func(subTest *testing.T) {
			_, _, constructionError := New(testCase.source, false)
			if constructionError == nil {
				subTest.Fatal("expected a construction error")
			}
			if !strings.Contains(constructionError.Error(), "classifier:") {
				subTest.Errorf("error should be wrapped with package context, got: %v", constructionError)
			}
		})
	}
}
