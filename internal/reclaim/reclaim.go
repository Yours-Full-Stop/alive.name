// Package reclaim performs the history rewrite via git filter-repo, but only
// behind a wall of gates: a verified backup must exist, filter-repo must be
// installed, the working tree must be clean, and signed or already-published
// history must be explicitly acknowledged. It is dry-run by default.
//
// After a successful rewrite it never pushes. It prints the exact commands for
// the user to review and publish themselves, and stops.
package reclaim

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"alive.name/internal/backup"
	"alive.name/internal/domain"
	"alive.name/internal/gitclient"
	"alive.name/internal/mend"
	"alive.name/internal/trace"
)

// Plan describes a rewrite. Its zero value is a dry run, which is the safe
// default: a real rewrite requires Apply to be set explicitly.
type Plan struct {
	OldIdentity domain.OldIdentity
	NewIdentity domain.NewIdentity
	// Apply performs the real rewrite. When false (the default) the run is a
	// dry run that changes nothing.
	Apply bool
	// AcknowledgeSignedCommits must be set to apply a rewrite over signed commits.
	AcknowledgeSignedCommits bool
	// AcknowledgePushedHistory must be set to apply a rewrite over history that
	// is already on a remote.
	AcknowledgePushedHistory bool
	// Output receives progress, the dry-run report, and the review-and-publish
	// instructions. A nil Output discards them.
	Output io.Writer
}

// FilterRepoRunner runs git filter-repo. It is an interface so tests can inject a
// spy or fake in place of the real, destructive tool.
type FilterRepoRunner interface {
	EnsureAvailable() error
	Run(repositoryPath string, arguments []string) (output string, runError error)
}

// repositoryController is the read-only git surface reclaim needs before it acts.
type repositoryController interface {
	RepositoryPath() string
	WorkingTreeIsClean() (bool, error)
	ListUntrackedFiles() ([]string, error)
	HasSignedCommits() (bool, error)
	ListRemotes() ([]gitclient.Remote, error)
}

// occurrenceCounter reports how many occurrences of the old identity exist, so a
// rewrite with nothing to do can be reported as such rather than run pointlessly.
type occurrenceCounter func(oldIdentity domain.OldIdentity) (int, error)

// History runs the rewrite for the given plan against the client's repository,
// using the real filter-repo tool.
func History(client *gitclient.GitClient, backupRecord backup.Record, plan Plan) error {
	return HistoryWithFilterRepoRunner(client, defaultFilterRepoRunner{}, backupRecord, plan)
}

// HistoryWithFilterRepoRunner is History with an injectable filter-repo runner,
// for wiring a spy in tests or an alternative implementation.
func HistoryWithFilterRepoRunner(client *gitclient.GitClient, filterRepo FilterRepoRunner, backupRecord backup.Record, plan Plan) error {
	counter := func(oldIdentity domain.OldIdentity) (int, error) {
		traceReport, traceError := trace.OldNameAcrossRepository(client, oldIdentity, trace.Options{IncludeHistoricalFileContents: true})
		if traceError != nil {
			return 0, traceError
		}
		return len(traceReport.Occurrences), nil
	}
	return history(client, filterRepo, counter, backupRecord, plan)
}

