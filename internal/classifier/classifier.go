// Package classifier assigns each occurrence a zone describing how safely it can
// be rewritten.
//
// The heuristic is deliberately narrow and honest:
//
//   - A commit reachable from any remote-tracking ref (refs/remotes/*) is on a
//     controlled remote. Rewriting it implies a force-push, which is the user's
//     to run.
//   - Any other commit is local-only and freely rewritable.
//   - The unreachable zone (forks, other people's clones, archives) is never
//     computed per commit, because it cannot be enumerated. The report warns
//     about it as a standing narrative whenever the repository has any remote.
//
// Remote-tracking refs can be stale. Callers may request a refresh (a fetch,
// which only reads from the remote); if the refresh fails, classification falls
// back to the last-known state with a warning rather than aborting.
package classifier

import (
	"fmt"

	"alive.name/internal/domain"
	"alive.name/internal/gitclient"
)

// RemoteReachabilitySource supplies the remote state the classifier needs. The
// *gitclient.GitClient satisfies it; tests can substitute a fake.
type RemoteReachabilitySource interface {
	ListRemotes() ([]gitclient.Remote, error)
	ListCommitsReachableFromRemoteTrackingRefs() ([]string, error)
	FetchAllRemotes() error
}

// Classifier answers zone questions about commits from a snapshot of remote state.
type Classifier struct {
	remoteReachableCommitHashes map[string]struct{}
	remoteCount                 int
	remoteTrackingRefsRefreshed bool
}

// New builds a Classifier from the current remote state. When
// refreshRemoteTrackingRefs is true it fetches first; a failed fetch does not
// abort; it is reported as a warning and classification proceeds against the
// last-known state.
func New(source RemoteReachabilitySource, refreshRemoteTrackingRefs bool) (*Classifier, []string, error) {
	warnings := make([]string, 0)
	refreshed := false

	if refreshRemoteTrackingRefs {
		if fetchError := source.FetchAllRemotes(); fetchError != nil {
			warnings = append(warnings, fmt.Sprintf(
				"could not refresh remote-tracking refs (%v); classification reflects the last-known remote state",
				fetchError,
			))
		} else {
			refreshed = true
		}
	}

	remotes, remotesError := source.ListRemotes()
	if remotesError != nil {
		return nil, warnings, fmt.Errorf("classifier: listing remotes: %w", remotesError)
	}

	reachableCommits, reachableError := source.ListCommitsReachableFromRemoteTrackingRefs()
	if reachableError != nil {
		return nil, warnings, fmt.Errorf("classifier: listing commits reachable from remotes: %w", reachableError)
	}

	remoteReachableCommitHashes := make(map[string]struct{}, len(reachableCommits))
	for _, commitHash := range reachableCommits {
		remoteReachableCommitHashes[commitHash] = struct{}{}
	}

	return &Classifier{
		remoteReachableCommitHashes: remoteReachableCommitHashes,
		remoteCount:                 len(remotes),
		remoteTrackingRefsRefreshed: refreshed,
	}, warnings, nil
}

// ZoneForCommit returns the zone for a commit. It never returns ZoneUnreachable,
// which is a narrative warning rather than a per-commit label.
func (classifier *Classifier) ZoneForCommit(commitHash string) domain.OccurrenceZone {
	if commitHash == "" {
		return domain.ZoneLocalRewritable
	}
	if _, isRemoteReachable := classifier.remoteReachableCommitHashes[commitHash]; isRemoteReachable {
		return domain.ZoneControlledRemote
	}
	return domain.ZoneLocalRewritable
}

// RepositoryHasAnyRemote reports whether the standing unreachable-zone narrative
// should be shown.
func (classifier *Classifier) RepositoryHasAnyRemote() bool {
	return classifier.remoteCount > 0
}

// RepositoryHasMultipleRemotes reports whether more than one remote is configured.
func (classifier *Classifier) RepositoryHasMultipleRemotes() bool {
	return classifier.remoteCount > 1
}

// RemoteTrackingRefsWereRefreshed reports whether a fetch successfully refreshed
// remote-tracking refs for this classification.
func (classifier *Classifier) RemoteTrackingRefsWereRefreshed() bool {
	return classifier.remoteTrackingRefsRefreshed
}
