//go:build integration

package backup

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"alive.name/internal/domain"
	"alive.name/internal/fixturerepo"
	"alive.name/internal/gitclient"
)

// Integration tests: anything that touches the real filesystem or runs git.
// Some use fakes for the git surface but still copy real files, which is why
// they live here rather than in the unit file.

func requireGitBinary(testingHandle *testing.T) {
	testingHandle.Helper()
	if _, lookupError := exec.LookPath("git"); lookupError != nil {
		testingHandle.Skip("git binary not available; skipping backup integration test")
	}
}

func newClient(testingHandle *testing.T, repositoryPath string) *gitclient.GitClient {
	testingHandle.Helper()
	client, constructionError := gitclient.NewGitClient(repositoryPath)
	if constructionError != nil {
		testingHandle.Fatalf("constructing client: %v", constructionError)
	}
	return client
}

// fakeSource is a scripted sourceRepository that writes a real bundle file so a
// backup copy can be produced and verified without a real git binary.
type fakeSource struct {
	repositoryPath  string
	references      []gitclient.Reference
	headCommit      string
	remotes         []gitclient.Remote
	verifyBundleErr error
	cloneCalledWith []string
}

func (source *fakeSource) RepositoryPath() string           { return source.repositoryPath }
func (source *fakeSource) VerifyRepositoryIntegrity() error { return nil }
func (source *fakeSource) ListReferences() ([]gitclient.Reference, error) {
	return source.references, nil
}
func (source *fakeSource) ResolveHeadCommit() (string, error) { return source.headCommit, nil }
func (source *fakeSource) CreateAllRefsBundle(destinationPath string) error {
	return os.WriteFile(destinationPath, []byte("BUNDLE"), 0o644)
}
func (source *fakeSource) VerifyBundle(string) error { return source.verifyBundleErr }
func (source *fakeSource) ListRemotes() ([]gitclient.Remote, error) {
	return source.remotes, nil
}
func (source *fakeSource) CloneMirrorOfRemote(remoteURL, destinationPath string) error {
	source.cloneCalledWith = append(source.cloneCalledWith, remoteURL)
	return os.MkdirAll(destinationPath, 0o755)
}

type fakeVerifier struct {
	references   []gitclient.Reference
	headCommit   string
	integrityErr error
}

func (verifier *fakeVerifier) VerifyRepositoryIntegrity() error { return verifier.integrityErr }
func (verifier *fakeVerifier) ListReferences() ([]gitclient.Reference, error) {
	return verifier.references, nil
}
func (verifier *fakeVerifier) ResolveHeadCommit() (string, error) { return verifier.headCommit, nil }

func factoryReturning(verifier repositoryVerifier) verifierFactory {
	return func(string) (repositoryVerifier, error) { return verifier, nil }
}

func factoryThatMustNotBeCalled(testingHandle *testing.T) verifierFactory {
	return func(string) (repositoryVerifier, error) {
		testingHandle.Helper()
		testingHandle.Fatal("verifier factory should not be called for a bundle-only backup")
		return nil, nil
	}
}

func newSourceRepositoryTree(testingHandle *testing.T) string {
	testingHandle.Helper()
	repositoryPath := testingHandle.TempDir()
	mustWrite(testingHandle, filepath.Join(repositoryPath, "file.txt"), "hello")
	mustWrite(testingHandle, filepath.Join(repositoryPath, ".git", "HEAD"), "ref: refs/heads/main")
	return repositoryPath
}

func mustWrite(testingHandle *testing.T, path, content string) {
	testingHandle.Helper()
	if makeError := os.MkdirAll(filepath.Dir(path), 0o755); makeError != nil {
		testingHandle.Fatalf("mkdir for %q: %v", path, makeError)
	}
	if writeError := os.WriteFile(path, []byte(content), 0o644); writeError != nil {
		testingHandle.Fatalf("writing %q: %v", path, writeError)
	}
}

func writeFakeBackup(testingHandle *testing.T, stateDirectoryPath, identifier string, createdAt time.Time) Record {
	testingHandle.Helper()
	containerPath := filepath.Join(stateDirectoryPath, identifier)
	if makeError := os.MkdirAll(containerPath, 0o755); makeError != nil {
		testingHandle.Fatalf("mkdir: %v", makeError)
	}
	bundlePath := filepath.Join(containerPath, allRefsBundleName)
	mustWrite(testingHandle, bundlePath, "BUNDLE")
	record := Record{Identifier: identifier, BundleFilePath: bundlePath, CreatedAt: createdAt, VerificationStatus: domain.VerificationPassed}
	if writeError := writeRecordFile(containerPath, record); writeError != nil {
		testingHandle.Fatalf("writeRecordFile: %v", writeError)
	}
	return record
}

