// Package trace finds every occurrence of an old name or email across a
// repository: commit metadata, commit messages, annotated tag metadata, and,
// when explicitly asked, historical and working-tree file contents.
//
// Tracing is strictly read-only; it never mutates the repository. Matching is
// case-insensitive by default (deadnames commonly appear in mixed case), with an
// opt-in case-sensitive mode.
package trace

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"alive.name/internal/classifier"
	"alive.name/internal/domain"
	"alive.name/internal/gitclient"
)

// contextWindowBytes is how much text on each side of a match is shown as
// surrounding context for long text (messages, file contents).
const contextWindowBytes = 48

// binarySniffLength is how many leading bytes are inspected to decide whether a
// blob is binary (and therefore skipped during content scans).
const binarySniffLength = 8000

// Options controls the breadth of a trace.
type Options struct {
	// IncludeHistoricalFileContents enables the slow deep scan of every blob in
	// history. Opt-in.
	IncludeHistoricalFileContents bool
	// IncludeWorkingTreeFileContents enables scanning the currently tracked files.
	IncludeWorkingTreeFileContents bool
	// CaseSensitive forces exact-case matching instead of the default
	// case-insensitive matching.
	CaseSensitive bool
	// RefreshRemoteTrackingRefs fetches before classifying so that zones reflect
	// current remote state. A fetch only reads; it never pushes.
	RefreshRemoteTrackingRefs bool
}

// Report is the result of a trace.
type Report struct {
	Occurrences                     []domain.DeadnameOccurrence
	OccurrenceCountByZone           map[domain.OccurrenceZone]int
	RepositoryHasAnyRemote          bool
	RepositoryHasMultipleRemotes    bool
	RemoteTrackingRefsWereRefreshed bool
	Warnings                        []string
}

// repositoryReader is the subset of *gitclient.GitClient that a trace needs. It
// is an interface so the classifier's needs and the reader's needs are explicit
// and so tests could substitute a fake; production passes a *gitclient.GitClient.
type repositoryReader interface {
	classifier.RemoteReachabilitySource
	ListAllCommitsWithMetadata() ([]gitclient.CommitMetadata, error)
	ListAnnotatedTags() ([]gitclient.AnnotatedTag, error)
	ListAllObjectsWithPaths() ([]gitclient.ObjectEntry, error)
	ReadObjectType(objectHash string) (string, error)
	ReadObjectContent(objectHash string) ([]byte, error)
	ListTrackedWorkingTreeFiles() ([]string, error)
	RepositoryPath() string
}

// OldNameAcrossRepository finds every occurrence of the old identity across the
// repository. It never mutates anything.
func OldNameAcrossRepository(client *gitclient.GitClient, oldIdentity domain.OldIdentity, options Options) (Report, error) {
	return traceWithReader(client, oldIdentity, options)
}

