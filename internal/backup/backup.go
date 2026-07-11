// Package backup creates, verifies, lists, restores, and removes backups of a
// repository. It is the load-bearing safety module: nothing destructive
// elsewhere runs until CreateVerified has returned a Record whose
// VerificationStatus is domain.VerificationPassed.
//
// Restore is deliberately split to honour the project's no-push rule. A local
// restore copies files back and runs directly. A remote restore is never
// executed: PrepareRemoteRestoreCommand returns the exact command for the user
// to run themselves.
package backup

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	ignore "github.com/sabhiram/go-gitignore"

	"alive.name/internal/domain"
	"alive.name/internal/gitclient"
)

const (
	recordFileName       = "record.json"
	repositoryCopyDir    = "repository"
	allRefsBundleName    = "all-refs.bundle"
	remoteMirrorDirName  = "remote-mirror.git"
	aliveIgnoreFileName  = ".aliveignore"
	gitDirectoryName     = ".git"
	identifierTimeLayout = "20060102-150405"
)

// Options controls how a backup is created.
type Options struct {
	// StateDirectoryPath is where backups and their records live. It must be
	// outside the working tree being backed up.
	StateDirectoryPath string
	// IncludeRemoteMirror additionally clones a local mirror of the first remote,
	// when one exists.
	IncludeRemoteMirror bool
	// BundleOnly skips the full recursive copy and keeps only the all-refs
	// bundle, for very large repositories.
	BundleOnly bool
}

// Record describes a single backup.
type Record struct {
	Identifier          string                    `json:"identifier"`
	RepositoryPath      string                    `json:"repositoryPath"`
	BackupDirectoryPath string                    `json:"backupDirectoryPath"`
	BundleFilePath      string                    `json:"bundleFilePath"`
	RemoteMirrorPath    string                    `json:"remoteMirrorPath"`
	CreatedAt           time.Time                 `json:"createdAt"`
	VerificationStatus  domain.VerificationStatus `json:"verificationStatus"`
}

// repositoryVerifier is the read-only surface used to check a repository copy.
type repositoryVerifier interface {
	VerifyRepositoryIntegrity() error
	ListReferences() ([]gitclient.Reference, error)
	ResolveHeadCommit() (string, error)
}

// sourceRepository is what CreateVerified needs from the repository being backed
// up. *gitclient.GitClient satisfies it.
type sourceRepository interface {
	repositoryVerifier
	RepositoryPath() string
	CreateAllRefsBundle(destinationPath string) error
	VerifyBundle(bundlePath string) error
	ListRemotes() ([]gitclient.Remote, error)
	CloneMirrorOfRemote(remoteURL, destinationPath string) error
}

// verifierFactory builds a verifier for a repository path (the backup copy).
type verifierFactory func(repositoryPath string) (repositoryVerifier, error)

func defaultVerifierFactory(repositoryPath string) (repositoryVerifier, error) {
	return gitclient.NewGitClient(repositoryPath)
}

// CreateVerified creates a backup and verifies it against the source. It returns
// a Record with VerificationPassed only when every verification step succeeds;
// otherwise it returns the (failed) record and an error, so callers stop.
func CreateVerified(client *gitclient.GitClient, options Options) (Record, error) {
	return createVerified(client, defaultVerifierFactory, options)
}

