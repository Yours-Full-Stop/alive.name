// Command alive is the alive.name CLI: it finds an old name across a git history
// and helps make it yours again, safely, with a verified backup before anything
// destructive. This file is wiring only; the behaviour lives in internal/.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"alive.name/internal/backup"
	"alive.name/internal/cleanup"
	"alive.name/internal/domain"
	"alive.name/internal/gitclient"
	"alive.name/internal/identity"
	"alive.name/internal/mend"
	"alive.name/internal/reclaim"
	"alive.name/internal/report"
	"alive.name/internal/trace"
)

func main() {
	if executeError := newRootCommand().Execute(); executeError != nil {
		os.Exit(1)
	}
}

func newRootCommand() *cobra.Command {
	rootCommand := &cobra.Command{
		Use:   "alive",
		Short: "Find an old name in your git history and make it yours again",
		Long: "alive.name finds every place an old name still lives in a git repository and helps\n" +
			"you reclaim it, safely, on your own machine, with a verified backup before\n" +
			"anything is ever changed. It never pushes and never commits for you.\n\n" +
			"Run `alive` with no command for the guided, held-by-the-hand walkthrough.",
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(command *cobra.Command, _ []string) error {
			return runGuide(command)
		},
	}
	rootCommand.PersistentFlags().String("repo", ".", "path to the git repository to work on")
	rootCommand.AddCommand(
		newGuideCommand(),
		newTraceCommand(),
		newMendCommand(),
		newReclaimCommand(),
		newBackupCommand(),
		newCleanupCommand(),
	)
	return rootCommand
}

func newCleanupCommand() *cobra.Command {
	var shellOverride string
	command := &cobra.Command{
		Use:   "cleanup",
		Short: "Show how to clear the old name from shell history and other local caches",
		RunE: func(command *cobra.Command, _ []string) error {
			environment := cleanup.DetectEnvironment(runtime.GOOS, os.LookupEnv)
			if strings.TrimSpace(shellOverride) != "" {
				parsedShell, recognised := parseShellOverride(shellOverride)
				if !recognised {
					return fmt.Errorf("unknown shell %q; use one of powershell, bash, zsh, fish, cmd", shellOverride)
				}
				environment.Shell = parsedShell
			}
			fmt.Fprint(command.OutOrStdout(), cleanup.Render(environment))
			return nil
		},
	}
	command.Flags().StringVar(&shellOverride, "shell", "", "force guidance for a specific shell: powershell|bash|zsh|fish|cmd")
	return command
}

func parseShellOverride(value string) (cleanup.Shell, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "powershell", "pwsh", "posh":
		return cleanup.ShellPowerShell, true
	case "bash":
		return cleanup.ShellBash, true
	case "zsh":
		return cleanup.ShellZsh, true
	case "fish":
		return cleanup.ShellFish, true
	case "cmd", "cmd.exe":
		return cleanup.ShellCmd, true
	default:
		return cleanup.ShellUnknown, false
	}
}

// printShellHistoryTip reminds the user that a name passed on the command line is
// now in their shell history, and points at the cleanup command.
func printShellHistoryTip(command *cobra.Command) {
	fmt.Fprintln(command.ErrOrStderr(), "\nTip: the name you passed is now in your shell history. Run 'alive cleanup' to clear it, or use the interactive 'alive' next time so it never lands there.")
}

// identityFlags binds the old/new identity command-line flags.
type identityFlags struct {
	oldNames  []string
	oldEmails []string
	newName   string
	newEmail  string
}

func registerOldIdentityFlags(command *cobra.Command, flags *identityFlags) {
	command.Flags().StringArrayVar(&flags.oldNames, "old-name", nil, "an old name to search for (may be repeated)")
	command.Flags().StringArrayVar(&flags.oldEmails, "old-email", nil, "an old email to search for (may be repeated)")
}

func registerNewIdentityFlags(command *cobra.Command, flags *identityFlags) {
	command.Flags().StringVar(&flags.newName, "new-name", "", "the name that is yours now")
	command.Flags().StringVar(&flags.newEmail, "new-email", "", "the email that is yours now")
}

