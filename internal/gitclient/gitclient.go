// Package gitclient is the single place in alive.name that executes the system
// git binary. Every other package reaches git only through the named methods
// here.
//
// The package provides the tool's structural no-push guarantee. It is enforced
// three ways:
//
//  1. No method issues a push, and no push method exists.
//  2. Every git invocation is checked against an allowlist of subcommands that
//     deliberately does not contain "push"; anything off the list is refused.
//  3. Tests assert that "push" is rejected by the allowlist and that a spy
//     runner records no push across end-to-end flows.
//
// CloneMirrorOfRemote and the fetch helpers read from a remote and write only to
// local disk. They never push, so they are permitted.
package gitclient

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Field and record separators used when asking git for machine-readable output.
// The unit separator (0x1f) is used between fields and never appears in the
// git-provided values we parse; records are terminated with NUL via "-z".
const (
	gitUnitSeparator   = "\x1f"
	gitNulTerminator   = "\x00"
	gitLogPrettyFormat = "%H" + gitUnitSeparator +
		"%an" + gitUnitSeparator +
		"%ae" + gitUnitSeparator +
		"%cn" + gitUnitSeparator +
		"%ce" + gitUnitSeparator +
		"%B"
)

// allowedGitSubcommands is the complete set of git subcommands alive.name is
// permitted to run. "push" is deliberately absent, and so is every other
// operation that writes to a remote.
var allowedGitSubcommands = map[string]struct{}{
	"version":      {},
	"rev-parse":    {},
	"log":          {},
	"rev-list":     {},
	"cat-file":     {},
	"for-each-ref": {},
	"tag":          {},
	"remote":       {},
	"fsck":         {},
	"bundle":       {},
	"clone":        {},
	"status":       {},
	"fetch":        {},
	"ls-files":     {},
}

// GitSubcommandIsAllowed reports whether a git subcommand is on the allowlist.
// It is exported so the no-push guarantee can be asserted directly by tests.
func GitSubcommandIsAllowed(subcommand string) bool {
	_, isAllowed := allowedGitSubcommands[subcommand]
	return isAllowed
}

// GitInvocation is a single request to run git, as seen by a CommandRunner.
type GitInvocation struct {
	WorkingDirectory string
	Arguments        []string
}

// CommandRunner executes a git invocation. It is an interface so tests can
// substitute a spy that records invocations without touching a real git binary.
type CommandRunner interface {
	RunGit(invocation GitInvocation) (standardOutput []byte, standardError []byte, runError error)
}

// execCommandRunner runs the real system git binary.
type execCommandRunner struct{}

func (execCommandRunner) RunGit(invocation GitInvocation) ([]byte, []byte, error) {
	command := exec.Command("git", invocation.Arguments...)
	if invocation.WorkingDirectory != "" {
		command.Dir = invocation.WorkingDirectory
	}
	var standardOutputBuffer, standardErrorBuffer bytes.Buffer
	command.Stdout = &standardOutputBuffer
	command.Stderr = &standardErrorBuffer
	runError := command.Run()
	return standardOutputBuffer.Bytes(), standardErrorBuffer.Bytes(), runError
}

// GitClient runs git against a single repository through named operations only.
type GitClient struct {
	repositoryPath string
	runner         CommandRunner
}

// CommitMetadata is the identity-bearing metadata of a single commit.
type CommitMetadata struct {
	CommitHash     string
	AuthorName     string
	AuthorEmail    string
	CommitterName  string
	CommitterEmail string
	Message        string
}

// AnnotatedTag is a single annotated (not lightweight) tag and its metadata.
type AnnotatedTag struct {
	TagName     string
	TaggerName  string
	TaggerEmail string
	Message     string
	ObjectHash  string
	// TargetCommitHash is the commit the tag ultimately points at (the peeled
	// object). It lets callers classify a tag by the reachability of its target.
	TargetCommitHash string
}

