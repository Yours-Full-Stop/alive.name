package backup

import (
	"path/filepath"
	"strings"
	"testing"

	"alive.name/internal/gitclient"
)

// Unit tests: pure logic only, driven through stubs with no filesystem and no
// git. Everything that copies files, reads directories, or runs git is under the
// integration tag.

// stubSource is a pure sourceRepository: it performs no I/O.
type stubSource struct {
	references  []gitclient.Reference
	headCommit  string
	listRefsErr error
	headErr     error
}

func (source stubSource) RepositoryPath() string           { return "" }
func (source stubSource) VerifyRepositoryIntegrity() error { return nil }
func (source stubSource) CreateAllRefsBundle(string) error { return nil }
func (source stubSource) VerifyBundle(string) error        { return nil }
func (source stubSource) ListRemotes() ([]gitclient.Remote, error) {
	return nil, nil
}
func (source stubSource) CloneMirrorOfRemote(string, string) error { return nil }
func (source stubSource) ListReferences() ([]gitclient.Reference, error) {
	return source.references, source.listRefsErr
}
func (source stubSource) ResolveHeadCommit() (string, error) {
	return source.headCommit, source.headErr
}

// stubVerifier is a pure repositoryVerifier.
type stubVerifier struct {
	references   []gitclient.Reference
	headCommit   string
	integrityErr error
	listRefsErr  error
	headErr      error
}

func (verifier stubVerifier) VerifyRepositoryIntegrity() error { return verifier.integrityErr }
func (verifier stubVerifier) ListReferences() ([]gitclient.Reference, error) {
	return verifier.references, verifier.listRefsErr
}
func (verifier stubVerifier) ResolveHeadCommit() (string, error) {
	return verifier.headCommit, verifier.headErr
}

func sampleReferences() []gitclient.Reference {
	return []gitclient.Reference{{Name: "refs/heads/main", ObjectHash: "abc123"}}
}

// errString is a tiny error helper so tests can inject failures inline.
type errString string

func (message errString) Error() string { return string(message) }

func TestReferencesEqual(testingHandle *testing.T) {
	base := []gitclient.Reference{{Name: "refs/heads/main", ObjectHash: "a"}, {Name: "refs/tags/v1", ObjectHash: "b"}}
	reordered := []gitclient.Reference{{Name: "refs/tags/v1", ObjectHash: "b"}, {Name: "refs/heads/main", ObjectHash: "a"}}
	differentHash := []gitclient.Reference{{Name: "refs/heads/main", ObjectHash: "a"}, {Name: "refs/tags/v1", ObjectHash: "DIFF"}}

	if !referencesEqual(base, reordered) {
		testingHandle.Error("expected order-independent equality")
	}
	if referencesEqual(base, differentHash) {
		testingHandle.Error("expected inequality on differing hash")
	}
	if referencesEqual(base, base[:1]) {
		testingHandle.Error("expected inequality on differing length")
	}
}

func TestBackupContainerPathOf(testingHandle *testing.T) {
	if got := backupContainerPathOf(Record{BundleFilePath: filepath.Join("state", "id", "all-refs.bundle")}); got != filepath.Join("state", "id") {
		testingHandle.Errorf("bundle-based container path wrong: %q", got)
	}
	if got := backupContainerPathOf(Record{BackupDirectoryPath: filepath.Join("state", "id", "repository")}); got != filepath.Join("state", "id") {
		testingHandle.Errorf("copy-based container path wrong: %q", got)
	}
	if got := backupContainerPathOf(Record{}); got != "" {
		testingHandle.Errorf("empty record should yield empty container path, got %q", got)
	}
}

