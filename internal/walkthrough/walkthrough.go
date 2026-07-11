// Package walkthrough is the kind front door. A bare `alive` (or `alive guide`)
// walks someone through the whole thing one calm step at a time: trace first,
// then the safe mailmap fix, then, only if they choose it and confirm twice,
// a verified backup and the history rewrite.
//
// It is a convenience layer over the same gated operations, never a shortcut
// around them. Every dependency is an interface, so it can be driven by scripted
// tests, and so it structurally has no way to push or commit: no dependency
// exposes either.
package walkthrough

import (
	"strings"

	"alive.name/internal/backup"
	"alive.name/internal/domain"
	"alive.name/internal/identity"
	"alive.name/internal/reclaim"
	"alive.name/internal/trace"
)

// Prompter abstracts all interaction, so tests can supply scripted answers and
// capture output without a real terminal.
type Prompter interface {
	Confirm(question string) (bool, error)
	AskText(question string) (string, error)
	Show(message string)
}

// Tracer runs the read-only scan.
type Tracer interface {
	Trace(oldIdentity domain.OldIdentity) (trace.Report, error)
}

// ReportRenderer turns a trace report into text.
type ReportRenderer interface {
	Render(report trace.Report) string
}

// Mender is the safe, display-only mailmap fix.
type Mender interface {
	Generate(oldIdentity domain.OldIdentity, newIdentity domain.NewIdentity) (string, error)
	Write(repositoryPath, mailmapContent string) error
}

// Backuper creates a verified backup.
type Backuper interface {
	CreateVerified() (backup.Record, error)
}

// Reclaimer runs the gated history rewrite.
type Reclaimer interface {
	History(backupRecord backup.Record, plan reclaim.Plan) error
}

// Walkthrough holds the dependencies for the guided flow.
type Walkthrough struct {
	Prompter       Prompter
	Tracer         Tracer
	Renderer       ReportRenderer
	Mender         Mender
	Backuper       Backuper
	Reclaimer      Reclaimer
	RepositoryPath string
}

// Run performs the guided flow, asking at every branch and assuming nothing.
func (walkthrough *Walkthrough) Run() error {
	walkthrough.Prompter.Show("Your name is yours. Let's find where the old one still lives, and put you back in control, one calm step at a time.")

	oldIdentity, newIdentity, gatherError := walkthrough.gatherIdentities()
	if gatherError != nil {
		return gatherError
	}

	traceReport, traceError := walkthrough.Tracer.Trace(oldIdentity)
	if traceError != nil {
		walkthrough.Prompter.Show("I couldn't finish scanning this repository: " + traceError.Error())
		return traceError
	}
	walkthrough.Prompter.Show(walkthrough.Renderer.Render(traceReport))

	if len(traceReport.Occurrences) == 0 {
		walkthrough.Prompter.Show("There is nothing to do here: your old name doesn't appear anywhere this scan looked. You can stop with an easy mind.")
		return nil
	}

	if mendError := walkthrough.offerMend(oldIdentity, newIdentity); mendError != nil {
		return mendError
	}

	return walkthrough.offerDeepRewrite(oldIdentity, newIdentity)
}

func (walkthrough *Walkthrough) gatherIdentities() (domain.OldIdentity, domain.NewIdentity, error) {
	oldNamesRaw, namesError := walkthrough.Prompter.AskText("What name are you moving away from? (you can list a few, separated by commas)")
	if namesError != nil {
		return domain.OldIdentity{}, domain.NewIdentity{}, namesError
	}
	oldEmailsRaw, emailsError := walkthrough.Prompter.AskText("And any old email addresses tied to it? (comma separated, or leave blank)")
	if emailsError != nil {
		return domain.OldIdentity{}, domain.NewIdentity{}, emailsError
	}

	oldIdentity, warnings, oldError := identity.ParseOld(splitOnCommas(oldNamesRaw), splitOnCommas(oldEmailsRaw))
	if oldError != nil {
		walkthrough.Prompter.Show("I need at least one old name or email to search for: " + oldError.Error())
		return domain.OldIdentity{}, domain.NewIdentity{}, oldError
	}
	for _, warning := range warnings {
		walkthrough.Prompter.Show("A gentle heads-up: " + warning.Message)
	}

	newNameRaw, newNameError := walkthrough.Prompter.AskText("And the name that is yours now?")
	if newNameError != nil {
		return domain.OldIdentity{}, domain.NewIdentity{}, newNameError
	}
	newEmailRaw, newEmailError := walkthrough.Prompter.AskText("With which email?")
	if newEmailError != nil {
		return domain.OldIdentity{}, domain.NewIdentity{}, newEmailError
	}

	newIdentity, newError := identity.ParseNew(newNameRaw, newEmailRaw)
	if newError != nil {
		walkthrough.Prompter.Show("I need a name and an email for who you are now: " + newError.Error())
		return domain.OldIdentity{}, domain.NewIdentity{}, newError
	}
	return oldIdentity, newIdentity, nil
}