func (flags *identityFlags) resolveOld(output io.Writer) (domain.OldIdentity, error) {
	oldIdentity, warnings, parseError := identity.ParseOld(flags.oldNames, flags.oldEmails)
	if parseError != nil {
		return domain.OldIdentity{}, parseError
	}
	for _, warning := range warnings {
		fmt.Fprintln(output, "note:", warning.Message)
	}
	return oldIdentity, nil
}

func (flags *identityFlags) resolveNew() (domain.NewIdentity, error) {
	return identity.ParseNew(flags.newName, flags.newEmail)
}

func clientFromCommand(command *cobra.Command) (*gitclient.GitClient, error) {
	repositoryPath, _ := command.Flags().GetString("repo")
	client, constructionError := gitclient.NewGitClient(repositoryPath)
	if constructionError != nil {
		return nil, constructionError
	}
	if availabilityError := client.EnsureGitAvailable(); availabilityError != nil {
		return nil, availabilityError
	}
	return client, nil
}

func repositoryPathFromCommand(command *cobra.Command) string {
	repositoryPath, _ := command.Flags().GetString("repo")
	return repositoryPath
}

// resolveStateDirectory picks where backups live, in order of precedence: the
// --state-dir flag, then the ALIVE_STATE_DIR environment variable (used by the
// Docker image to point at the mounted /backups directory), then a per-user
// location outside any repository.
func resolveStateDirectory(override string) string {
	if strings.TrimSpace(override) != "" {
		return override
	}
	if environmentDirectory := strings.TrimSpace(os.Getenv("ALIVE_STATE_DIR")); environmentDirectory != "" {
		return environmentDirectory
	}
	homeDirectory, homeError := os.UserHomeDir()
	if homeError != nil {
		return filepath.Join(os.TempDir(), "alive-backups")
	}
	return filepath.Join(homeDirectory, ".alive", "backups")
}

func newTraceCommand() *cobra.Command {
	var flags identityFlags
	var caseSensitive, fetch, deep, workingTree bool
	command := &cobra.Command{
		Use:   "trace",
		Short: "Find every occurrence of an old name (read-only, safe)",
		RunE: func(command *cobra.Command, _ []string) error {
			client, clientError := clientFromCommand(command)
			if clientError != nil {
				return clientError
			}
			oldIdentity, identityError := flags.resolveOld(command.OutOrStdout())
			if identityError != nil {
				return identityError
			}
			traceReport, traceError := trace.OldNameAcrossRepository(client, oldIdentity, trace.Options{
				CaseSensitive:                  caseSensitive,
				RefreshRemoteTrackingRefs:      fetch,
				IncludeHistoricalFileContents:  deep,
				IncludeWorkingTreeFileContents: workingTree,
			})
			if traceError != nil {
				return traceError
			}
			fmt.Fprint(command.OutOrStdout(), report.RenderTraceReport(traceReport))
			printShellHistoryTip(command)
			return nil
		},
	}
	registerOldIdentityFlags(command, &flags)
	command.Flags().BoolVar(&caseSensitive, "case-sensitive", false, "match exact case (default is case-insensitive)")
	command.Flags().BoolVar(&fetch, "fetch", false, "refresh remote-tracking refs before classifying (a read, never a push)")
	command.Flags().BoolVar(&deep, "deep", false, "also scan historical file contents (slower)")
	command.Flags().BoolVar(&workingTree, "working-tree", false, "also scan the current working-tree files")
	return command
}

func newMendCommand() *cobra.Command {
	var flags identityFlags
	command := &cobra.Command{
		Use:   "mend",
		Short: "Write a .mailmap so git shows your new name (non-destructive)",
		RunE: func(command *cobra.Command, _ []string) error {
			oldIdentity, identityError := flags.resolveOld(command.OutOrStdout())
			if identityError != nil {
				return identityError
			}
			newIdentity, newError := flags.resolveNew()
			if newError != nil {
				return newError
			}
			mailmapContent, generateError := mend.GenerateMailmap(oldIdentity, newIdentity)
			if generateError != nil {
				return generateError
			}
			if writeError := mend.WriteMailmapFile(repositoryPathFromCommand(command), mailmapContent); writeError != nil {
				return writeError
			}
			fmt.Fprintln(command.OutOrStdout(), "Wrote .mailmap. Your history is untouched; this only changes how names display.")
			fmt.Fprintln(command.OutOrStdout(), "Review and commit it yourself when you're ready. This tool does not commit for you.")
			printShellHistoryTip(command)
			return nil
		},
	}
	registerOldIdentityFlags(command, &flags)
	registerNewIdentityFlags(command, &flags)
	return command
}