func createVerified(source sourceRepository, verifierForPath verifierFactory, options Options) (Record, error) {
	if strings.TrimSpace(options.StateDirectoryPath) == "" {
		return Record{}, errors.New("backup: a state directory path is required")
	}
	repositoryPath := source.RepositoryPath()
	if guardError := ensureBackupIsOutsideRepository(repositoryPath, options.StateDirectoryPath); guardError != nil {
		return Record{}, guardError
	}

	identifier, identifierError := generateIdentifier()
	if identifierError != nil {
		return Record{}, identifierError
	}
	backupContainerPath := filepath.Join(options.StateDirectoryPath, identifier)
	if makeError := os.MkdirAll(backupContainerPath, 0o755); makeError != nil {
		return Record{}, fmt.Errorf("backup: creating backup directory: %w", makeError)
	}

	record := Record{
		Identifier:         identifier,
		RepositoryPath:     repositoryPath,
		CreatedAt:          time.Now().UTC(),
		VerificationStatus: domain.VerificationNotYetRun,
	}

	if !options.BundleOnly {
		copyDestination := filepath.Join(backupContainerPath, repositoryCopyDir)
		ignoreMatcher, ignoreError := loadAliveIgnore(repositoryPath)
		if ignoreError != nil {
			return record, ignoreError
		}
		if copyError := copyDirectoryTree(repositoryPath, copyDestination, ignoreMatcher); copyError != nil {
			return record, fmt.Errorf("backup: copying repository: %w", copyError)
		}
		record.BackupDirectoryPath = copyDestination
	}

	bundlePath := filepath.Join(backupContainerPath, allRefsBundleName)
	if bundleError := source.CreateAllRefsBundle(bundlePath); bundleError != nil {
		return record, fmt.Errorf("backup: creating bundle: %w", bundleError)
	}
	record.BundleFilePath = bundlePath

	if options.IncludeRemoteMirror {
		mirrorPath, mirrorError := createRemoteMirror(source, backupContainerPath)
		if mirrorError != nil {
			return record, mirrorError
		}
		record.RemoteMirrorPath = mirrorPath
	}

	if verificationError := runVerification(source, verifierForPath, record, options); verificationError != nil {
		record.VerificationStatus = domain.VerificationFailed
		if persistError := writeRecordFile(backupContainerPath, record); persistError != nil {
			return record, fmt.Errorf("backup: verification failed (%v) and the failed record could not be written: %w", verificationError, persistError)
		}
		return record, fmt.Errorf("backup: verification failed: %w", verificationError)
	}

	record.VerificationStatus = domain.VerificationPassed
	if persistError := writeRecordFile(backupContainerPath, record); persistError != nil {
		return record, fmt.Errorf("backup: writing record: %w", persistError)
	}
	return record, nil
}

func createRemoteMirror(source sourceRepository, backupContainerPath string) (string, error) {
	remotes, remotesError := source.ListRemotes()
	if remotesError != nil {
		return "", fmt.Errorf("backup: listing remotes for mirror: %w", remotesError)
	}
	if len(remotes) == 0 {
		// No remote to mirror is not an error; there is simply nothing to do.
		return "", nil
	}
	mirrorPath := filepath.Join(backupContainerPath, remoteMirrorDirName)
	if mirrorError := source.CloneMirrorOfRemote(remotes[0].FetchURL, mirrorPath); mirrorError != nil {
		return "", fmt.Errorf("backup: mirroring remote %q: %w", remotes[0].Name, mirrorError)
	}
	return mirrorPath, nil
}

// runVerification is the gate. It verifies the bundle and, for a full copy,
// checks the copy's integrity and that its references and HEAD match the source.
func runVerification(source sourceRepository, verifierForPath verifierFactory, record Record, options Options) error {
	if verifyBundleError := source.VerifyBundle(record.BundleFilePath); verifyBundleError != nil {
		return fmt.Errorf("bundle verification failed: %w", verifyBundleError)
	}
	if options.BundleOnly {
		return nil
	}
	copyVerifier, verifierError := verifierForPath(record.BackupDirectoryPath)
	if verifierError != nil {
		return fmt.Errorf("opening backup copy for verification: %w", verifierError)
	}
	return verifyCopyAgainstSource(source, copyVerifier)
}

func verifyCopyAgainstSource(source sourceRepository, copyVerifier repositoryVerifier) error {
	if integrityError := copyVerifier.VerifyRepositoryIntegrity(); integrityError != nil {
		return fmt.Errorf("backup copy failed its integrity check: %w", integrityError)
	}

	sourceReferences, sourceReferencesError := source.ListReferences()
	if sourceReferencesError != nil {
		return fmt.Errorf("reading source references: %w", sourceReferencesError)
	}
	copyReferences, copyReferencesError := copyVerifier.ListReferences()
	if copyReferencesError != nil {
		return fmt.Errorf("reading backup references: %w", copyReferencesError)
	}
	if !referencesEqual(sourceReferences, copyReferences) {
		return errors.New("the backup's references do not match the source")
	}

	sourceHead, sourceHeadError := source.ResolveHeadCommit()
	if sourceHeadError != nil {
		return fmt.Errorf("reading source HEAD: %w", sourceHeadError)
	}
	copyHead, copyHeadError := copyVerifier.ResolveHeadCommit()
	if copyHeadError != nil {
		return fmt.Errorf("reading backup HEAD: %w", copyHeadError)
	}
	if sourceHead != copyHead {
		return fmt.Errorf("the backup's HEAD (%q) does not match the source (%q)", copyHead, sourceHead)
	}
	return nil
}