// Remote is a single configured remote and its fetch URL.
type Remote struct {
	Name     string
	FetchURL string
}

// Reference is a single git ref and the object it points at.
type Reference struct {
	Name       string
	ObjectHash string
}

// ObjectEntry is one object from the repository's full object list, together
// with the path git associates with it (populated for blobs and trees).
type ObjectEntry struct {
	ObjectHash string
	Path       string
}

// NewGitClient creates a client for the repository at repositoryPath using the
// real git binary. It validates that the path exists as a directory but does
// not itself run git, so that a missing git binary surfaces through
// EnsureGitAvailable rather than here.
func NewGitClient(repositoryPath string) (*GitClient, error) {
	return NewGitClientWithRunner(repositoryPath, execCommandRunner{})
}

// NewGitClientWithRunner is like NewGitClient but runs git through the supplied
// runner. It exists so tests can inject a spy or fake; production code uses
// NewGitClient.
func NewGitClientWithRunner(repositoryPath string, runner CommandRunner) (*GitClient, error) {
	if strings.TrimSpace(repositoryPath) == "" {
		return nil, errors.New("gitclient: repository path must not be empty")
	}
	if runner == nil {
		return nil, errors.New("gitclient: command runner must not be nil")
	}
	pathInformation, statError := os.Stat(repositoryPath)
	if statError != nil {
		return nil, fmt.Errorf("gitclient: inspecting repository path %q: %w", repositoryPath, statError)
	}
	if !pathInformation.IsDir() {
		return nil, fmt.Errorf("gitclient: repository path %q is not a directory", repositoryPath)
	}
	return &GitClient{repositoryPath: repositoryPath, runner: runner}, nil
}

// RepositoryPath returns the repository this client operates on.
func (client *GitClient) RepositoryPath() string {
	return client.repositoryPath
}

// execute runs git in the given working directory after checking the subcommand
// against the allowlist. It is the sole choke point through which every git
// call in the package flows.
func (client *GitClient) execute(workingDirectory string, arguments ...string) ([]byte, error) {
	if len(arguments) == 0 {
		return nil, errors.New("gitclient: refusing to run git with no subcommand")
	}
	subcommand := arguments[0]
	if !GitSubcommandIsAllowed(subcommand) {
		return nil, fmt.Errorf("gitclient: git subcommand %q is not permitted; alive.name never runs it (this includes push)", subcommand)
	}
	standardOutput, standardError, runError := client.runner.RunGit(GitInvocation{
		WorkingDirectory: workingDirectory,
		Arguments:        arguments,
	})
	if runError != nil {
		trimmedStandardError := strings.TrimSpace(string(standardError))
		if trimmedStandardError == "" {
			return nil, fmt.Errorf("gitclient: running git %s: %w", subcommand, runError)
		}
		return nil, fmt.Errorf("gitclient: running git %s: %w: %s", subcommand, runError, trimmedStandardError)
	}
	return standardOutput, nil
}

// executeInRepository runs git inside this client's repository.
func (client *GitClient) executeInRepository(arguments ...string) ([]byte, error) {
	return client.execute(client.repositoryPath, arguments...)
}

// EnsureGitAvailable confirms the git binary can be executed, returning a clear
// error if git is not installed or not on PATH.
func (client *GitClient) EnsureGitAvailable() error {
	if _, lookupError := exec.LookPath("git"); lookupError != nil {
		return fmt.Errorf("gitclient: git does not appear to be installed or on PATH: %w", lookupError)
	}
	if _, runError := client.executeInRepository("version"); runError != nil {
		return fmt.Errorf("gitclient: git is present but could not be run: %w", runError)
	}
	return nil
}

// repositoryHasAnyCommits reports whether the repository contains at least one
// commit, so that readers can return empty results for a fresh repository rather
// than surfacing git's "does not have any commits yet" error.
func (client *GitClient) repositoryHasAnyCommits() (bool, error) {
	output, runError := client.executeInRepository("rev-list", "--all", "--max-count=1")
	if runError != nil {
		return false, fmt.Errorf("gitclient: checking for commits: %w", runError)
	}
	return strings.TrimSpace(string(output)) != "", nil
}