func (walkthrough *Walkthrough) offerMend(oldIdentity domain.OldIdentity, newIdentity domain.NewIdentity) error {
	wantsMend, confirmError := walkthrough.Prompter.Confirm("Would you like the safe fix first? It writes a .mailmap so git shows your new name everywhere, without touching history at all. It's fully reversible.")
	if confirmError != nil {
		return confirmError
	}
	if !wantsMend {
		return nil
	}
	mailmapContent, generateError := walkthrough.Mender.Generate(oldIdentity, newIdentity)
	if generateError != nil {
		walkthrough.Prompter.Show("I couldn't build the mailmap: " + generateError.Error())
		return generateError
	}
	if writeError := walkthrough.Mender.Write(walkthrough.RepositoryPath, mailmapContent); writeError != nil {
		walkthrough.Prompter.Show("I couldn't write the .mailmap file: " + writeError.Error())
		return writeError
	}
	walkthrough.Prompter.Show("Done. I wrote a .mailmap. Your history is completely untouched; this only changes how names are displayed. Commit it yourself when you're ready.")
	return nil
}

func (walkthrough *Walkthrough) offerDeepRewrite(oldIdentity domain.OldIdentity, newIdentity domain.NewIdentity) error {
	wantsDeep, confirmError := walkthrough.Prompter.Confirm("Do you also want the old name gone from history itself? This is the deep fix. It is powerful and it has real costs: every commit's ID changes, any signatures break, and publishing it means a force-push you run yourself, later.")
	if confirmError != nil {
		return confirmError
	}
	if !wantsDeep {
		walkthrough.Prompter.Show("That's a completely fine place to stop. Nothing destructive was run. Your name will now display correctly if you kept the mailmap.")
		return nil
	}

	walkthrough.Prompter.Show("Before anything is touched, I'll make a full, verified backup. Nothing proceeds unless it passes.")
	backupRecord, backupError := walkthrough.Backuper.CreateVerified()
	if backupError != nil {
		walkthrough.Prompter.Show("The backup could not be completed and verified, so I will not touch your history. Reason: " + backupError.Error())
		return backupError
	}
	if backupRecord.VerificationStatus != domain.VerificationPassed {
		walkthrough.Prompter.Show("The backup did not verify, so I'm refusing to rewrite anything. Your repository is untouched.")
		return nil
	}
	walkthrough.Prompter.Show("Backup verified and saved at: " + backupRecord.BackupDirectoryPath)

	walkthrough.Prompter.Show("Here is a dry run, a preview of the rewrite. Nothing changes yet:")
	dryRunPlan := reclaim.Plan{OldIdentity: oldIdentity, NewIdentity: newIdentity, Apply: false, Output: newShowWriter(walkthrough.Prompter)}
	if dryRunError := walkthrough.Reclaimer.History(backupRecord, dryRunPlan); dryRunError != nil {
		walkthrough.Prompter.Show("The dry run couldn't complete: " + dryRunError.Error())
		return dryRunError
	}

	wantsApply, applyConfirmError := walkthrough.Prompter.Confirm("Shall I apply this rewrite for real? It cannot be undone except from the backup you just made.")
	if applyConfirmError != nil {
		return applyConfirmError
	}
	if !wantsApply {
		walkthrough.Prompter.Show("Stopped, and that's okay. Nothing was rewritten. Your backup is at " + backupRecord.BackupDirectoryPath + " if you want it.")
		return nil
	}

	applyPlan := reclaim.Plan{
		OldIdentity:              oldIdentity,
		NewIdentity:              newIdentity,
		Apply:                    true,
		AcknowledgeSignedCommits: true,
		AcknowledgePushedHistory: true,
		Output:                   newShowWriter(walkthrough.Prompter),
	}
	if applyError := walkthrough.Reclaimer.History(backupRecord, applyPlan); applyError != nil {
		walkthrough.Prompter.Show("The rewrite couldn't complete: " + applyError.Error() + "\nYour backup is safe at " + backupRecord.BackupDirectoryPath)
		return applyError
	}

	walkthrough.Prompter.Show("It's done, and it's yours. History now carries your name. Your backup is at " + backupRecord.BackupDirectoryPath + ". Review the changes in your own git tooling; the exact publish commands were printed above. This tool never pushes or commits for you; those last steps are yours.")
	return nil
}

func splitOnCommas(rawValue string) []string {
	if strings.TrimSpace(rawValue) == "" {
		return nil
	}
	parts := strings.Split(rawValue, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			values = append(values, trimmed)
		}
	}
	return values
}

// showWriter adapts a Prompter's Show to an io.Writer so reclaim's output flows
// through the same channel as everything else.
type showWriter struct {
	prompter Prompter
}

func newShowWriter(prompter Prompter) showWriter { return showWriter{prompter: prompter} }

func (writer showWriter) Write(payload []byte) (int, error) {
	message := strings.TrimRight(string(payload), "\n")
	if message != "" {
		writer.prompter.Show(message)
	}
	return len(payload), nil
}
