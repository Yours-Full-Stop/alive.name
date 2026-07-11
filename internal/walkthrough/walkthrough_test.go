package walkthrough

import (
	"errors"
	"strings"
	"testing"

	"alive.name/internal/backup"
	"alive.name/internal/domain"
	"alive.name/internal/reclaim"
	"alive.name/internal/trace"
)

// Unit tests: the guided flow driven by a scripted prompter and fakes. Nothing
// here touches git or the filesystem. None of the dependencies can push or
// commit, so the flow structurally cannot either.

type scriptedPrompter struct {
	confirmAnswers []bool
	textAnswers    []string
	confirmIndex   int
	textIndex      int
	shown          []string
	confirmErr     error
	textErr        error
}

func (prompter *scriptedPrompter) Confirm(string) (bool, error) {
	if prompter.confirmErr != nil {
		return false, prompter.confirmErr
	}
	if prompter.confirmIndex >= len(prompter.confirmAnswers) {
		return false, errors.New("scriptedPrompter: ran out of confirm answers")
	}
	answer := prompter.confirmAnswers[prompter.confirmIndex]
	prompter.confirmIndex++
	return answer, nil
}

func (prompter *scriptedPrompter) AskText(string) (string, error) {
	if prompter.textErr != nil {
		return "", prompter.textErr
	}
	if prompter.textIndex >= len(prompter.textAnswers) {
		return "", errors.New("scriptedPrompter: ran out of text answers")
	}
	answer := prompter.textAnswers[prompter.textIndex]
	prompter.textIndex++
	return answer, nil
}

func (prompter *scriptedPrompter) Show(message string) {
	prompter.shown = append(prompter.shown, message)
}

func (prompter *scriptedPrompter) shownText() string { return strings.Join(prompter.shown, "\n") }

type fakeTracer struct {
	report trace.Report
	err    error
}

func (tracer fakeTracer) Trace(domain.OldIdentity) (trace.Report, error) {
	return tracer.report, tracer.err
}

type fakeRenderer struct{}

func (fakeRenderer) Render(trace.Report) string { return "RENDERED REPORT" }

type fakeMender struct {
	generateErr error
	writeErr    error
	wrote       bool
}

func (mender *fakeMender) Generate(domain.OldIdentity, domain.NewIdentity) (string, error) {
	if mender.generateErr != nil {
		return "", mender.generateErr
	}
	return "MAILMAP CONTENT", nil
}

func (mender *fakeMender) Write(string, string) error {
	mender.wrote = true
	return mender.writeErr
}

type fakeBackuper struct {
	record backup.Record
	err    error
	called int
}

func (backuper *fakeBackuper) CreateVerified() (backup.Record, error) {
	backuper.called++
	return backuper.record, backuper.err
}

type fakeReclaimer struct {
	calls  []reclaim.Plan
	errors []error
}

func (reclaimer *fakeReclaimer) History(_ backup.Record, plan reclaim.Plan) error {
	callIndex := len(reclaimer.calls)
	reclaimer.calls = append(reclaimer.calls, plan)
	if callIndex < len(reclaimer.errors) {
		return reclaimer.errors[callIndex]
	}
	return nil
}

func reportWithOccurrences() trace.Report {
	return trace.Report{
		Occurrences:           []domain.DeadnameOccurrence{{CommitHash: "c1", Location: domain.LocationAuthorName}},
		OccurrenceCountByZone: map[domain.OccurrenceZone]int{domain.ZoneLocalRewritable: 1},
	}
}

func passedBackupRecord() backup.Record {
	return backup.Record{VerificationStatus: domain.VerificationPassed, BackupDirectoryPath: "/backups/here"}
}

func newWalkthrough(prompter Prompter, tracer Tracer, mender Mender, backuper Backuper, reclaimer Reclaimer) *Walkthrough {
	return &Walkthrough{
		Prompter:       prompter,
		Tracer:         tracer,
		Renderer:       fakeRenderer{},
		Mender:         mender,
		Backuper:       backuper,
		Reclaimer:      reclaimer,
		RepositoryPath: "/repo",
	}
}

func identityAnswers() []string {
	return []string{"Old Name", "old@example.test", "New Name", "new@example.test"}
}