func newReclaimCommand() *cobra.Command {
	var flags identityFlags
	var apply, acknowledgeSigned, acknowledgePushed, assumeYes, bundleOnly bool
	var stateDirectory string
	command := &cobra.Command{
		Use:   "reclaim",
		Short: "Rewrite history to remove an old name (destructive; gated; dry-run by default)",
		RunE: func(command *cobra.Command, _ []string) error {
			client, clientError := clientFromCommand(command)
			if clientError != nil {
				return clientError
			}
			oldIdentity, identityError := flags.resolveOld(command.OutOrStdout())
			if identityError != nil {
				return identityError
			}
			newIdentity, newError := flags.resolveNew()
			if newError != nil {
				return newError
			}

			fmt.Fprintln(command.OutOrStdout(), "Making a full, verified backup before anything is touched...")
			backupRecord, backupError := backup.CreateVerified(client, backup.Options{
				StateDirectoryPath: resolveStateDirectory(stateDirectory),
				BundleOnly:         bundleOnly,
			})
			if backupError != nil {
				return backupError
			}
			fmt.Fprintln(command.OutOrStdout(), "Backup verified and saved at:", backupRecord.BackupDirectoryPath)

			if apply && !assumeYes {
				confirmed, confirmError := confirmOnTerminal(command, "Apply the rewrite for real? It cannot be undone except from the backup.")
				if confirmError != nil {
					return confirmError
				}
				if !confirmed {
					fmt.Fprintln(command.OutOrStdout(), "Stopped. Nothing was rewritten.")
					return nil
				}
			}

			historyError := reclaim.History(client, backupRecord, reclaim.Plan{
				OldIdentity:              oldIdentity,
				NewIdentity:              newIdentity,
				Apply:                    apply,
				AcknowledgeSignedCommits: acknowledgeSigned,
				AcknowledgePushedHistory: acknowledgePushed,
				Output:                   command.OutOrStdout(),
			})
			printShellHistoryTip(command)
			return historyError
		},
	}
	registerOldIdentityFlags(command, &flags)
	registerNewIdentityFlags(command, &flags)
	command.Flags().BoolVar(&apply, "apply", false, "actually rewrite history (default is a dry run that changes nothing)")
	command.Flags().BoolVar(&acknowledgeSigned, "acknowledge-signed", false, "acknowledge that signatures will break")
	command.Flags().BoolVar(&acknowledgePushed, "acknowledge-pushed", false, "acknowledge that already-published history is being rewritten")
	command.Flags().BoolVar(&assumeYes, "yes", false, "skip the interactive confirmation on --apply")
	command.Flags().BoolVar(&bundleOnly, "bundle-only", false, "back up only an all-refs bundle, not a full copy")
	command.Flags().StringVar(&stateDirectory, "state-dir", "", "where to store the backup (defaults to a per-user location)")
	return command
}

func newBackupCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "backup",
		Short: "Create, list, restore, and remove backups",
	}
	command.AddCommand(
		newBackupCreateCommand(),
		newBackupListCommand(),
		newBackupRestoreCommand(),
		newBackupRemoveCommand(),
		newBackupGarbageCollectCommand(),
	)
	return command
}

func newBackupCreateCommand() *cobra.Command {
	var bundleOnly, includeMirror bool
	var stateDirectory string
	command := &cobra.Command{
		Use:   "create",
		Short: "Create and verify a backup of the repository",
		RunE: func(command *cobra.Command, _ []string) error {
			client, clientError := clientFromCommand(command)
			if clientError != nil {
				return clientError
			}
			record, createError := backup.CreateVerified(client, backup.Options{
				StateDirectoryPath:  resolveStateDirectory(stateDirectory),
				BundleOnly:          bundleOnly,
				IncludeRemoteMirror: includeMirror,
			})
			if createError != nil {
				return createError
			}
			fmt.Fprintln(command.OutOrStdout(), "Backup created and verified:", record.Identifier)
			fmt.Fprintln(command.OutOrStdout(), "  location:", record.BackupDirectoryPath)
			fmt.Fprintln(command.OutOrStdout(), "  bundle:  ", record.BundleFilePath)
			return nil
		},
	}
	command.Flags().BoolVar(&bundleOnly, "bundle-only", false, "keep only an all-refs bundle, not a full copy")
	command.Flags().BoolVar(&includeMirror, "mirror", false, "also mirror the first remote")
	command.Flags().StringVar(&stateDirectory, "state-dir", "", "where to store the backup")
	return command
}