func traceWithReader(client repositoryReader, oldIdentity domain.OldIdentity, options Options) (Report, error) {
	matcher := newIdentityMatcher(oldIdentity, options.CaseSensitive)

	zoneClassifier, classifierWarnings, classifierError := classifier.New(client, options.RefreshRemoteTrackingRefs)
	if classifierError != nil {
		return Report{}, fmt.Errorf("trace: preparing zone classification: %w", classifierError)
	}

	occurrences := make([]domain.DeadnameOccurrence, 0)

	commitOccurrences, commitScanError := scanCommits(client, matcher, zoneClassifier)
	if commitScanError != nil {
		return Report{}, commitScanError
	}
	occurrences = append(occurrences, commitOccurrences...)

	tagOccurrences, tagScanError := scanAnnotatedTags(client, matcher, zoneClassifier)
	if tagScanError != nil {
		return Report{}, tagScanError
	}
	occurrences = append(occurrences, tagOccurrences...)

	if options.IncludeHistoricalFileContents {
		historicalOccurrences, historicalScanError := scanHistoricalFileContents(client, matcher, zoneClassifier)
		if historicalScanError != nil {
			return Report{}, historicalScanError
		}
		occurrences = append(occurrences, historicalOccurrences...)
	}

	if options.IncludeWorkingTreeFileContents {
		workingTreeOccurrences, workingTreeScanError := scanWorkingTreeFileContents(client, matcher)
		if workingTreeScanError != nil {
			return Report{}, workingTreeScanError
		}
		occurrences = append(occurrences, workingTreeOccurrences...)
	}

	occurrenceCountByZone := make(map[domain.OccurrenceZone]int)
	for _, occurrence := range occurrences {
		occurrenceCountByZone[occurrence.Zone]++
	}

	return Report{
		Occurrences:                     occurrences,
		OccurrenceCountByZone:           occurrenceCountByZone,
		RepositoryHasAnyRemote:          zoneClassifier.RepositoryHasAnyRemote(),
		RepositoryHasMultipleRemotes:    zoneClassifier.RepositoryHasMultipleRemotes(),
		RemoteTrackingRefsWereRefreshed: zoneClassifier.RemoteTrackingRefsWereRefreshed(),
		Warnings:                        classifierWarnings,
	}, nil
}

func scanCommits(client repositoryReader, matcher identityMatcher, zoneClassifier *classifier.Classifier) ([]domain.DeadnameOccurrence, error) {
	commits, commitsError := client.ListAllCommitsWithMetadata()
	if commitsError != nil {
		return nil, fmt.Errorf("trace: reading commits: %w", commitsError)
	}
	occurrences := make([]domain.DeadnameOccurrence, 0)
	for _, commit := range commits {
		zone := zoneClassifier.ZoneForCommit(commit.CommitHash)
		occurrences = append(occurrences, matcher.findInShortField(commit.CommitHash, "", zone, domain.LocationAuthorName, commit.AuthorName, matcher.nameTerms)...)
		occurrences = append(occurrences, matcher.findInShortField(commit.CommitHash, "", zone, domain.LocationAuthorEmail, commit.AuthorEmail, matcher.emailTerms)...)
		occurrences = append(occurrences, matcher.findInShortField(commit.CommitHash, "", zone, domain.LocationCommitterName, commit.CommitterName, matcher.nameTerms)...)
		occurrences = append(occurrences, matcher.findInShortField(commit.CommitHash, "", zone, domain.LocationCommitterEmail, commit.CommitterEmail, matcher.emailTerms)...)
		occurrences = append(occurrences, matcher.findInLongText(commit.CommitHash, "", zone, domain.LocationCommitMessage, commit.Message, matcher.combinedTerms)...)
	}
	return occurrences, nil
}

func scanAnnotatedTags(client repositoryReader, matcher identityMatcher, zoneClassifier *classifier.Classifier) ([]domain.DeadnameOccurrence, error) {
	annotatedTags, tagsError := client.ListAnnotatedTags()
	if tagsError != nil {
		return nil, fmt.Errorf("trace: reading annotated tags: %w", tagsError)
	}
	occurrences := make([]domain.DeadnameOccurrence, 0)
	for _, annotatedTag := range annotatedTags {
		zone := zoneClassifier.ZoneForCommit(annotatedTag.TargetCommitHash)
		commitHashForOccurrence := annotatedTag.TargetCommitHash
		occurrences = append(occurrences, matcher.findInShortField(commitHashForOccurrence, "", zone, domain.LocationAnnotatedTagMetadata, annotatedTag.TaggerName, matcher.nameTerms)...)
		occurrences = append(occurrences, matcher.findInShortField(commitHashForOccurrence, "", zone, domain.LocationAnnotatedTagMetadata, annotatedTag.TaggerEmail, matcher.emailTerms)...)
		occurrences = append(occurrences, matcher.findInLongText(commitHashForOccurrence, "", zone, domain.LocationAnnotatedTagMetadata, annotatedTag.Message, matcher.combinedTerms)...)
	}
	return occurrences, nil
}