func TestWalkthroughHappyPath(testingHandle *testing.T) {
	prompter := &scriptedPrompter{textAnswers: identityAnswers(), confirmAnswers: []bool{true, true, true}}
	mender := &fakeMender{}
	backuper := &fakeBackuper{record: passedBackupRecord()}
	reclaimer := &fakeReclaimer{}
	walkthrough := newWalkthrough(prompter, fakeTracer{report: reportWithOccurrences()}, mender, backuper, reclaimer)

	if runError := walkthrough.Run(); runError != nil {
		testingHandle.Fatalf("Run: %v", runError)
	}
	if !mender.wrote {
		testingHandle.Error("expected the mailmap to be written")
	}
	if backuper.called != 1 {
		testingHandle.Errorf("expected one backup, got %d", backuper.called)
	}
	if len(reclaimer.calls) != 2 {
		testingHandle.Fatalf("expected a dry run then an apply, got %d calls", len(reclaimer.calls))
	}
	if reclaimer.calls[0].Apply {
		testingHandle.Error("first reclaim call should be a dry run")
	}
	if !reclaimer.calls[1].Apply || !reclaimer.calls[1].AcknowledgeSignedCommits || !reclaimer.calls[1].AcknowledgePushedHistory {
		testingHandle.Errorf("apply call should be a real, acknowledged run: %+v", reclaimer.calls[1])
	}
	if !strings.Contains(prompter.shownText(), "never pushes or commits") {
		testingHandle.Error("expected the closing message to state the tool never pushes or commits")
	}
}

func TestWalkthroughDeclinesEverything(testingHandle *testing.T) {
	prompter := &scriptedPrompter{textAnswers: identityAnswers(), confirmAnswers: []bool{false, false}}
	mender := &fakeMender{}
	backuper := &fakeBackuper{record: passedBackupRecord()}
	reclaimer := &fakeReclaimer{}
	walkthrough := newWalkthrough(prompter, fakeTracer{report: reportWithOccurrences()}, mender, backuper, reclaimer)

	if runError := walkthrough.Run(); runError != nil {
		testingHandle.Fatalf("Run: %v", runError)
	}
	if mender.wrote {
		testingHandle.Error("mailmap should not be written when mend is declined")
	}
	if backuper.called != 0 || len(reclaimer.calls) != 0 {
		testingHandle.Error("nothing destructive should run when the deep path is declined")
	}
	if !strings.Contains(prompter.shownText(), "fine place to stop") {
		testingHandle.Error("expected a kind stopping message")
	}
}

func TestWalkthroughNothingToDoStopsEarly(testingHandle *testing.T) {
	prompter := &scriptedPrompter{textAnswers: identityAnswers()}
	backuper := &fakeBackuper{}
	reclaimer := &fakeReclaimer{}
	walkthrough := newWalkthrough(prompter, fakeTracer{report: trace.Report{}}, &fakeMender{}, backuper, reclaimer)

	if runError := walkthrough.Run(); runError != nil {
		testingHandle.Fatalf("Run: %v", runError)
	}
	if backuper.called != 0 || len(reclaimer.calls) != 0 {
		testingHandle.Error("nothing destructive should be offered when there is nothing to do")
	}
	if !strings.Contains(prompter.shownText(), "nothing to do") {
		testingHandle.Errorf("expected a nothing-to-do message, got %q", prompter.shownText())
	}
}

func TestWalkthroughBackupFailureRefusesReclaim(testingHandle *testing.T) {
	prompter := &scriptedPrompter{textAnswers: identityAnswers(), confirmAnswers: []bool{false, true}}
	backuper := &fakeBackuper{record: backup.Record{VerificationStatus: domain.VerificationFailed}, err: errors.New("verification failed")}
	reclaimer := &fakeReclaimer{}
	walkthrough := newWalkthrough(prompter, fakeTracer{report: reportWithOccurrences()}, &fakeMender{}, backuper, reclaimer)

	if runError := walkthrough.Run(); runError == nil {
		testingHandle.Fatal("expected an error when the backup fails")
	}
	if len(reclaimer.calls) != 0 {
		testingHandle.Error("reclaim must not run when the backup fails")
	}
	if !strings.Contains(prompter.shownText(), "will not touch your history") {
		testingHandle.Errorf("expected a clear backup-failure message, got %q", prompter.shownText())
	}
}