func history(controller repositoryController, filterRepo FilterRepoRunner, countOccurrences occurrenceCounter, backupRecord backup.Record, plan Plan) error {
	output := plan.Output
	if output == nil {
		output = io.Discard
	}

	occurrenceCount, countError := countOccurrences(plan.OldIdentity)
	if countError != nil {
		return fmt.Errorf("reclaim: checking for occurrences: %w", countError)
	}
	if occurrenceCount == 0 {
		fmt.Fprintln(output, "Nothing to do: the old name does not appear anywhere this rewrite would reach.")
		return nil
	}

	if backupRecord.VerificationStatus != domain.VerificationPassed {
		return fmt.Errorf("reclaim: refusing to rewrite history without a verified backup (backup status: %s)", backupRecord.VerificationStatus)
	}

	if availabilityError := filterRepo.EnsureAvailable(); availabilityError != nil {
		return fmt.Errorf("reclaim: git filter-repo is required but was not found; install it with 'pip install git-filter-repo': %w", availabilityError)
	}

	workingTreeClean, cleanError := controller.WorkingTreeIsClean()
	if cleanError != nil {
		return fmt.Errorf("reclaim: checking the working tree: %w", cleanError)
	}
	if !workingTreeClean {
		return fmt.Errorf("reclaim: refusing to rewrite history with uncommitted changes to tracked files; commit or stash them first")
	}

	if reportError := reportUntrackedFiles(controller, output); reportError != nil {
		return reportError
	}

	if gateError := checkSignedCommitGate(controller, plan, output); gateError != nil {
		return gateError
	}
	if gateError := checkPushedHistoryGate(controller, plan, output); gateError != nil {
		return gateError
	}

	mailmapContent, mailmapError := mend.GenerateMailmap(plan.OldIdentity, plan.NewIdentity)
	if mailmapError != nil {
		return fmt.Errorf("reclaim: building mailmap: %w", mailmapError)
	}
	replaceTextContent := buildReplaceTextExpressions(plan.OldIdentity, plan.NewIdentity)

	workingDirectory, workingDirectoryError := os.MkdirTemp("", "alive-reclaim-")
	if workingDirectoryError != nil {
		return fmt.Errorf("reclaim: creating a temporary working directory: %w", workingDirectoryError)
	}
	defer func() { _ = os.RemoveAll(workingDirectory) }()

	mailmapPath := filepath.Join(workingDirectory, "mailmap")
	if writeError := os.WriteFile(mailmapPath, []byte(mailmapContent), 0o644); writeError != nil {
		return fmt.Errorf("reclaim: writing mailmap: %w", writeError)
	}
	replaceTextPath := ""
	if replaceTextContent != "" {
		replaceTextPath = filepath.Join(workingDirectory, "replace-text")
		if writeError := os.WriteFile(replaceTextPath, []byte(replaceTextContent), 0o644); writeError != nil {
			return fmt.Errorf("reclaim: writing replace-text rules: %w", writeError)
		}
	}

	filterRepoArguments := buildFilterRepoArguments(mailmapPath, replaceTextPath, plan.Apply)
	filterRepoOutput, runError := filterRepo.Run(controller.RepositoryPath(), filterRepoArguments)
	if strings.TrimSpace(filterRepoOutput) != "" {
		fmt.Fprintln(output, strings.TrimRight(filterRepoOutput, "\n"))
	}
	if runError != nil {
		return fmt.Errorf("reclaim: git filter-repo failed: %w", runError)
	}

	if !plan.Apply {
		fmt.Fprintln(output, "\nThis was a dry run. Nothing has changed. Re-run with the apply option to rewrite history for real.")
		return nil
	}

	remotes, remotesError := controller.ListRemotes()
	if remotesError != nil {
		return fmt.Errorf("reclaim: listing remotes for publish instructions: %w", remotesError)
	}
	fmt.Fprint(output, buildPublishInstructions(remotes))
	return nil
}

// maxUntrackedFilesToList caps how many untracked paths the transparency note
// prints, so a working tree full of build output or editor folders does not
// flood the screen.
const maxUntrackedFilesToList = 10

// reportUntrackedFiles tells the user, before the rewrite proceeds, about any
// untracked files in the working tree. The dirty-tree gate deliberately ignores
// untracked files because a history rewrite never touches them; this note keeps
// that decision transparent so their presence is never a silent surprise.
func reportUntrackedFiles(controller repositoryController, output io.Writer) error {
	untrackedFiles, listError := controller.ListUntrackedFiles()
	if listError != nil {
		return fmt.Errorf("reclaim: listing untracked files: %w", listError)
	}
	if len(untrackedFiles) == 0 {
		return nil
	}

	fmt.Fprintf(output, "Note: %s present in your working tree. A history rewrite does not touch untracked files, so they are left exactly as they are:\n", describeUntrackedCount(len(untrackedFiles)))
	for index, untrackedPath := range untrackedFiles {
		if index == maxUntrackedFilesToList {
			fmt.Fprintf(output, "  ... and %d more\n", len(untrackedFiles)-maxUntrackedFilesToList)
			break
		}
		fmt.Fprintf(output, "  %s\n", untrackedPath)
	}
	return nil
}