// scanHistoricalFileContents reads every unique blob in history and searches its
// text. A blob can live in many commits, so its zone is treated conservatively:
// if the repository has any remote, the match is labelled controlled-remote
// (the safe, force-push-implying direction) rather than under-warning.
func scanHistoricalFileContents(client repositoryReader, matcher identityMatcher, zoneClassifier *classifier.Classifier) ([]domain.DeadnameOccurrence, error) {
	objectEntries, objectsError := client.ListAllObjectsWithPaths()
	if objectsError != nil {
		return nil, fmt.Errorf("trace: listing objects for deep scan: %w", objectsError)
	}
	historicalZone := domain.ZoneLocalRewritable
	if zoneClassifier.RepositoryHasAnyRemote() {
		historicalZone = domain.ZoneControlledRemote
	}
	occurrences := make([]domain.DeadnameOccurrence, 0)
	alreadyScannedBlob := make(map[string]struct{})
	for _, objectEntry := range objectEntries {
		if objectEntry.Path == "" {
			continue
		}
		if _, scanned := alreadyScannedBlob[objectEntry.ObjectHash]; scanned {
			continue
		}
		objectType, typeError := client.ReadObjectType(objectEntry.ObjectHash)
		if typeError != nil {
			return nil, fmt.Errorf("trace: reading object type during deep scan: %w", typeError)
		}
		if objectType != "blob" {
			continue
		}
		alreadyScannedBlob[objectEntry.ObjectHash] = struct{}{}
		content, contentError := client.ReadObjectContent(objectEntry.ObjectHash)
		if contentError != nil {
			return nil, fmt.Errorf("trace: reading object content during deep scan: %w", contentError)
		}
		if looksBinary(content) {
			continue
		}
		occurrences = append(occurrences, matcher.findInLongText(objectEntry.ObjectHash, objectEntry.Path, historicalZone, domain.LocationFileContent, string(content), matcher.combinedTerms)...)
	}
	return occurrences, nil
}

func scanWorkingTreeFileContents(client repositoryReader, matcher identityMatcher) ([]domain.DeadnameOccurrence, error) {
	trackedFiles, filesError := client.ListTrackedWorkingTreeFiles()
	if filesError != nil {
		return nil, fmt.Errorf("trace: listing tracked files: %w", filesError)
	}
	repositoryPath := client.RepositoryPath()
	occurrences := make([]domain.DeadnameOccurrence, 0)
	for _, relativePath := range trackedFiles {
		absolutePath := filepath.Join(repositoryPath, filepath.FromSlash(relativePath))
		content, readError := os.ReadFile(absolutePath)
		if readError != nil {
			// A tracked file may be absent from the working tree (for example
			// after a delete that is not yet committed). Skip it rather than
			// failing the whole scan.
			continue
		}
		if looksBinary(content) {
			continue
		}
		occurrences = append(occurrences, matcher.findInLongText("", relativePath, domain.ZoneLocalRewritable, domain.LocationFileContent, string(content), matcher.combinedTerms)...)
	}
	return occurrences, nil
}

// identityMatcher holds the search terms and the case-sensitivity policy.
type identityMatcher struct {
	nameTerms     []string
	emailTerms    []string
	combinedTerms []string
	caseSensitive bool
}

func newIdentityMatcher(oldIdentity domain.OldIdentity, caseSensitive bool) identityMatcher {
	combinedTerms := make([]string, 0, len(oldIdentity.Names)+len(oldIdentity.Emails))
	combinedTerms = append(combinedTerms, oldIdentity.Names...)
	combinedTerms = append(combinedTerms, oldIdentity.Emails...)
	return identityMatcher{
		nameTerms:     oldIdentity.Names,
		emailTerms:    oldIdentity.Emails,
		combinedTerms: combinedTerms,
		caseSensitive: caseSensitive,
	}
}

