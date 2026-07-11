package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// Unit tests: the command tree is built without touching git or the filesystem,
// so its shape and safe defaults can be asserted purely.

func findSubcommand(parent *cobra.Command, name string) *cobra.Command {
	for _, child := range parent.Commands() {
		if child.Name() == name {
			return child
		}
	}
	return nil
}

func TestCommandTreeShape(testingHandle *testing.T) {
	rootCommand := newRootCommand()
	if rootCommand.Name() != "alive" {
		testingHandle.Fatalf("root command should be named alive, got %q", rootCommand.Name())
	}

	for _, expectedVerb := range []string{"guide", "trace", "mend", "reclaim", "backup", "cleanup"} {
		if findSubcommand(rootCommand, expectedVerb) == nil {
			testingHandle.Errorf("expected a %q command", expectedVerb)
		}
	}

	backupCommand := findSubcommand(rootCommand, "backup")
	if backupCommand == nil {
		testingHandle.Fatal("expected a backup command")
	}
	for _, expectedSub := range []string{"create", "list", "restore", "rm", "gc"} {
		if findSubcommand(backupCommand, expectedSub) == nil {
			testingHandle.Errorf("expected backup %q subcommand", expectedSub)
		}
	}

	if rootCommand.PersistentFlags().Lookup("repo") == nil {
		testingHandle.Error("expected a persistent --repo flag")
	}
}

func TestNoPushCommandExists(testingHandle *testing.T) {
	rootCommand := newRootCommand()
	var assertNoPush func(command *cobra.Command)
	assertNoPush = func(command *cobra.Command) {
		if command.Name() == "push" {
			testingHandle.Errorf("there must never be a push command, found one under %q", command.Parent().Name())
		}
		for _, child := range command.Commands() {
			assertNoPush(child)
		}
	}
	assertNoPush(rootCommand)
}

func runRootForTest(testingHandle *testing.T, arguments ...string) (string, error) {
	testingHandle.Helper()
	rootCommand := newRootCommand()
	var output bytes.Buffer
	rootCommand.SetOut(&output)
	rootCommand.SetErr(&output)
	rootCommand.SetArgs(arguments)
	executeError := rootCommand.Execute()
	return output.String(), executeError
}

func TestCleanupCommandRendersAdvice(testingHandle *testing.T) {
	// cleanup touches no git and no filesystem; it only reads env vars and
	// prints text, so it is a unit test.
	output, executeError := runRootForTest(testingHandle, "cleanup")
	if executeError != nil {
		testingHandle.Fatalf("cleanup: %v", executeError)
	}
	for _, expected := range []string{"Shell history", "alive backup", "OLD NAME OR EMAIL"} {
		if !strings.Contains(output, expected) {
			testingHandle.Errorf("expected cleanup output to contain %q", expected)
		}
	}
}

func TestCleanupCommandShellOverride(testingHandle *testing.T) {
	testingHandle.Run("valid shell narrows the advice", func(subTest *testing.T) {
		output, executeError := runRootForTest(subTest, "cleanup", "--shell", "fish")
		if executeError != nil {
			subTest.Fatalf("cleanup --shell fish: %v", executeError)
		}
		if !strings.Contains(output, "history delete --contains") {
			subTest.Errorf("expected fish-specific advice, got:\n%s", output)
		}
	})
	testingHandle.Run("unknown shell is an error", func(subTest *testing.T) {
		if _, executeError := runRootForTest(subTest, "cleanup", "--shell", "nonsense"); executeError == nil {
			subTest.Error("expected an error for an unknown shell")
		}
	})
}

func TestReclaimIsDryRunByDefault(testingHandle *testing.T) {
	reclaimCommand := findSubcommand(newRootCommand(), "reclaim")
	if reclaimCommand == nil {
		testingHandle.Fatal("expected a reclaim command")
	}
	applyFlag := reclaimCommand.Flags().Lookup("apply")
	if applyFlag == nil {
		testingHandle.Fatal("expected an --apply flag")
	}
	if applyFlag.DefValue != "false" {
		testingHandle.Errorf("--apply must default to false (dry run), got %q", applyFlag.DefValue)
	}
}