func TestWalkthroughBackupUnverifiedRefusesReclaim(testingHandle *testing.T) {
	prompter := &scriptedPrompter{textAnswers: identityAnswers(), confirmAnswers: []bool{false, true}}
	backuper := &fakeBackuper{record: backup.Record{VerificationStatus: domain.VerificationNotYetRun}}
	reclaimer := &fakeReclaimer{}
	walkthrough := newWalkthrough(prompter, fakeTracer{report: reportWithOccurrences()}, &fakeMender{}, backuper, reclaimer)

	if runError := walkthrough.Run(); runError != nil {
		testingHandle.Fatalf("an unverified backup should stop cleanly, got: %v", runError)
	}
	if len(reclaimer.calls) != 0 {
		testingHandle.Error("reclaim must not run when the backup did not verify")
	}
	if !strings.Contains(prompter.shownText(), "refusing to rewrite") {
		testingHandle.Errorf("expected a refusal message, got %q", prompter.shownText())
	}
}

func TestWalkthroughApplyDeclinedAfterDryRun(testingHandle *testing.T) {
	prompter := &scriptedPrompter{textAnswers: identityAnswers(), confirmAnswers: []bool{false, true, false}}
	reclaimer := &fakeReclaimer{}
	walkthrough := newWalkthrough(prompter, fakeTracer{report: reportWithOccurrences()}, &fakeMender{}, &fakeBackuper{record: passedBackupRecord()}, reclaimer)

	if runError := walkthrough.Run(); runError != nil {
		testingHandle.Fatalf("Run: %v", runError)
	}
	if len(reclaimer.calls) != 1 || reclaimer.calls[0].Apply {
		testingHandle.Errorf("expected only a dry run, got %+v", reclaimer.calls)
	}
	if !strings.Contains(prompter.shownText(), "Stopped") {
		testingHandle.Error("expected a stopped message after declining the apply")
	}
}