// findInShortField searches a short field (a name or email) and uses the whole
// field as the surrounding context.
func (matcher identityMatcher) findInShortField(commitHash, filePath string, zone domain.OccurrenceZone, location domain.OccurrenceLocation, fieldText string, terms []string) []domain.DeadnameOccurrence {
	occurrences := make([]domain.DeadnameOccurrence, 0)
	for _, term := range terms {
		if _, found := findTerm(fieldText, term, matcher.caseSensitive); found {
			occurrences = append(occurrences, domain.DeadnameOccurrence{
				CommitHash:         commitHash,
				Location:           location,
				Zone:               zone,
				FilePath:           filePath,
				MatchedText:        term,
				SurroundingContext: collapseWhitespace(fieldText),
			})
		}
	}
	return occurrences
}

// findInLongText searches a long body of text and reports a windowed context
// around the first match of each term.
func (matcher identityMatcher) findInLongText(commitHash, filePath string, zone domain.OccurrenceZone, location domain.OccurrenceLocation, text string, terms []string) []domain.DeadnameOccurrence {
	occurrences := make([]domain.DeadnameOccurrence, 0)
	for _, term := range terms {
		matchByteIndex, found := findTerm(text, term, matcher.caseSensitive)
		if !found {
			continue
		}
		occurrences = append(occurrences, domain.DeadnameOccurrence{
			CommitHash:         commitHash,
			Location:           location,
			Zone:               zone,
			FilePath:           filePath,
			MatchedText:        term,
			SurroundingContext: extractWindowedContext(text, matchByteIndex, len(term)),
		})
	}
	return occurrences
}

// findTerm returns the byte index of the first occurrence of term in text,
// honouring the case-sensitivity policy. For case-insensitive matching both are
// lower-cased; for typical inputs the returned index remains valid against the
// original text, and callers that slice the original clamp to rune boundaries.
func findTerm(text, term string, caseSensitive bool) (int, bool) {
	if term == "" {
		return 0, false
	}
	haystack := text
	needle := term
	if !caseSensitive {
		haystack = strings.ToLower(text)
		needle = strings.ToLower(term)
	}
	index := strings.Index(haystack, needle)
	if index < 0 {
		return 0, false
	}
	return index, true
}

func looksBinary(content []byte) bool {
	sniffLength := len(content)
	if sniffLength > binarySniffLength {
		sniffLength = binarySniffLength
	}
	return bytes.IndexByte(content[:sniffLength], 0) >= 0
}

// extractWindowedContext returns a single-line snippet of text around a match,
// clamped to valid rune boundaries and with surrounding whitespace collapsed.
func extractWindowedContext(text string, matchByteIndex, matchLength int) string {
	if matchByteIndex < 0 || matchByteIndex > len(text) {
		return collapseWhitespace(text)
	}
	start := clampToRuneStart(text, matchByteIndex-contextWindowBytes)
	end := clampToRuneEnd(text, matchByteIndex+matchLength+contextWindowBytes)
	snippet := collapseWhitespace(text[start:end])
	if start > 0 {
		snippet = "…" + snippet
	}
	if end < len(text) {
		snippet = snippet + "…"
	}
	return snippet
}

func clampToRuneStart(text string, byteIndex int) int {
	if byteIndex <= 0 {
		return 0
	}
	if byteIndex >= len(text) {
		return len(text)
	}
	for byteIndex > 0 && !utf8.RuneStart(text[byteIndex]) {
		byteIndex--
	}
	return byteIndex
}

func clampToRuneEnd(text string, byteIndex int) int {
	if byteIndex >= len(text) {
		return len(text)
	}
	if byteIndex <= 0 {
		return 0
	}
	for byteIndex < len(text) && !utf8.RuneStart(text[byteIndex]) {
		byteIndex++
	}
	return byteIndex
}

// collapseWhitespace turns any run of whitespace (including newlines) into a
// single space and trims the ends, so contexts render on one tidy line.
func collapseWhitespace(text string) string {
	return strings.Join(strings.Fields(text), " ")
}