func newBackupListCommand() *cobra.Command {
	var stateDirectory string
	command := &cobra.Command{
		Use:   "list",
		Short: "List existing backups",
		RunE: func(command *cobra.Command, _ []string) error {
			records, listError := backup.List(resolveStateDirectory(stateDirectory))
			if listError != nil {
				return listError
			}
			if len(records) == 0 {
				fmt.Fprintln(command.OutOrStdout(), "No backups found.")
				return nil
			}
			for _, record := range records {
				fmt.Fprintf(command.OutOrStdout(), "%s  %s  %s\n", record.Identifier, record.CreatedAt.Format(time.RFC3339), record.VerificationStatus)
			}
			return nil
		},
	}
	command.Flags().StringVar(&stateDirectory, "state-dir", "", "where backups are stored")
	return command
}

func newBackupRestoreCommand() *cobra.Command {
	var stateDirectory, destination string
	var remote bool
	command := &cobra.Command{
		Use:   "restore <identifier>",
		Short: "Restore a backup locally, or print the command to restore a remote",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			record, findError := findBackupRecord(resolveStateDirectory(stateDirectory), args[0])
			if findError != nil {
				return findError
			}
			if remote {
				restoreCommand, commandError := backup.PrepareRemoteRestoreCommand(record)
				if commandError != nil {
					return commandError
				}
				fmt.Fprintln(command.OutOrStdout(), "To restore the remote, run this yourself (this tool never pushes):")
				fmt.Fprintln(command.OutOrStdout(), "  "+restoreCommand)
				return nil
			}
			if strings.TrimSpace(destination) == "" {
				return fmt.Errorf("a --destination is required for a local restore")
			}
			if restoreError := backup.PrepareLocalRestore(record, destination); restoreError != nil {
				return restoreError
			}
			fmt.Fprintln(command.OutOrStdout(), "Restored to", destination)
			return nil
		},
	}
	command.Flags().StringVar(&stateDirectory, "state-dir", "", "where backups are stored")
	command.Flags().StringVar(&destination, "destination", "", "where to restore a local copy")
	command.Flags().BoolVar(&remote, "remote", false, "print the command to restore a mirrored remote instead of restoring locally")
	return command
}

func newBackupRemoveCommand() *cobra.Command {
	var stateDirectory string
	command := &cobra.Command{
		Use:   "rm <identifier>",
		Short: "Remove a backup",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			record, findError := findBackupRecord(resolveStateDirectory(stateDirectory), args[0])
			if findError != nil {
				return findError
			}
			if removeError := backup.Remove(record); removeError != nil {
				return removeError
			}
			fmt.Fprintln(command.OutOrStdout(), "Removed", record.Identifier)
			return nil
		},
	}
	command.Flags().StringVar(&stateDirectory, "state-dir", "", "where backups are stored")
	return command
}

func newBackupGarbageCollectCommand() *cobra.Command {
	var stateDirectory string
	var olderThan time.Duration
	command := &cobra.Command{
		Use:   "gc",
		Short: "Remove backups older than a given age",
		RunE: func(command *cobra.Command, _ []string) error {
			removed, gcError := backup.GarbageCollect(resolveStateDirectory(stateDirectory), olderThan)
			if gcError != nil {
				return gcError
			}
			fmt.Fprintf(command.OutOrStdout(), "Removed %d backup(s).\n", len(removed))
			return nil
		},
	}
	command.Flags().StringVar(&stateDirectory, "state-dir", "", "where backups are stored")
	command.Flags().DurationVar(&olderThan, "older-than", 30*24*time.Hour, "remove backups older than this")
	return command
}

func findBackupRecord(stateDirectoryPath, identifier string) (backup.Record, error) {
	records, listError := backup.List(stateDirectoryPath)
	if listError != nil {
		return backup.Record{}, listError
	}
	for _, record := range records {
		if record.Identifier == identifier {
			return record, nil
		}
	}
	return backup.Record{}, fmt.Errorf("no backup found with identifier %q", identifier)
}
