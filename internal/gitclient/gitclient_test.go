package gitclient

import (
	"errors"
	"strings"
	"testing"
)

// These are unit tests: they drive GitClient through a scripted CommandRunner
// with canned output and never execute the real git binary. The real-git
// behaviour is covered by the integration tests (build tag "integration").

const (
	fieldSeparator  = "\x1f"
	recordSeparator = "\x00"
)

// scriptedGitRunner is a fake CommandRunner. It records every invocation and
// returns canned output chosen by inspecting the arguments, so parsing logic can
// be tested deterministically without a git binary.
type scriptedGitRunner struct {
	respond     func(invocation GitInvocation) (standardOutput string, standardError string, runError error)
	invocations []GitInvocation
}

func (runner *scriptedGitRunner) RunGit(invocation GitInvocation) ([]byte, []byte, error) {
	runner.invocations = append(runner.invocations, invocation)
	if runner.respond == nil {
		return nil, nil, nil
	}
	standardOutput, standardError, runError := runner.respond(invocation)
	return []byte(standardOutput), []byte(standardError), runError
}

func (runner *scriptedGitRunner) subcommandsIssued() []string {
	subcommands := make([]string, 0, len(runner.invocations))
	for _, invocation := range runner.invocations {
		if len(invocation.Arguments) > 0 {
			subcommands = append(subcommands, invocation.Arguments[0])
		}
	}
	return subcommands
}