// describeUntrackedCount renders the count with correct singular or plural
// agreement, so the note reads naturally for one file or many.
func describeUntrackedCount(count int) string {
	if count == 1 {
		return "1 untracked file is"
	}
	return fmt.Sprintf("%d untracked files are", count)
}

func checkSignedCommitGate(controller repositoryController, plan Plan, output io.Writer) error {
	hasSignedCommits, signedError := controller.HasSignedCommits()
	if signedError != nil {
		return fmt.Errorf("reclaim: checking for signed commits: %w", signedError)
	}
	if !hasSignedCommits {
		return nil
	}
	fmt.Fprintln(output, "Warning: this history contains signed commits. A rewrite will break those signatures.")
	if plan.Apply && !plan.AcknowledgeSignedCommits {
		return fmt.Errorf("reclaim: refusing to rewrite signed commits without acknowledgement; the signatures will be broken")
	}
	return nil
}

func checkPushedHistoryGate(controller repositoryController, plan Plan, output io.Writer) error {
	remotes, remotesError := controller.ListRemotes()
	if remotesError != nil {
		return fmt.Errorf("reclaim: checking remotes: %w", remotesError)
	}
	if len(remotes) == 0 {
		return nil
	}
	fmt.Fprintln(output, "Warning: this repository has a remote, so some of this history may already be published. Rewriting it means a force-push you will run yourself.")
	if plan.Apply && !plan.AcknowledgePushedHistory {
		return fmt.Errorf("reclaim: refusing to rewrite already-published history without acknowledgement")
	}
	return nil
}

// buildReplaceTextExpressions produces git filter-repo replace-text rules for the
// old names and emails that actually change. Values are matched literally to
// avoid treating names with regex metacharacters as patterns.
func buildReplaceTextExpressions(oldIdentity domain.OldIdentity, newIdentity domain.NewIdentity) string {
	lines := make([]string, 0)
	appendRule := func(oldValue, newValue string) {
		trimmedOld := strings.TrimSpace(oldValue)
		if trimmedOld == "" || trimmedOld == strings.TrimSpace(newValue) {
			return
		}
		lines = append(lines, fmt.Sprintf("%s==>%s", trimmedOld, newValue))
	}
	for _, oldName := range oldIdentity.Names {
		appendRule(oldName, newIdentity.Name)
	}
	for _, oldEmail := range oldIdentity.Emails {
		appendRule(oldEmail, newIdentity.Email)
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// buildFilterRepoArguments builds the filter-repo command. --force lets it run on
// a repository that is not a fresh clone (we have our own verified backup).
func buildFilterRepoArguments(mailmapPath, replaceTextPath string, apply bool) []string {
	arguments := []string{"--force", "--mailmap", mailmapPath}
	if replaceTextPath != "" {
		arguments = append(arguments, "--replace-text", replaceTextPath)
	}
	if !apply {
		arguments = append(arguments, "--dry-run")
	}
	return arguments
}

// buildPublishInstructions explains how to publish the rewrite. filter-repo
// removes the remote after a rewrite as a safety measure, so the instructions
// re-add it and force-push: commands the user runs themselves.
func buildPublishInstructions(remotes []gitclient.Remote) string {
	var builder strings.Builder
	builder.WriteString("\nHistory rewritten. Nothing has been pushed; that is yours to do.\n")
	builder.WriteString("Review the changes in your own git tooling first. When you are ready to publish:\n\n")
	if len(remotes) == 0 {
		builder.WriteString("  (No remote is configured, so there is nothing to publish.)\n")
		return builder.String()
	}
	for _, remote := range remotes {
		fmt.Fprintf(&builder, "  git remote add %s %s\n", remote.Name, remote.FetchURL)
		fmt.Fprintf(&builder, "  git push --force-with-lease --all %s\n", remote.Name)
		fmt.Fprintf(&builder, "  git push --force-with-lease --tags %s\n", remote.Name)
	}
	builder.WriteString("\n(filter-repo removes remotes after a rewrite, which is why the remote is re-added above.)\n")
	return builder.String()
}