// ListAllCommitsWithMetadata returns identity metadata for every commit reachable
// from any ref. A repository with no commits yields an empty slice and no error.
func (client *GitClient) ListAllCommitsWithMetadata() ([]CommitMetadata, error) {
	hasCommits, commitCheckError := client.repositoryHasAnyCommits()
	if commitCheckError != nil {
		return nil, commitCheckError
	}
	if !hasCommits {
		return nil, nil
	}
	output, runError := client.executeInRepository("log", "--all", "-z", "--pretty=format:"+gitLogPrettyFormat)
	if runError != nil {
		return nil, fmt.Errorf("gitclient: listing commits: %w", runError)
	}
	commitRecords := splitOnNulTerminator(string(output))
	commitMetadataList := make([]CommitMetadata, 0, len(commitRecords))
	for _, commitRecord := range commitRecords {
		fields := strings.Split(commitRecord, gitUnitSeparator)
		if len(fields) < 6 {
			return nil, fmt.Errorf("gitclient: unexpected commit record with %d fields: %q", len(fields), commitRecord)
		}
		commitMetadataList = append(commitMetadataList, CommitMetadata{
			CommitHash:     fields[0],
			AuthorName:     fields[1],
			AuthorEmail:    fields[2],
			CommitterName:  fields[3],
			CommitterEmail: fields[4],
			Message:        fields[5],
		})
	}
	return commitMetadataList, nil
}

// ListAllObjectHashes returns the hash of every object reachable from any ref.
func (client *GitClient) ListAllObjectHashes() ([]string, error) {
	objectEntries, listError := client.ListAllObjectsWithPaths()
	if listError != nil {
		return nil, listError
	}
	objectHashes := make([]string, 0, len(objectEntries))
	for _, objectEntry := range objectEntries {
		objectHashes = append(objectHashes, objectEntry.ObjectHash)
	}
	return objectHashes, nil
}

// ListAllObjectsWithPaths returns every reachable object together with the path
// git associates with it. Blob objects carry their file path; this is what the
// deep file-content scan walks.
func (client *GitClient) ListAllObjectsWithPaths() ([]ObjectEntry, error) {
	hasCommits, commitCheckError := client.repositoryHasAnyCommits()
	if commitCheckError != nil {
		return nil, commitCheckError
	}
	if !hasCommits {
		return nil, nil
	}
	output, runError := client.executeInRepository("rev-list", "--all", "--objects")
	if runError != nil {
		return nil, fmt.Errorf("gitclient: listing objects: %w", runError)
	}
	objectEntries := make([]ObjectEntry, 0)
	for _, line := range strings.Split(string(output), "\n") {
		trimmedLine := strings.TrimRight(line, "\r")
		if trimmedLine == "" {
			continue
		}
		objectHash := trimmedLine
		objectPath := ""
		if separatorIndex := strings.IndexByte(trimmedLine, ' '); separatorIndex >= 0 {
			objectHash = trimmedLine[:separatorIndex]
			objectPath = trimmedLine[separatorIndex+1:]
		}
		objectEntries = append(objectEntries, ObjectEntry{ObjectHash: objectHash, Path: objectPath})
	}
	return objectEntries, nil
}

// ReadObjectType returns git's type for an object ("blob", "tree", "commit",
// "tag").
func (client *GitClient) ReadObjectType(objectHash string) (string, error) {
	output, runError := client.executeInRepository("cat-file", "-t", objectHash)
	if runError != nil {
		return "", fmt.Errorf("gitclient: reading type of object %s: %w", objectHash, runError)
	}
	return strings.TrimSpace(string(output)), nil
}