func argumentsContain(invocation GitInvocation, requiredTokens ...string) bool {
	for _, requiredToken := range requiredTokens {
		found := false
		for _, argument := range invocation.Arguments {
			if argument == requiredToken {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func newUnitClient(testingHandle *testing.T, runner CommandRunner) *GitClient {
	testingHandle.Helper()
	// Construct directly so unit tests touch no filesystem: they exercise
	// parsing and the allowlist, not path validation. The repository path is
	// just a token here because the runner is a fake.
	return &GitClient{repositoryPath: "repository", runner: runner}
}

func TestGitSubcommandAllowlist(testingHandle *testing.T) {
	if GitSubcommandIsAllowed("push") {
		testingHandle.Fatal("push must never be an allowed git subcommand")
	}
	for _, forbiddenSubcommand := range []string{"push", "send-pack", "update-ref", "commit", "reset"} {
		if GitSubcommandIsAllowed(forbiddenSubcommand) {
			testingHandle.Errorf("subcommand %q must not be allowed", forbiddenSubcommand)
		}
	}
	for _, requiredSubcommand := range []string{"log", "rev-list", "cat-file", "fetch", "bundle", "clone", "ls-files"} {
		if !GitSubcommandIsAllowed(requiredSubcommand) {
			testingHandle.Errorf("subcommand %q should be allowed", requiredSubcommand)
		}
	}
}

func TestExecuteRefusesDisallowedSubcommand(testingHandle *testing.T) {
	runner := &scriptedGitRunner{}
	client := newUnitClient(testingHandle, runner)

	_, pushError := client.execute(client.repositoryPath, "push", "origin", "main")
	if pushError == nil {
		testingHandle.Fatal("expected execute to refuse a push subcommand")
	}
	if !strings.Contains(pushError.Error(), "not permitted") {
		testingHandle.Errorf("push refusal should explain itself, got: %v", pushError)
	}
	if len(runner.invocations) != 0 {
		testingHandle.Errorf("a refused push must never reach the runner, got %d invocations", len(runner.invocations))
	}

	if _, emptyError := client.execute(client.repositoryPath); emptyError == nil {
		testingHandle.Error("expected execute to refuse an empty argument list")
	}
}

func TestNoPushAcrossReadOperations(testingHandle *testing.T) {
	runner := &scriptedGitRunner{}
	client := newUnitClient(testingHandle, runner)

	_, _ = client.ListAllCommitsWithMetadata()
	_, _ = client.ListAllObjectsWithPaths()
	_, _ = client.ListAnnotatedTags()
	_, _ = client.ListRemotes()
	_, _ = client.ListCommitsReachableFromRemoteTrackingRefs()
	_ = client.FetchAllRemotes()
	_ = client.VerifyRepositoryIntegrity()
	_ = client.CreateAllRefsBundle("some.bundle")
	_, _ = client.WorkingTreeIsClean()
	_, _ = client.ListTrackedWorkingTreeFiles()

	for _, issuedSubcommand := range runner.subcommandsIssued() {
		if issuedSubcommand == "push" {
			testingHandle.Fatalf("a read operation issued a push: %v", runner.subcommandsIssued())
		}
	}
}

func TestNewGitClientValidation(testingHandle *testing.T) {
	// These branches return before any filesystem access, so they are pure.
	testingHandle.Run("empty path", func(subTest *testing.T) {
		if _, constructionError := NewGitClientWithRunner("", &scriptedGitRunner{}); constructionError == nil {
			subTest.Error("expected error for empty path")
		}
	})
	testingHandle.Run("nil runner", func(subTest *testing.T) {
		if _, constructionError := NewGitClientWithRunner("some/path", nil); constructionError == nil {
			subTest.Error("expected error for nil runner")
		}
	})
}

func TestListAllCommitsWithMetadataParsing(testingHandle *testing.T) {
	firstRecord := strings.Join([]string{"hash1", "Alice Author", "alice@example.test", "Bob Committer", "bob@example.test", "message one by Alice"}, fieldSeparator)
	secondRecord := strings.Join([]string{"hash2", "Old Name", "old.name@example.test", "Old Name", "old.name@example.test", "second message"}, fieldSeparator)
	logOutput := firstRecord + recordSeparator + secondRecord + recordSeparator

	testCases := []struct {
		caseName        string
		category        string
		hasCommitsReply string
		logReply        string
		logError        error
		expectError     bool
		expectedCount   int
	}{
		{caseName: "two commits parse", category: "happy", hasCommitsReply: "hash1\n", logReply: logOutput, expectedCount: 2},
		{caseName: "empty repository yields none", category: "negative", hasCommitsReply: "", logReply: "", expectedCount: 0},
		{caseName: "malformed record errors", category: "edge", hasCommitsReply: "hash1\n", logReply: "too\x1ffew\x00", expectError: true},
		{caseName: "log failure surfaces", category: "error", hasCommitsReply: "hash1\n", logError: errors.New("exit 1"), expectError: true},
	}
	for _, testCase := range testCases {
		testingHandle.Run(testCase.caseName, func(subTest *testing.T) {
			runner := &scriptedGitRunner{respond: func(invocation GitInvocation) (string, string, error) {
				switch {
				case argumentsContain(invocation, "rev-list", "--max-count=1"):
					return testCase.hasCommitsReply, "", nil
				case argumentsContain(invocation, "log", "--all"):
					return testCase.logReply, "", testCase.logError
				}
				return "", "", nil
			}}
			client := newUnitClient(subTest, runner)
			commits, listError := client.ListAllCommitsWithMetadata()
			if testCase.expectError {
				if listError == nil {
					subTest.Fatal("expected an error")
				}
				return
			}
			if listError != nil {
				subTest.Fatalf("unexpected error: %v", listError)
			}
			if len(commits) != testCase.expectedCount {
				subTest.Fatalf("expected %d commits, got %d", testCase.expectedCount, len(commits))
			}
			if testCase.expectedCount == 2 {
				if commits[0].AuthorName != "Alice Author" || commits[1].AuthorEmail != "old.name@example.test" {
					subTest.Errorf("parsed commits are wrong: %+v", commits)
				}
			}
		})
	}
}

func TestListAllObjectsWithPathsParsing(testingHandle *testing.T) {
	runner := &scriptedGitRunner{respond: func(invocation GitInvocation) (string, string, error) {
		switch {
		case argumentsContain(invocation, "rev-list", "--max-count=1"):
			return "hash1\n", "", nil
		case argumentsContain(invocation, "rev-list", "--objects"):
			return "hash1\nhash2 path/to/file.txt\nhash3 somedir\n", "", nil
		}
		return "", "", nil
	}}
	client := newUnitClient(testingHandle, runner)
	objectEntries, listError := client.ListAllObjectsWithPaths()
	if listError != nil {
		testingHandle.Fatalf("unexpected error: %v", listError)
	}
	if len(objectEntries) != 3 {
		testingHandle.Fatalf("expected 3 objects, got %d", len(objectEntries))
	}
	if objectEntries[0].Path != "" || objectEntries[1].Path != "path/to/file.txt" {
		testingHandle.Errorf("object paths parsed incorrectly: %+v", objectEntries)
	}

	hashes, hashError := client.ListAllObjectHashes()
	if hashError != nil {
		testingHandle.Fatalf("ListAllObjectHashes error: %v", hashError)
	}
	if len(hashes) != 3 {
		testingHandle.Errorf("expected 3 hashes, got %d", len(hashes))
	}
}

func TestListAnnotatedTagsParsing(testingHandle *testing.T) {
	forEachRefOutput := strings.Join([]string{"tag", "taghash", "v1.0.0", "Tag Tagger", "<tagger@example.test>", "targetcommit"}, fieldSeparator) + "\n" +
		strings.Join([]string{"commit", "commithash", "lightweight", "", "", ""}, fieldSeparator) + "\n"
	runner := &scriptedGitRunner{respond: func(invocation GitInvocation) (string, string, error) {
		switch {
		case argumentsContain(invocation, "for-each-ref"):
			return forEachRefOutput, "", nil
		case argumentsContain(invocation, "tag", "--list"):
			return "release message\n", "", nil
		}
		return "", "", nil
	}}
	client := newUnitClient(testingHandle, runner)
	annotatedTags, listError := client.ListAnnotatedTags()
	if listError != nil {
		testingHandle.Fatalf("unexpected error: %v", listError)
	}
	if len(annotatedTags) != 1 {
		testingHandle.Fatalf("expected 1 annotated tag (lightweight skipped), got %d", len(annotatedTags))
	}
	tag := annotatedTags[0]
	if tag.TagName != "v1.0.0" || tag.TaggerEmail != "tagger@example.test" || tag.TargetCommitHash != "targetcommit" || tag.Message != "release message" {
		testingHandle.Errorf("annotated tag parsed incorrectly: %+v", tag)
	}
}

func TestListRemotesParsing(testingHandle *testing.T) {
	runner := &scriptedGitRunner{respond: func(invocation GitInvocation) (string, string, error) {
		if argumentsContain(invocation, "remote", "--verbose") {
			return "origin\thttps://example.test/repo (fetch)\norigin\thttps://example.test/repo (push)\nbackup\t/local/mirror (fetch)\nbackup\t/local/mirror (push)\n", "", nil
		}
		return "", "", nil
	}}
	client := newUnitClient(testingHandle, runner)
	remotes, listError := client.ListRemotes()
	if listError != nil {
		testingHandle.Fatalf("unexpected error: %v", listError)
	}
	if len(remotes) != 2 {
		testingHandle.Fatalf("expected 2 deduplicated remotes, got %d: %+v", len(remotes), remotes)
	}
	if remotes[0].Name != "origin" || remotes[0].FetchURL != "https://example.test/repo" || remotes[1].Name != "backup" {
		testingHandle.Errorf("remotes parsed incorrectly: %+v", remotes)
	}
}

func TestSmallReaderParsing(testingHandle *testing.T) {
	testingHandle.Run("reachable commits", func(subTest *testing.T) {
		runner := &scriptedGitRunner{respond: func(invocation GitInvocation) (string, string, error) {
			if argumentsContain(invocation, "rev-list", "--remotes") {
				return "commitA\ncommitB\n", "", nil
			}
			return "", "", nil
		}}
		client := newUnitClient(subTest, runner)
		reachable, listError := client.ListCommitsReachableFromRemoteTrackingRefs()
		if listError != nil || len(reachable) != 2 {
			subTest.Fatalf("expected 2 reachable commits, got %v (err %v)", reachable, listError)
		}
	})

	testingHandle.Run("working tree cleanliness", func(subTest *testing.T) {
		cleanRunner := &scriptedGitRunner{respond: func(GitInvocation) (string, string, error) { return "", "", nil }}
		if isClean, _ := newUnitClient(subTest, cleanRunner).WorkingTreeIsClean(); !isClean {
			subTest.Error("empty status should be clean")
		}
		dirtyRunner := &scriptedGitRunner{respond: func(GitInvocation) (string, string, error) { return " M file.txt\n", "", nil }}
		if isClean, _ := newUnitClient(subTest, dirtyRunner).WorkingTreeIsClean(); isClean {
			subTest.Error("non-empty status should be dirty")
		}
	})

	testingHandle.Run("tracked files", func(subTest *testing.T) {
		runner := &scriptedGitRunner{respond: func(invocation GitInvocation) (string, string, error) {
			if argumentsContain(invocation, "ls-files", "-z") {
				return "a.txt\x00b/c.txt\x00", "", nil
			}
			return "", "", nil
		}}
		trackedFiles, listError := newUnitClient(subTest, runner).ListTrackedWorkingTreeFiles()
		if listError != nil || len(trackedFiles) != 2 || trackedFiles[1] != "b/c.txt" {
			subTest.Fatalf("expected 2 tracked files, got %v (err %v)", trackedFiles, listError)
		}
	})

	testingHandle.Run("object type and content", func(subTest *testing.T) {
		runner := &scriptedGitRunner{respond: func(invocation GitInvocation) (string, string, error) {
			if argumentsContain(invocation, "cat-file", "-t") {
				return "blob\n", "", nil
			}
			return "the file content", "", nil
		}}
		client := newUnitClient(subTest, runner)
		objectType, typeError := client.ReadObjectType("hash")
		if typeError != nil || objectType != "blob" {
			subTest.Fatalf("expected blob, got %q (err %v)", objectType, typeError)
		}
		content, contentError := client.ReadObjectContent("hash")
		if contentError != nil || string(content) != "the file content" {
			subTest.Fatalf("unexpected content %q (err %v)", content, contentError)
		}
	})
}

func TestListReferencesParsing(testingHandle *testing.T) {
	runner := &scriptedGitRunner{respond: func(invocation GitInvocation) (string, string, error) {
		if argumentsContain(invocation, "for-each-ref") {
			return "refs/heads/main" + fieldSeparator + "aaa\nrefs/tags/v1" + fieldSeparator + "bbb\n", "", nil
		}
		return "", "", nil
	}}
	references, listError := newUnitClient(testingHandle, runner).ListReferences()
	if listError != nil {
		testingHandle.Fatalf("unexpected error: %v", listError)
	}
	if len(references) != 2 || references[0].Name != "refs/heads/main" || references[0].ObjectHash != "aaa" || references[1].Name != "refs/tags/v1" {
		testingHandle.Errorf("references parsed incorrectly: %+v", references)
	}
}

func TestResolveHeadCommit(testingHandle *testing.T) {
	testingHandle.Run("resolves a born HEAD", func(subTest *testing.T) {
		runner := &scriptedGitRunner{respond: func(GitInvocation) (string, string, error) { return "deadbeef\n", "", nil }}
		head, headError := newUnitClient(subTest, runner).ResolveHeadCommit()
		if headError != nil || head != "deadbeef" {
			subTest.Fatalf("expected deadbeef, got %q (err %v)", head, headError)
		}
	})
	testingHandle.Run("unborn HEAD is empty, not an error", func(subTest *testing.T) {
		runner := &scriptedGitRunner{respond: func(GitInvocation) (string, string, error) {
			return "", "", errString("exit 1") // --quiet reports an unborn HEAD as a non-zero exit
		}}
		head, headError := newUnitClient(subTest, runner).ResolveHeadCommit()
		if headError != nil || head != "" {
			subTest.Fatalf("expected empty HEAD and no error, got %q (err %v)", head, headError)
		}
	})
}

// errString is a tiny error helper for injecting failures inline.
type errString string

func (message errString) Error() string { return string(message) }

func TestHasSignedCommits(testingHandle *testing.T) {
	testCases := []struct {
		caseName        string
		hasCommitsReply string
		logReply        string
		expectSigned    bool
	}{
		{caseName: "all unsigned", hasCommitsReply: "h\n", logReply: "N\nN\nN\n", expectSigned: false},
		{caseName: "one good signature", hasCommitsReply: "h\n", logReply: "N\nG\nN\n", expectSigned: true},
		{caseName: "unverifiable signature still counts", hasCommitsReply: "h\n", logReply: "E\n", expectSigned: true},
		{caseName: "empty repository has none", hasCommitsReply: "", logReply: "", expectSigned: false},
	}
	for _, testCase := range testCases {
		testingHandle.Run(testCase.caseName, func(subTest *testing.T) {
			runner := &scriptedGitRunner{respond: func(invocation GitInvocation) (string, string, error) {
				switch {
				case argumentsContain(invocation, "rev-list", "--max-count=1"):
					return testCase.hasCommitsReply, "", nil
				case argumentsContain(invocation, "log", "--all"):
					return testCase.logReply, "", nil
				}
				return "", "", nil
			}}
			hasSigned, signedError := newUnitClient(subTest, runner).HasSignedCommits()
			if signedError != nil {
				subTest.Fatalf("unexpected error: %v", signedError)
			}
			if hasSigned != testCase.expectSigned {
				subTest.Errorf("expected signed=%v, got %v", testCase.expectSigned, hasSigned)
			}
		})
	}
}

func TestExecuteWrapsRunnerError(testingHandle *testing.T) {
	runner := &scriptedGitRunner{respond: func(GitInvocation) (string, string, error) {
		return "", "fatal: something broke", errors.New("exit status 128")
	}}
	client := newUnitClient(testingHandle, runner)
	_, integrityError := client.execute(client.repositoryPath, "fsck")
	if integrityError == nil {
		testingHandle.Fatal("expected an error")
	}
	if !strings.Contains(integrityError.Error(), "something broke") {
		testingHandle.Errorf("expected stderr in wrapped error, got: %v", integrityError)
	}
}

func TestReaderErrorAndSuccessPaths(testingHandle *testing.T) {
	failingRunner := &scriptedGitRunner{respond: func(GitInvocation) (string, string, error) {
		return "", "boom", errors.New("exit 1")
	}}
	failingClient := newUnitClient(testingHandle, failingRunner)

	testingHandle.Run("commit listing fails when commit check fails", func(subTest *testing.T) {
		if _, listError := failingClient.ListAllCommitsWithMetadata(); listError == nil {
			subTest.Error("expected error when the commit check fails")
		}
	})
	testingHandle.Run("object listing fails when commit check fails", func(subTest *testing.T) {
		if _, listError := failingClient.ListAllObjectsWithPaths(); listError == nil {
			subTest.Error("expected error when the commit check fails")
		}
	})
	testingHandle.Run("annotated tag message read failure surfaces", func(subTest *testing.T) {
		runner := &scriptedGitRunner{respond: func(invocation GitInvocation) (string, string, error) {
			if argumentsContain(invocation, "for-each-ref") {
				return strings.Join([]string{"tag", "h", "v1", "T", "<t@e.test>", "c"}, fieldSeparator) + "\n", "", nil
			}
			return "", "boom", errors.New("exit 1") // the tag --list message read fails
		}}
		if _, listError := newUnitClient(subTest, runner).ListAnnotatedTags(); listError == nil {
			subTest.Error("expected error when reading a tag message fails")
		}
	})
	testingHandle.Run("fetch, fsck, verify bundle wrap errors", func(subTest *testing.T) {
		if fetchError := failingClient.FetchAllRemotes(); fetchError == nil {
			subTest.Error("expected fetch error")
		}
		if integrityError := failingClient.VerifyRepositoryIntegrity(); integrityError == nil {
			subTest.Error("expected fsck error")
		}
		if bundleError := failingClient.VerifyBundle("some.bundle"); bundleError == nil {
			subTest.Error("expected bundle verify error")
		}
		if _, remotesError := failingClient.ListRemotes(); remotesError == nil {
			subTest.Error("expected remotes error")
		}
	})

	succeedingRunner := &scriptedGitRunner{respond: func(GitInvocation) (string, string, error) { return "", "", nil }}
	succeedingClient := newUnitClient(testingHandle, succeedingRunner)
	testingHandle.Run("bundle, mirror, fetch success paths", func(subTest *testing.T) {
		if bundleError := succeedingClient.CreateAllRefsBundle("dest.bundle"); bundleError != nil {
			subTest.Errorf("unexpected bundle error: %v", bundleError)
		}
		if verifyError := succeedingClient.VerifyBundle("dest.bundle"); verifyError != nil {
			subTest.Errorf("unexpected bundle verify error: %v", verifyError)
		}
		if mirrorError := succeedingClient.CloneMirrorOfRemote("https://example.test/repo", "dest.git"); mirrorError != nil {
			subTest.Errorf("unexpected mirror error: %v", mirrorError)
		}
		if fetchError := succeedingClient.FetchAllRemotes(); fetchError != nil {
			subTest.Errorf("unexpected fetch error: %v", fetchError)
		}
	})
}

func TestValidationOnlyMethods(testingHandle *testing.T) {
	runner := &scriptedGitRunner{}
	client := newUnitClient(testingHandle, runner)

	if bundleError := client.CreateAllRefsBundle(""); bundleError == nil {
		testingHandle.Error("expected error for empty bundle destination")
	}
	if cloneError := client.CloneMirrorOfRemote("", "dest"); cloneError == nil {
		testingHandle.Error("expected error for empty remote URL")
	}
	if cloneError := client.CloneMirrorOfRemote("url", ""); cloneError == nil {
		testingHandle.Error("expected error for empty mirror destination")
	}
	// None of the pure-validation failures should have reached the runner.
	if len(runner.invocations) != 0 {
		testingHandle.Errorf("validation failures should not invoke git, got %d", len(runner.invocations))
	}
}