func TestWalkthroughErrorPaths(testingHandle *testing.T) {
	testingHandle.Run("trace failure surfaces a clear message", func(subTest *testing.T) {
		prompter := &scriptedPrompter{textAnswers: identityAnswers()}
		walkthrough := newWalkthrough(prompter, fakeTracer{err: errors.New("git missing")}, &fakeMender{}, &fakeBackuper{}, &fakeReclaimer{})
		if runError := walkthrough.Run(); runError == nil {
			subTest.Fatal("expected an error")
		}
		if !strings.Contains(prompter.shownText(), "couldn't finish scanning") {
			subTest.Errorf("expected a clear scan-failure message, got %q", prompter.shownText())
		}
	})

	testingHandle.Run("dry-run failure stops before apply", func(subTest *testing.T) {
		prompter := &scriptedPrompter{textAnswers: identityAnswers(), confirmAnswers: []bool{false, true}}
		reclaimer := &fakeReclaimer{errors: []error{errors.New("git filter-repo not found")}}
		walkthrough := newWalkthrough(prompter, fakeTracer{report: reportWithOccurrences()}, &fakeMender{}, &fakeBackuper{record: passedBackupRecord()}, reclaimer)
		if runError := walkthrough.Run(); runError == nil {
			subTest.Fatal("expected an error from the dry run")
		}
		if len(reclaimer.calls) != 1 {
			subTest.Errorf("apply must not be reached when the dry run fails, got %d calls", len(reclaimer.calls))
		}
		if !strings.Contains(prompter.shownText(), "dry run couldn't complete") {
			subTest.Errorf("expected a clear dry-run failure message, got %q", prompter.shownText())
		}
	})

	testingHandle.Run("invalid new identity surfaces a message", func(subTest *testing.T) {
		prompter := &scriptedPrompter{textAnswers: []string{"Old Name", "", "", ""}}
		walkthrough := newWalkthrough(prompter, fakeTracer{report: reportWithOccurrences()}, &fakeMender{}, &fakeBackuper{}, &fakeReclaimer{})
		if runError := walkthrough.Run(); runError == nil {
			subTest.Fatal("expected an error for an empty new identity")
		}
		if !strings.Contains(prompter.shownText(), "who you are now") {
			subTest.Errorf("expected a clear identity message, got %q", prompter.shownText())
		}
	})

	testingHandle.Run("no old identity surfaces a message", func(subTest *testing.T) {
		prompter := &scriptedPrompter{textAnswers: []string{"", "", "New Name", "new@example.test"}}
		walkthrough := newWalkthrough(prompter, fakeTracer{report: reportWithOccurrences()}, &fakeMender{}, &fakeBackuper{}, &fakeReclaimer{})
		if runError := walkthrough.Run(); runError == nil {
			subTest.Fatal("expected an error for an empty old identity")
		}
		if !strings.Contains(prompter.shownText(), "at least one old name or email") {
			subTest.Errorf("expected a clear old-identity message, got %q", prompter.shownText())
		}
	})

	testingHandle.Run("asking for input can fail", func(subTest *testing.T) {
		prompter := &scriptedPrompter{textErr: errors.New("stdin closed")}
		walkthrough := newWalkthrough(prompter, fakeTracer{report: reportWithOccurrences()}, &fakeMender{}, &fakeBackuper{}, &fakeReclaimer{})
		if runError := walkthrough.Run(); runError == nil {
			subTest.Fatal("expected an error when input cannot be read")
		}
	})

	testingHandle.Run("a prompt confirmation can fail", func(subTest *testing.T) {
		prompter := &scriptedPrompter{textAnswers: identityAnswers(), confirmErr: errors.New("terminal gone")}
		walkthrough := newWalkthrough(prompter, fakeTracer{report: reportWithOccurrences()}, &fakeMender{}, &fakeBackuper{}, &fakeReclaimer{})
		if runError := walkthrough.Run(); runError == nil {
			subTest.Fatal("expected an error when a confirmation fails")
		}
	})

	testingHandle.Run("mailmap generation failure surfaces", func(subTest *testing.T) {
		prompter := &scriptedPrompter{textAnswers: identityAnswers(), confirmAnswers: []bool{true}}
		mender := &fakeMender{generateErr: errors.New("bad identity")}
		walkthrough := newWalkthrough(prompter, fakeTracer{report: reportWithOccurrences()}, mender, &fakeBackuper{}, &fakeReclaimer{})
		if runError := walkthrough.Run(); runError == nil {
			subTest.Fatal("expected an error when mailmap generation fails")
		}
		if !strings.Contains(prompter.shownText(), "couldn't build the mailmap") {
			subTest.Errorf("expected a clear generation-failure message, got %q", prompter.shownText())
		}
	})

	testingHandle.Run("mailmap write failure surfaces", func(subTest *testing.T) {
		prompter := &scriptedPrompter{textAnswers: identityAnswers(), confirmAnswers: []bool{true}}
		mender := &fakeMender{writeErr: errors.New("read-only filesystem")}
		walkthrough := newWalkthrough(prompter, fakeTracer{report: reportWithOccurrences()}, mender, &fakeBackuper{}, &fakeReclaimer{})
		if runError := walkthrough.Run(); runError == nil {
			subTest.Fatal("expected an error when the mailmap write fails")
		}
		if !strings.Contains(prompter.shownText(), "couldn't write the .mailmap") {
			subTest.Errorf("expected a clear write-failure message, got %q", prompter.shownText())
		}
	})

	testingHandle.Run("apply failure keeps the backup safe", func(subTest *testing.T) {
		prompter := &scriptedPrompter{textAnswers: identityAnswers(), confirmAnswers: []bool{false, true, true}}
		reclaimer := &fakeReclaimer{errors: []error{nil, errors.New("filter-repo exploded")}}
		walkthrough := newWalkthrough(prompter, fakeTracer{report: reportWithOccurrences()}, &fakeMender{}, &fakeBackuper{record: passedBackupRecord()}, reclaimer)
		if runError := walkthrough.Run(); runError == nil {
			subTest.Fatal("expected an error when the apply fails")
		}
		if !strings.Contains(prompter.shownText(), "backup is safe") {
			subTest.Errorf("expected reassurance about the backup, got %q", prompter.shownText())
		}
	})
}