// ReadObjectContent returns the raw content of an object.
func (client *GitClient) ReadObjectContent(objectHash string) ([]byte, error) {
	output, runError := client.executeInRepository("cat-file", "-p", objectHash)
	if runError != nil {
		return nil, fmt.Errorf("gitclient: reading content of object %s: %w", objectHash, runError)
	}
	return output, nil
}

// ListAnnotatedTags returns every annotated tag with its tagger metadata and
// message. Lightweight tags are excluded because they carry no identity.
func (client *GitClient) ListAnnotatedTags() ([]AnnotatedTag, error) {
	const tagEnumerationFormat = "%(objecttype)" + gitUnitSeparator +
		"%(objectname)" + gitUnitSeparator +
		"%(refname:short)" + gitUnitSeparator +
		"%(taggername)" + gitUnitSeparator +
		"%(taggeremail)" + gitUnitSeparator +
		"%(*objectname)"
	output, runError := client.executeInRepository("for-each-ref", "--format="+tagEnumerationFormat, "refs/tags")
	if runError != nil {
		return nil, fmt.Errorf("gitclient: enumerating tags: %w", runError)
	}
	annotatedTags := make([]AnnotatedTag, 0)
	for _, line := range strings.Split(string(output), "\n") {
		trimmedLine := strings.TrimRight(line, "\r")
		if trimmedLine == "" {
			continue
		}
		fields := strings.Split(trimmedLine, gitUnitSeparator)
		if len(fields) < 6 {
			return nil, fmt.Errorf("gitclient: unexpected tag record with %d fields: %q", len(fields), trimmedLine)
		}
		objectType := fields[0]
		if objectType != "tag" {
			continue
		}
		tagName := fields[2]
		tagMessage, messageError := client.readAnnotatedTagMessage(tagName)
		if messageError != nil {
			return nil, messageError
		}
		annotatedTags = append(annotatedTags, AnnotatedTag{
			TagName:          tagName,
			TaggerName:       fields[3],
			TaggerEmail:      strings.Trim(fields[4], "<>"),
			Message:          tagMessage,
			ObjectHash:       fields[1],
			TargetCommitHash: fields[5],
		})
	}
	return annotatedTags, nil
}

// readAnnotatedTagMessage returns the message body of a single annotated tag,
// read separately so multi-line messages cannot corrupt the enumeration format.
func (client *GitClient) readAnnotatedTagMessage(tagName string) (string, error) {
	output, runError := client.executeInRepository("tag", "--list", tagName, "--format=%(contents)")
	if runError != nil {
		return "", fmt.Errorf("gitclient: reading message of tag %s: %w", tagName, runError)
	}
	return strings.TrimRight(string(output), "\n"), nil
}

// ListRemotes returns the configured remotes and their fetch URLs.
func (client *GitClient) ListRemotes() ([]Remote, error) {
	output, runError := client.executeInRepository("remote", "--verbose")
	if runError != nil {
		return nil, fmt.Errorf("gitclient: listing remotes: %w", runError)
	}
	remotesByName := make(map[string]string)
	remoteOrder := make([]string, 0)
	for _, line := range strings.Split(string(output), "\n") {
		trimmedLine := strings.TrimSpace(line)
		if trimmedLine == "" {
			continue
		}
		if !strings.HasSuffix(trimmedLine, "(fetch)") {
			continue
		}
		fields := strings.Fields(trimmedLine)
		if len(fields) < 2 {
			continue
		}
		remoteName := fields[0]
		remoteURL := fields[1]
		if _, alreadySeen := remotesByName[remoteName]; !alreadySeen {
			remoteOrder = append(remoteOrder, remoteName)
		}
		remotesByName[remoteName] = remoteURL
	}
	remotes := make([]Remote, 0, len(remoteOrder))
	for _, remoteName := range remoteOrder {
		remotes = append(remotes, Remote{Name: remoteName, FetchURL: remotesByName[remoteName]})
	}
	return remotes, nil
}