func assertExists(testingHandle *testing.T, path string) {
	testingHandle.Helper()
	if _, statError := os.Stat(path); statError != nil {
		testingHandle.Errorf("expected %q to exist: %v", path, statError)
	}
}

func assertMissing(testingHandle *testing.T, path string) {
	testingHandle.Helper()
	if _, statError := os.Stat(path); statError == nil {
		testingHandle.Errorf("expected %q to be absent", path)
	}
}

// --- Real-git end-to-end -----------------------------------------------------

func TestIntegrationCreateVerifiedAndRestore(testingHandle *testing.T) {
	requireGitBinary(testingHandle)
	repository := fixturerepo.New(testingHandle)
	repository.Commit(fixturerepo.CommitSpec{Message: "first", Files: map[string]string{"a.txt": "one"}})
	repository.Commit(fixturerepo.CommitSpec{Message: "second", Files: map[string]string{"b.txt": "two"}})
	repository.AnnotatedTag(fixturerepo.AnnotatedTagSpec{TagName: "v1", Message: "release"})

	sourceClient := newClient(testingHandle, repository.Path())
	stateDirectoryPath := testingHandle.TempDir()

	record, createError := CreateVerified(sourceClient, Options{StateDirectoryPath: stateDirectoryPath})
	if createError != nil {
		testingHandle.Fatalf("CreateVerified: %v", createError)
	}
	if record.VerificationStatus != domain.VerificationPassed {
		testingHandle.Fatalf("expected VerificationPassed, got %v", record.VerificationStatus)
	}

	records, listError := List(stateDirectoryPath)
	if listError != nil || len(records) != 1 {
		testingHandle.Fatalf("expected one listed record, got %d (err %v)", len(records), listError)
	}

	restoreDestination := filepath.Join(testingHandle.TempDir(), "restored")
	if restoreError := PrepareLocalRestore(record, restoreDestination); restoreError != nil {
		testingHandle.Fatalf("PrepareLocalRestore: %v", restoreError)
	}
	sourceReferences, _ := sourceClient.ListReferences()
	restoredReferences, restoredError := newClient(testingHandle, restoreDestination).ListReferences()
	if restoredError != nil {
		testingHandle.Fatalf("reading restored references: %v", restoredError)
	}
	if !referencesEqual(sourceReferences, restoredReferences) {
		testingHandle.Errorf("restored references differ\nsource:  %+v\nrestored:%+v", sourceReferences, restoredReferences)
	}
}

func TestIntegrationCreateVerifiedWithRemoteMirror(testingHandle *testing.T) {
	requireGitBinary(testingHandle)
	repository := fixturerepo.New(testingHandle)
	repository.Commit(fixturerepo.CommitSpec{Message: "shared", Files: map[string]string{"a.txt": "one"}})
	repository.PublishToNewBareRemote("origin")

	record, createError := CreateVerified(newClient(testingHandle, repository.Path()), Options{StateDirectoryPath: testingHandle.TempDir(), IncludeRemoteMirror: true})
	if createError != nil {
		testingHandle.Fatalf("CreateVerified: %v", createError)
	}
	if record.RemoteMirrorPath == "" {
		testingHandle.Fatal("expected a remote mirror path")
	}
	restoreCommand, commandError := PrepareRemoteRestoreCommand(record)
	if commandError != nil || !strings.Contains(restoreCommand, "push --mirror") {
		testingHandle.Errorf("expected a mirror push command, got %q (err %v)", restoreCommand, commandError)
	}
}

// --- Fake git surface, real filesystem --------------------------------------

