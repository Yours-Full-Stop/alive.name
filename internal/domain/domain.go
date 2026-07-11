// Package domain holds the shared value types used across alive.name.
//
// These types carry no behaviour beyond small, self-contained helpers and
// deliberately depend on nothing else in the module, so every other package
// can import them without creating a cycle.
package domain

// OldIdentity is the set of names and emails a person is moving away from.
// Any single name or email is enough to match on; they are treated as a union.
type OldIdentity struct {
	Names  []string
	Emails []string
}

// NewIdentity is the single name and email a person is moving to.
type NewIdentity struct {
	Name  string
	Email string
}

// OccurrenceZone describes how reachable a given occurrence is, and therefore
// how safely it can be rewritten. See package classifier for how zones are
// assigned.
type OccurrenceZone int

const (
	// ZoneLocalRewritable marks an occurrence whose commit is reachable only
	// locally, from no remote-tracking ref. It can be rewritten freely.
	ZoneLocalRewritable OccurrenceZone = iota
	// ZoneControlledRemote marks an occurrence whose commit is reachable from
	// at least one remote-tracking ref. Rewriting it implies a force-push,
	// which is always the user's to run, never the tool's.
	ZoneControlledRemote
	// ZoneUnreachable is never assigned to an individual occurrence. Forks,
	// other people's clones, and archives cannot be enumerated programmatically,
	// so the tool warns about them as a standing narrative rather than
	// pretending to label them per commit.
	ZoneUnreachable
)

// String renders a zone in a short, human-readable form.
func (occurrenceZone OccurrenceZone) String() string {
	switch occurrenceZone {
	case ZoneLocalRewritable:
		return "local-rewritable"
	case ZoneControlledRemote:
		return "controlled-remote"
	case ZoneUnreachable:
		return "unreachable"
	default:
		return "unknown-zone"
	}
}

// OccurrenceLocation names where in the repository an old name or email was found.
type OccurrenceLocation int

const (
	LocationAuthorName OccurrenceLocation = iota
	LocationAuthorEmail
	LocationCommitterName
	LocationCommitterEmail
	LocationCommitMessage
	LocationAnnotatedTagMetadata
	LocationFileContent
)

// String renders a location in a short, human-readable form.
func (occurrenceLocation OccurrenceLocation) String() string {
	switch occurrenceLocation {
	case LocationAuthorName:
		return "author-name"
	case LocationAuthorEmail:
		return "author-email"
	case LocationCommitterName:
		return "committer-name"
	case LocationCommitterEmail:
		return "committer-email"
	case LocationCommitMessage:
		return "commit-message"
	case LocationAnnotatedTagMetadata:
		return "annotated-tag-metadata"
	case LocationFileContent:
		return "file-content"
	default:
		return "unknown-location"
	}
}

// DeadnameOccurrence is a single place an old name or email appears in the
// repository, together with enough context for a person to decide about it.
type DeadnameOccurrence struct {
	CommitHash string
	Location   OccurrenceLocation
	Zone       OccurrenceZone
	// FilePath is populated only when Location is LocationFileContent.
	FilePath           string
	MatchedText        string
	SurroundingContext string
}

// VerificationStatus records whether a backup has been verified against its
// source. Nothing destructive may run until a backup reports VerificationPassed.
type VerificationStatus int

const (
	VerificationNotYetRun VerificationStatus = iota
	VerificationPassed
	VerificationFailed
)

// String renders a verification status in a short, human-readable form.
func (verificationStatus VerificationStatus) String() string {
	switch verificationStatus {
	case VerificationNotYetRun:
		return "not-yet-run"
	case VerificationPassed:
		return "passed"
	case VerificationFailed:
		return "failed"
	default:
		return "unknown-verification-status"
	}
}