// ListCommitsReachableFromRemoteTrackingRefs returns the hashes of every commit
// reachable from any refs/remotes/* ref. It underpins zone classification: a
// commit in this set is on a controlled remote.
func (client *GitClient) ListCommitsReachableFromRemoteTrackingRefs() ([]string, error) {
	output, runError := client.executeInRepository("rev-list", "--remotes")
	if runError != nil {
		return nil, fmt.Errorf("gitclient: listing commits reachable from remotes: %w", runError)
	}
	commitHashes := make([]string, 0)
	for _, line := range strings.Split(string(output), "\n") {
		trimmedLine := strings.TrimSpace(line)
		if trimmedLine != "" {
			commitHashes = append(commitHashes, trimmedLine)
		}
	}
	return commitHashes, nil
}

// FetchAllRemotes refreshes remote-tracking refs from every remote. A fetch reads
// from the remote and writes only local refs; it never pushes.
func (client *GitClient) FetchAllRemotes() error {
	if _, runError := client.executeInRepository("fetch", "--all", "--prune"); runError != nil {
		return fmt.Errorf("gitclient: fetching all remotes: %w", runError)
	}
	return nil
}

// VerifyRepositoryIntegrity runs a full fsck and returns an error if the
// repository is corrupt.
func (client *GitClient) VerifyRepositoryIntegrity() error {
	if _, runError := client.executeInRepository("fsck", "--full"); runError != nil {
		return fmt.Errorf("gitclient: repository failed integrity check: %w", runError)
	}
	return nil
}

// CreateAllRefsBundle writes a bundle containing every ref to destinationPath.
func (client *GitClient) CreateAllRefsBundle(destinationPath string) error {
	if strings.TrimSpace(destinationPath) == "" {
		return errors.New("gitclient: bundle destination path must not be empty")
	}
	if _, runError := client.executeInRepository("bundle", "create", destinationPath, "--all"); runError != nil {
		return fmt.Errorf("gitclient: creating bundle at %q: %w", destinationPath, runError)
	}
	return nil
}

// VerifyBundle checks that a bundle file is well-formed and complete.
func (client *GitClient) VerifyBundle(bundlePath string) error {
	if _, runError := client.executeInRepository("bundle", "verify", bundlePath); runError != nil {
		return fmt.Errorf("gitclient: verifying bundle at %q: %w", bundlePath, runError)
	}
	return nil
}

// CloneMirrorOfRemote fetches a remote into a local mirror at destinationPath.
// It reads from the remote and writes only to local disk; it never pushes.
func (client *GitClient) CloneMirrorOfRemote(remoteURL, destinationPath string) error {
	if strings.TrimSpace(remoteURL) == "" {
		return errors.New("gitclient: remote URL must not be empty")
	}
	if strings.TrimSpace(destinationPath) == "" {
		return errors.New("gitclient: mirror destination path must not be empty")
	}
	// Run from an empty working directory: clone creates destinationPath itself.
	if _, runError := client.execute("", "clone", "--mirror", remoteURL, destinationPath); runError != nil {
		return fmt.Errorf("gitclient: cloning mirror of %q: %w", remoteURL, runError)
	}
	return nil
}

// WorkingTreeIsClean reports whether the working tree has no staged or unstaged
// changes to tracked files. Untracked files are deliberately ignored
// (--untracked-files=no): a history rewrite never touches them, so their
// presence is not a reason to refuse. Callers that want to tell the user about
// them anyway should use ListUntrackedFiles.
func (client *GitClient) WorkingTreeIsClean() (bool, error) {
	output, runError := client.executeInRepository("status", "--porcelain", "--untracked-files=no")
	if runError != nil {
		return false, fmt.Errorf("gitclient: reading working tree status: %w", runError)
	}
	return strings.TrimSpace(string(output)) == "", nil
}