func TestIntegrationCreateVerifiedHappyPath(testingHandle *testing.T) {
	repositoryPath := newSourceRepositoryTree(testingHandle)
	stateDirectoryPath := testingHandle.TempDir()
	source := &fakeSource{repositoryPath: repositoryPath, references: sampleReferences(), headCommit: "abc123"}
	copyVerifier := &fakeVerifier{references: sampleReferences(), headCommit: "abc123"}

	record, createError := createVerified(source, factoryReturning(copyVerifier), Options{StateDirectoryPath: stateDirectoryPath})
	if createError != nil {
		testingHandle.Fatalf("createVerified: %v", createError)
	}
	if record.VerificationStatus != domain.VerificationPassed {
		testingHandle.Fatalf("expected VerificationPassed, got %v", record.VerificationStatus)
	}
	assertExists(testingHandle, record.BundleFilePath)
	assertExists(testingHandle, filepath.Join(record.BackupDirectoryPath, "file.txt"))
	assertExists(testingHandle, filepath.Join(record.BackupDirectoryPath, ".git", "HEAD"))

	records, listError := List(stateDirectoryPath)
	if listError != nil || len(records) != 1 || records[0].VerificationStatus != domain.VerificationPassed {
		testingHandle.Errorf("expected one passed record, got %+v (err %v)", records, listError)
	}
}

func TestIntegrationCreateVerifiedVerificationFailure(testingHandle *testing.T) {
	source := &fakeSource{repositoryPath: newSourceRepositoryTree(testingHandle), references: sampleReferences(), headCommit: "abc123"}
	copyVerifier := &fakeVerifier{references: sampleReferences(), headCommit: "abc123", integrityErr: errString("corrupt")}

	record, createError := createVerified(source, factoryReturning(copyVerifier), Options{StateDirectoryPath: testingHandle.TempDir()})
	if createError == nil {
		testingHandle.Fatal("expected a verification error")
	}
	if record.VerificationStatus != domain.VerificationFailed {
		testingHandle.Errorf("expected VerificationFailed, got %v", record.VerificationStatus)
	}
}

func TestIntegrationCreateVerifiedBundleVerifyFailure(testingHandle *testing.T) {
	source := &fakeSource{repositoryPath: newSourceRepositoryTree(testingHandle), references: sampleReferences(), headCommit: "abc123", verifyBundleErr: errString("bad bundle")}
	if _, createError := createVerified(source, factoryReturning(&fakeVerifier{}), Options{StateDirectoryPath: testingHandle.TempDir()}); createError == nil {
		testingHandle.Fatal("expected a bundle verification error")
	}
}

func TestIntegrationCreateVerifiedBundleOnly(testingHandle *testing.T) {
	source := &fakeSource{repositoryPath: newSourceRepositoryTree(testingHandle), references: sampleReferences(), headCommit: "abc123"}
	record, createError := createVerified(source, factoryThatMustNotBeCalled(testingHandle), Options{StateDirectoryPath: testingHandle.TempDir(), BundleOnly: true})
	if createError != nil {
		testingHandle.Fatalf("createVerified bundle-only: %v", createError)
	}
	if record.BackupDirectoryPath != "" {
		testingHandle.Errorf("bundle-only backup should have no copy directory, got %q", record.BackupDirectoryPath)
	}
	if record.VerificationStatus != domain.VerificationPassed {
		testingHandle.Errorf("expected VerificationPassed, got %v", record.VerificationStatus)
	}
}

func TestIntegrationCreateVerifiedRemoteMirrorFakes(testingHandle *testing.T) {
	testingHandle.Run("mirror created when remote exists", func(subTest *testing.T) {
		source := &fakeSource{repositoryPath: newSourceRepositoryTree(subTest), references: sampleReferences(), headCommit: "abc123", remotes: []gitclient.Remote{{Name: "origin", FetchURL: "file:///somewhere"}}}
		record, createError := createVerified(source, factoryReturning(&fakeVerifier{references: sampleReferences(), headCommit: "abc123"}), Options{StateDirectoryPath: subTest.TempDir(), IncludeRemoteMirror: true})
		if createError != nil {
			subTest.Fatalf("createVerified: %v", createError)
		}
		if record.RemoteMirrorPath == "" || len(source.cloneCalledWith) != 1 {
			subTest.Errorf("expected a mirror clone, got path %q calls %v", record.RemoteMirrorPath, source.cloneCalledWith)
		}
	})
	testingHandle.Run("no remote means no mirror and no error", func(subTest *testing.T) {
		source := &fakeSource{repositoryPath: newSourceRepositoryTree(subTest), references: sampleReferences(), headCommit: "abc123"}
		record, createError := createVerified(source, factoryReturning(&fakeVerifier{references: sampleReferences(), headCommit: "abc123"}), Options{StateDirectoryPath: subTest.TempDir(), IncludeRemoteMirror: true})
		if createError != nil {
			subTest.Fatalf("createVerified: %v", createError)
		}
		if record.RemoteMirrorPath != "" {
			subTest.Errorf("expected no mirror path, got %q", record.RemoteMirrorPath)
		}
	})
}