func TestPrepareRemoteRestoreCommand(testingHandle *testing.T) {
	testingHandle.Run("with mirror returns a push command", func(subTest *testing.T) {
		command, commandError := PrepareRemoteRestoreCommand(Record{RemoteMirrorPath: "/backups/mirror.git"})
		if commandError != nil {
			subTest.Fatalf("unexpected error: %v", commandError)
		}
		if !strings.Contains(command, "push --mirror") || !strings.Contains(command, "mirror.git") {
			subTest.Errorf("unexpected command: %q", command)
		}
	})
	testingHandle.Run("without mirror is an error", func(subTest *testing.T) {
		if _, commandError := PrepareRemoteRestoreCommand(Record{}); commandError == nil {
			subTest.Error("expected an error when there is no mirror")
		}
	})
}

func TestVerifyCopyAgainstSource(testingHandle *testing.T) {
	testingHandle.Run("matching copy passes", func(subTest *testing.T) {
		source := stubSource{references: sampleReferences(), headCommit: "abc123"}
		copyVerifier := stubVerifier{references: sampleReferences(), headCommit: "abc123"}
		if verifyError := verifyCopyAgainstSource(source, copyVerifier); verifyError != nil {
			subTest.Errorf("expected a matching copy to pass, got %v", verifyError)
		}
	})

	testCases := []struct {
		caseName     string
		source       stubSource
		copyVerifier stubVerifier
	}{
		{caseName: "copy integrity fails", source: stubSource{references: sampleReferences(), headCommit: "abc123"}, copyVerifier: stubVerifier{references: sampleReferences(), headCommit: "abc123", integrityErr: errString("corrupt")}},
		{caseName: "references differ", source: stubSource{references: sampleReferences(), headCommit: "abc123"}, copyVerifier: stubVerifier{references: []gitclient.Reference{{Name: "refs/heads/main", ObjectHash: "DIFFERENT"}}, headCommit: "abc123"}},
		{caseName: "head differs", source: stubSource{references: sampleReferences(), headCommit: "abc123"}, copyVerifier: stubVerifier{references: sampleReferences(), headCommit: "DIFFERENT"}},
		{caseName: "source refs error", source: stubSource{listRefsErr: errString("boom")}, copyVerifier: stubVerifier{}},
		{caseName: "copy refs error", source: stubSource{references: sampleReferences()}, copyVerifier: stubVerifier{listRefsErr: errString("boom")}},
		{caseName: "source head error", source: stubSource{references: sampleReferences(), headErr: errString("boom")}, copyVerifier: stubVerifier{references: sampleReferences()}},
		{caseName: "copy head error", source: stubSource{references: sampleReferences()}, copyVerifier: stubVerifier{references: sampleReferences(), headErr: errString("boom")}},
	}
	for _, testCase := range testCases {
		testingHandle.Run(testCase.caseName, func(subTest *testing.T) {
			if verifyError := verifyCopyAgainstSource(testCase.source, testCase.copyVerifier); verifyError == nil {
				subTest.Error("expected an error")
			}
		})
	}
}

func TestCreateVerifiedRequiresStateDirectory(testingHandle *testing.T) {
	factory := func(string) (repositoryVerifier, error) { return stubVerifier{}, nil }
	if _, createError := createVerified(stubSource{}, factory, Options{}); createError == nil {
		testingHandle.Fatal("expected an error when no state directory is given")
	}
}

func TestRemoveWithNoLocatablePathErrors(testingHandle *testing.T) {
	if removeError := Remove(Record{}); removeError == nil {
		testingHandle.Error("expected an error removing a record with no locatable path")
	}
}

func TestPrepareLocalRestoreGuards(testingHandle *testing.T) {
	testingHandle.Run("bundle-only cannot be locally restored", func(subTest *testing.T) {
		if restoreError := PrepareLocalRestore(Record{BackupDirectoryPath: ""}, "some/destination"); restoreError == nil {
			subTest.Error("expected an error for a bundle-only backup")
		}
	})
	testingHandle.Run("empty destination is refused", func(subTest *testing.T) {
		if restoreError := PrepareLocalRestore(Record{BackupDirectoryPath: "some/copy"}, "  "); restoreError == nil {
			subTest.Error("expected an error for an empty restore destination")
		}
	})
}