// HasSignedCommits reports whether any commit reachable from any ref carries a
// signature. It is used to warn before a history rewrite, which breaks
// signatures. An empty repository has none.
func (client *GitClient) HasSignedCommits() (bool, error) {
	hasCommits, commitCheckError := client.repositoryHasAnyCommits()
	if commitCheckError != nil {
		return false, commitCheckError
	}
	if !hasCommits {
		return false, nil
	}
	output, runError := client.executeInRepository("log", "--all", "--format=%G?")
	if runError != nil {
		return false, fmt.Errorf("gitclient: checking for signed commits: %w", runError)
	}
	for _, line := range strings.Split(string(output), "\n") {
		signatureStatus := strings.TrimSpace(line)
		// %G? reports "N" for an unsigned commit; anything else is some kind of
		// signature (good, bad, expired, unverifiable, ...).
		if signatureStatus != "" && signatureStatus != "N" {
			return true, nil
		}
	}
	return false, nil
}

// ListReferences returns every ref in the repository with the object it points
// at. It is used to compare a backup against its source.
func (client *GitClient) ListReferences() ([]Reference, error) {
	const referenceFormat = "%(refname)" + gitUnitSeparator + "%(objectname)"
	output, runError := client.executeInRepository("for-each-ref", "--format="+referenceFormat)
	if runError != nil {
		return nil, fmt.Errorf("gitclient: listing references: %w", runError)
	}
	references := make([]Reference, 0)
	for _, line := range strings.Split(string(output), "\n") {
		trimmedLine := strings.TrimRight(line, "\r")
		if trimmedLine == "" {
			continue
		}
		fields := strings.Split(trimmedLine, gitUnitSeparator)
		if len(fields) < 2 {
			return nil, fmt.Errorf("gitclient: unexpected reference record: %q", trimmedLine)
		}
		references = append(references, Reference{Name: fields[0], ObjectHash: fields[1]})
	}
	return references, nil
}

// ResolveHeadCommit returns the commit hash that HEAD points at. When HEAD is
// unborn (a fresh repository with no commits) it returns an empty string and no
// error: --verify --quiet reports a missing HEAD as a non-zero exit with no
// output, which this method deliberately treats as "no HEAD yet" rather than a
// failure, so backup verification can handle an empty repository.
func (client *GitClient) ResolveHeadCommit() (string, error) {
	output, runError := client.executeInRepository("rev-parse", "--verify", "--quiet", "HEAD")
	if runError != nil {
		return "", nil
	}
	return strings.TrimSpace(string(output)), nil
}

// ListTrackedWorkingTreeFiles returns the repository-relative paths of every
// file currently tracked in the working tree. Paths use forward slashes.
func (client *GitClient) ListTrackedWorkingTreeFiles() ([]string, error) {
	output, runError := client.executeInRepository("ls-files", "-z")
	if runError != nil {
		return nil, fmt.Errorf("gitclient: listing tracked files: %w", runError)
	}
	return splitOnNulTerminator(string(output)), nil
}

// ListUntrackedFiles returns the repository-relative paths of files that are
// present in the working tree but neither tracked by git nor ignored.
// --exclude-standard honours .gitignore, .git/info/exclude, and the global
// ignore file, so files the user has already told git to ignore are not listed.
// Paths use forward slashes. A history rewrite leaves these files untouched;
// reclaim lists them so their presence is never a silent surprise.
func (client *GitClient) ListUntrackedFiles() ([]string, error) {
	output, runError := client.executeInRepository("ls-files", "--others", "--exclude-standard", "-z")
	if runError != nil {
		return nil, fmt.Errorf("gitclient: listing untracked files: %w", runError)
	}
	return splitOnNulTerminator(string(output)), nil
}

// splitOnNulTerminator splits git -z output into records, discarding the empty
// trailing element that a terminator leaves behind.
func splitOnNulTerminator(output string) []string {
	rawRecords := strings.Split(output, gitNulTerminator)
	records := make([]string, 0, len(rawRecords))
	for _, rawRecord := range rawRecords {
		if rawRecord != "" {
			records = append(records, rawRecord)
		}
	}
	return records
}