func referencesEqual(firstReferences, secondReferences []gitclient.Reference) bool {
	if len(firstReferences) != len(secondReferences) {
		return false
	}
	sortReferences := func(references []gitclient.Reference) []gitclient.Reference {
		sorted := make([]gitclient.Reference, len(references))
		copy(sorted, references)
		sort.Slice(sorted, func(firstIndex, secondIndex int) bool {
			return sorted[firstIndex].Name < sorted[secondIndex].Name
		})
		return sorted
	}
	sortedFirst := sortReferences(firstReferences)
	sortedSecond := sortReferences(secondReferences)
	for index := range sortedFirst {
		if sortedFirst[index] != sortedSecond[index] {
			return false
		}
	}
	return true
}

// List returns every backup recorded under a state directory, most recent first.
func List(stateDirectoryPath string) ([]Record, error) {
	entries, readError := os.ReadDir(stateDirectoryPath)
	if readError != nil {
		if errors.Is(readError, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("backup: reading state directory: %w", readError)
	}
	records := make([]Record, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		recordPath := filepath.Join(stateDirectoryPath, entry.Name(), recordFileName)
		record, recordError := readRecordFile(recordPath)
		if recordError != nil {
			if errors.Is(recordError, fs.ErrNotExist) {
				continue
			}
			return nil, recordError
		}
		records = append(records, record)
	}
	sort.Slice(records, func(firstIndex, secondIndex int) bool {
		return records[firstIndex].CreatedAt.After(records[secondIndex].CreatedAt)
	})
	return records, nil
}

// PrepareLocalRestore copies a backup's repository copy to destinationPath. It
// runs directly because a local restore involves no remote and therefore no push.
func PrepareLocalRestore(backupRecord Record, destinationPath string) error {
	if backupRecord.BackupDirectoryPath == "" {
		return errors.New("backup: this backup is bundle-only and has no file copy to restore; restore from the bundle instead")
	}
	if strings.TrimSpace(destinationPath) == "" {
		return errors.New("backup: a restore destination path is required")
	}
	if destinationIsNonEmpty(destinationPath) {
		return fmt.Errorf("backup: restore destination %q already exists and is not empty; refusing to overwrite", destinationPath)
	}
	if copyError := copyDirectoryTree(backupRecord.BackupDirectoryPath, destinationPath, nil); copyError != nil {
		return fmt.Errorf("backup: restoring copy: %w", copyError)
	}
	return nil
}

// PrepareRemoteRestoreCommand returns the exact command the user should run to
// push a mirrored backup back to its remote. It never runs the command: pushing
// is always the user's to do. The mirror clone already has its origin set to the
// original remote, so the push targets the right place.
func PrepareRemoteRestoreCommand(backupRecord Record) (string, error) {
	if backupRecord.RemoteMirrorPath == "" {
		return "", errors.New("backup: this backup has no remote mirror to restore from")
	}
	return fmt.Sprintf("git -C %q push --mirror", backupRecord.RemoteMirrorPath), nil
}

// Remove deletes a backup. It is only ever called explicitly; nothing removes
// backups automatically.
func Remove(backupRecord Record) error {
	containerPath := backupContainerPathOf(backupRecord)
	if containerPath == "" {
		return errors.New("backup: cannot determine the backup directory to remove")
	}
	if removeError := forceRemoveAll(containerPath); removeError != nil {
		return fmt.Errorf("backup: removing %q: %w", containerPath, removeError)
	}
	return nil
}

// GarbageCollect removes backups older than the given age from a state directory.
// It only runs when invoked, and returns the records it removed.
func GarbageCollect(stateDirectoryPath string, olderThan time.Duration) ([]Record, error) {
	records, listError := List(stateDirectoryPath)
	if listError != nil {
		return nil, listError
	}
	cutoffTime := time.Now().UTC().Add(-olderThan)
	removedRecords := make([]Record, 0)
	for _, record := range records {
		if record.CreatedAt.Before(cutoffTime) {
			if removeError := Remove(record); removeError != nil {
				return removedRecords, removeError
			}
			removedRecords = append(removedRecords, record)
		}
	}
	return removedRecords, nil
}

func backupContainerPathOf(backupRecord Record) string {
	if backupRecord.BundleFilePath != "" {
		return filepath.Dir(backupRecord.BundleFilePath)
	}
	if backupRecord.BackupDirectoryPath != "" {
		return filepath.Dir(backupRecord.BackupDirectoryPath)
	}
	return ""
}

func writeRecordFile(backupContainerPath string, record Record) error {
	recordBytes, marshalError := json.MarshalIndent(record, "", "  ")
	if marshalError != nil {
		return fmt.Errorf("backup: encoding record: %w", marshalError)
	}
	recordPath := filepath.Join(backupContainerPath, recordFileName)
	if writeError := os.WriteFile(recordPath, recordBytes, 0o644); writeError != nil {
		return fmt.Errorf("backup: writing record file: %w", writeError)
	}
	return nil
}

func readRecordFile(recordPath string) (Record, error) {
	recordBytes, readError := os.ReadFile(recordPath)
	if readError != nil {
		return Record{}, readError
	}
	var record Record
	if unmarshalError := json.Unmarshal(recordBytes, &record); unmarshalError != nil {
		return Record{}, fmt.Errorf("backup: decoding record %q: %w", recordPath, unmarshalError)
	}
	return record, nil
}

// loadAliveIgnore compiles the repository's .aliveignore if present. A missing
// file yields a nil matcher (nothing extra is ignored).
func loadAliveIgnore(repositoryPath string) (*ignore.GitIgnore, error) {
	aliveIgnorePath := filepath.Join(repositoryPath, aliveIgnoreFileName)
	if _, statError := os.Stat(aliveIgnorePath); statError != nil {
		if errors.Is(statError, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("backup: checking for %s: %w", aliveIgnoreFileName, statError)
	}
	ignoreMatcher, compileError := ignore.CompileIgnoreFile(aliveIgnorePath)
	if compileError != nil {
		return nil, fmt.Errorf("backup: reading %s: %w", aliveIgnoreFileName, compileError)
	}
	return ignoreMatcher, nil
}

// copyDirectoryTree copies sourceRoot to destinationRoot, preserving file modes
// and recreating symlinks without following them. The .git directory is always
// copied. When ignoreMatcher is non-nil, matching paths (other than .git) are
// skipped using gitignore semantics.
func copyDirectoryTree(sourceRoot, destinationRoot string, ignoreMatcher *ignore.GitIgnore) error {
	return filepath.WalkDir(sourceRoot, func(currentPath string, dirEntry fs.DirEntry, walkError error) error {
		if walkError != nil {
			return walkError
		}
		relativePath, relativeError := filepath.Rel(sourceRoot, currentPath)
		if relativeError != nil {
			return relativeError
		}
		if relativePath == "." {
			return os.MkdirAll(destinationRoot, 0o755)
		}
		relativeSlashPath := filepath.ToSlash(relativePath)

		if ignoreMatcher != nil && !isWithinGitDirectory(relativeSlashPath) && pathIsIgnored(ignoreMatcher, relativeSlashPath, dirEntry.IsDir()) {
			if dirEntry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		destinationPath := filepath.Join(destinationRoot, relativePath)
		entryInfo, infoError := dirEntry.Info()
		if infoError != nil {
			return infoError
		}

		if entryInfo.Mode()&os.ModeSymlink != 0 {
			return recreateSymlink(currentPath, destinationPath)
		}
		if dirEntry.IsDir() {
			return os.MkdirAll(destinationPath, entryInfo.Mode().Perm())
		}
		return copyRegularFile(currentPath, destinationPath, entryInfo.Mode().Perm())
	})
}

func isWithinGitDirectory(relativeSlashPath string) bool {
	return relativeSlashPath == gitDirectoryName || strings.HasPrefix(relativeSlashPath, gitDirectoryName+"/")
}

func pathIsIgnored(ignoreMatcher *ignore.GitIgnore, relativeSlashPath string, isDirectory bool) bool {
	if ignoreMatcher.MatchesPath(relativeSlashPath) {
		return true
	}
	if isDirectory && ignoreMatcher.MatchesPath(relativeSlashPath+"/") {
		return true
	}
	return false
}

func recreateSymlink(sourcePath, destinationPath string) error {
	linkTarget, readLinkError := os.Readlink(sourcePath)
	if readLinkError != nil {
		return fmt.Errorf("reading symlink %q: %w", sourcePath, readLinkError)
	}
	if makeParentError := os.MkdirAll(filepath.Dir(destinationPath), 0o755); makeParentError != nil {
		return makeParentError
	}
	if symlinkError := os.Symlink(linkTarget, destinationPath); symlinkError != nil {
		return fmt.Errorf("creating symlink %q: %w", destinationPath, symlinkError)
	}
	return nil
}

func copyRegularFile(sourcePath, destinationPath string, mode os.FileMode) error {
	if makeParentError := os.MkdirAll(filepath.Dir(destinationPath), 0o755); makeParentError != nil {
		return makeParentError
	}
	sourceFile, openError := os.Open(sourcePath)
	if openError != nil {
		return fmt.Errorf("opening %q: %w", sourcePath, openError)
	}
	defer sourceFile.Close()

	destinationFile, createError := os.OpenFile(destinationPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if createError != nil {
		return fmt.Errorf("creating %q: %w", destinationPath, createError)
	}
	if _, copyError := io.Copy(destinationFile, sourceFile); copyError != nil {
		destinationFile.Close()
		return fmt.Errorf("copying to %q: %w", destinationPath, copyError)
	}
	if closeError := destinationFile.Close(); closeError != nil {
		return fmt.Errorf("closing %q: %w", destinationPath, closeError)
	}
	if chmodError := os.Chmod(destinationPath, mode); chmodError != nil {
		return fmt.Errorf("setting mode on %q: %w", destinationPath, chmodError)
	}
	return nil
}

// forceRemoveAll removes a tree, first clearing read-only bits so that git's
// read-only loose objects can be deleted on Windows.
func forceRemoveAll(rootPath string) error {
	_ = filepath.WalkDir(rootPath, func(currentPath string, _ fs.DirEntry, walkError error) error {
		if walkError == nil {
			_ = os.Chmod(currentPath, 0o700)
		}
		return nil
	})
	return os.RemoveAll(rootPath)
}

func destinationIsNonEmpty(destinationPath string) bool {
	entries, readError := os.ReadDir(destinationPath)
	if readError != nil {
		return false
	}
	return len(entries) > 0
}

// ensureBackupIsOutsideRepository refuses a state directory that is the
// repository itself or nested inside it, so a backup is never written into the
// tree it is protecting.
func ensureBackupIsOutsideRepository(repositoryPath, stateDirectoryPath string) error {
	absoluteRepository, repositoryError := filepath.Abs(repositoryPath)
	if repositoryError != nil {
		return fmt.Errorf("backup: resolving repository path: %w", repositoryError)
	}
	absoluteState, stateError := filepath.Abs(stateDirectoryPath)
	if stateError != nil {
		return fmt.Errorf("backup: resolving state directory path: %w", stateError)
	}
	relativePath, relativeError := filepath.Rel(absoluteRepository, absoluteState)
	if relativeError != nil {
		// On different volumes (Windows) Rel fails, which means it is outside.
		return nil
	}
	if relativePath == "." || !strings.HasPrefix(relativePath, "..") {
		return fmt.Errorf("backup: the state directory %q must be outside the repository %q", stateDirectoryPath, repositoryPath)
	}
	return nil
}

func generateIdentifier() (string, error) {
	randomSuffix := make([]byte, 4)
	if _, randomError := rand.Read(randomSuffix); randomError != nil {
		return "", fmt.Errorf("backup: generating identifier: %w", randomError)
	}
	return fmt.Sprintf("alive-backup-%s-%s", time.Now().UTC().Format(identifierTimeLayout), hex.EncodeToString(randomSuffix)), nil
}