func TestIntegrationCreateVerifiedRejectsStateInsideRepository(testingHandle *testing.T) {
	repositoryPath := newSourceRepositoryTree(testingHandle)
	source := &fakeSource{repositoryPath: repositoryPath, references: sampleReferences(), headCommit: "abc123"}
	if _, createError := createVerified(source, factoryReturning(&fakeVerifier{}), Options{StateDirectoryPath: filepath.Join(repositoryPath, "backups")}); createError == nil {
		testingHandle.Fatal("expected an error when the state directory is inside the repository")
	}
}

func TestIntegrationDefaultVerifierFactory(testingHandle *testing.T) {
	verifier, factoryError := defaultVerifierFactory(testingHandle.TempDir())
	if factoryError != nil || verifier == nil {
		testingHandle.Fatalf("expected a verifier, got %v (err %v)", verifier, factoryError)
	}
}

// --- copyDirectoryTree -------------------------------------------------------

func TestIntegrationCopyDirectoryTreeHonoursAliveIgnore(testingHandle *testing.T) {
	sourceRoot := testingHandle.TempDir()
	mustWrite(testingHandle, filepath.Join(sourceRoot, "keep.txt"), "keep me")
	mustWrite(testingHandle, filepath.Join(sourceRoot, ".git", "config"), "[core]")
	mustWrite(testingHandle, filepath.Join(sourceRoot, "node_modules", "junk.js"), "junk")
	mustWrite(testingHandle, filepath.Join(sourceRoot, "build", "artifact.bin"), "bin")
	mustWrite(testingHandle, filepath.Join(sourceRoot, aliveIgnoreFileName), "node_modules/\nbuild/\n.git/\n")

	ignoreMatcher, loadError := loadAliveIgnore(sourceRoot)
	if loadError != nil {
		testingHandle.Fatalf("loadAliveIgnore: %v", loadError)
	}
	destinationRoot := filepath.Join(testingHandle.TempDir(), "copy")
	if copyError := copyDirectoryTree(sourceRoot, destinationRoot, ignoreMatcher); copyError != nil {
		testingHandle.Fatalf("copyDirectoryTree: %v", copyError)
	}

	assertExists(testingHandle, filepath.Join(destinationRoot, "keep.txt"))
	assertExists(testingHandle, filepath.Join(destinationRoot, ".git", "config")) // .git is never excluded
	assertMissing(testingHandle, filepath.Join(destinationRoot, "node_modules"))
	assertMissing(testingHandle, filepath.Join(destinationRoot, "build"))
}

func TestIntegrationCopyDirectoryTreeRecreatesSymlinks(testingHandle *testing.T) {
	sourceRoot := testingHandle.TempDir()
	mustWrite(testingHandle, filepath.Join(sourceRoot, "real.txt"), "target")
	if symlinkError := os.Symlink("real.txt", filepath.Join(sourceRoot, "link.txt")); symlinkError != nil {
		testingHandle.Skipf("symlinks not permitted in this environment: %v", symlinkError)
	}
	destinationRoot := filepath.Join(testingHandle.TempDir(), "copy")
	if copyError := copyDirectoryTree(sourceRoot, destinationRoot, nil); copyError != nil {
		testingHandle.Fatalf("copyDirectoryTree: %v", copyError)
	}
	linkInfo, lstatError := os.Lstat(filepath.Join(destinationRoot, "link.txt"))
	if lstatError != nil {
		testingHandle.Fatalf("lstat copied link: %v", lstatError)
	}
	if linkInfo.Mode()&os.ModeSymlink == 0 {
		testingHandle.Error("expected the copied entry to remain a symlink")
	}
}

// --- restore, list, remove, gc, guards --------------------------------------

func TestIntegrationPrepareLocalRestore(testingHandle *testing.T) {
	testingHandle.Run("happy restore reproduces the tree", func(subTest *testing.T) {
		backupCopy := filepath.Join(subTest.TempDir(), "backupcopy")
		mustWrite(subTest, filepath.Join(backupCopy, "restored.txt"), "content")
		destination := filepath.Join(subTest.TempDir(), "restored")
		if restoreError := PrepareLocalRestore(Record{BackupDirectoryPath: backupCopy}, destination); restoreError != nil {
			subTest.Fatalf("PrepareLocalRestore: %v", restoreError)
		}
		assertExists(subTest, filepath.Join(destination, "restored.txt"))
	})
	testingHandle.Run("non-empty destination is refused", func(subTest *testing.T) {
		backupCopy := filepath.Join(subTest.TempDir(), "backupcopy")
		mustWrite(subTest, filepath.Join(backupCopy, "restored.txt"), "content")
		destination := subTest.TempDir()
		mustWrite(subTest, filepath.Join(destination, "existing.txt"), "in the way")
		if restoreError := PrepareLocalRestore(Record{BackupDirectoryPath: backupCopy}, destination); restoreError == nil {
			subTest.Error("expected refusal to overwrite a non-empty destination")
		}
	})
}

func TestIntegrationRemoveAndGarbageCollect(testingHandle *testing.T) {
	testingHandle.Run("remove deletes a backup", func(subTest *testing.T) {
		stateDirectoryPath := subTest.TempDir()
		record := writeFakeBackup(subTest, stateDirectoryPath, "alive-backup-old", time.Now().UTC())
		if removeError := Remove(record); removeError != nil {
			subTest.Fatalf("Remove: %v", removeError)
		}
		assertMissing(subTest, filepath.Dir(record.BundleFilePath))
	})

	testingHandle.Run("garbage collect removes only old backups", func(subTest *testing.T) {
		stateDirectoryPath := subTest.TempDir()
		writeFakeBackup(subTest, stateDirectoryPath, "alive-backup-old", time.Now().UTC().Add(-48*time.Hour))
		recentRecord := writeFakeBackup(subTest, stateDirectoryPath, "alive-backup-recent", time.Now().UTC())

		removed, gcError := GarbageCollect(stateDirectoryPath, 24*time.Hour)
		if gcError != nil {
			subTest.Fatalf("GarbageCollect: %v", gcError)
		}
		if len(removed) != 1 || removed[0].Identifier != "alive-backup-old" {
			subTest.Errorf("expected only the old backup removed, got %+v", removed)
		}
		remaining, listError := List(stateDirectoryPath)
		if listError != nil || len(remaining) != 1 || remaining[0].Identifier != recentRecord.Identifier {
			subTest.Errorf("expected the recent backup to remain, got %+v (err %v)", remaining, listError)
		}
	})
}

func TestIntegrationListReturnsEmptyForMissingStateDirectory(testingHandle *testing.T) {
	records, listError := List(filepath.Join(testingHandle.TempDir(), "never-created"))
	if listError != nil || len(records) != 0 {
		testingHandle.Errorf("expected no records and no error, got %d (err %v)", len(records), listError)
	}
}

func TestIntegrationListSkipsAndReportsMalformedRecords(testingHandle *testing.T) {
	stateDirectoryPath := testingHandle.TempDir()
	mustWrite(testingHandle, filepath.Join(stateDirectoryPath, "loose.txt"), "ignore me")
	if makeError := os.MkdirAll(filepath.Join(stateDirectoryPath, "no-record"), 0o755); makeError != nil {
		testingHandle.Fatalf("mkdir: %v", makeError)
	}
	records, listError := List(stateDirectoryPath)
	if listError != nil || len(records) != 0 {
		testingHandle.Fatalf("List should skip incomplete entries, got %d (err %v)", len(records), listError)
	}

	mustWrite(testingHandle, filepath.Join(stateDirectoryPath, "broken", recordFileName), "{ this is not json")
	if _, malformedError := List(stateDirectoryPath); malformedError == nil {
		testingHandle.Error("expected an error for a malformed record file")
	}
}

func TestIntegrationEnsureBackupIsOutsideRepository(testingHandle *testing.T) {
	repositoryPath := testingHandle.TempDir()
	if insideError := ensureBackupIsOutsideRepository(repositoryPath, filepath.Join(repositoryPath, "sub")); insideError == nil {
		testingHandle.Error("expected error for a state directory inside the repository")
	}
	if sameError := ensureBackupIsOutsideRepository(repositoryPath, repositoryPath); sameError == nil {
		testingHandle.Error("expected error for a state directory equal to the repository")
	}
	if outsideError := ensureBackupIsOutsideRepository(repositoryPath, testingHandle.TempDir()); outsideError != nil {
		testingHandle.Errorf("expected no error for a state directory outside the repository, got %v", outsideError)
	}
}
