package main

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"alive.name/internal/backup"
	"alive.name/internal/domain"
	"alive.name/internal/gitclient"
	"alive.name/internal/mend"
	"alive.name/internal/reclaim"
	"alive.name/internal/report"
	"alive.name/internal/trace"
	"alive.name/internal/walkthrough"
)

// terminalPrompter implements walkthrough.Prompter against real input/output.
type terminalPrompter struct {
	reader *bufio.Reader
	output io.Writer
}

func newTerminalPrompter(input io.Reader, output io.Writer) *terminalPrompter {
	return &terminalPrompter{reader: bufio.NewReader(input), output: output}
}

func (prompter *terminalPrompter) Show(message string) {
	fmt.Fprintln(prompter.output, message)
}

func (prompter *terminalPrompter) AskText(question string) (string, error) {
	fmt.Fprintln(prompter.output, question)
	fmt.Fprint(prompter.output, "> ")
	line, readError := prompter.reader.ReadString('\n')
	if readError != nil && readError != io.EOF {
		return "", readError
	}
	return strings.TrimSpace(line), nil
}

func (prompter *terminalPrompter) Confirm(question string) (bool, error) {
	fmt.Fprintf(prompter.output, "%s [y/N] ", question)
	line, readError := prompter.reader.ReadString('\n')
	if readError != nil && readError != io.EOF {
		return false, readError
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

func confirmOnTerminal(command *cobra.Command, question string) (bool, error) {
	return newTerminalPrompter(command.InOrStdin(), command.OutOrStdout()).Confirm(question)
}

// Adapters wire the real packages into the walkthrough's interfaces. None of them
// exposes a push or commit, so the walkthrough structurally cannot do either.

type traceAdapter struct {
	client  *gitclient.GitClient
	options trace.Options
}

func (adapter traceAdapter) Trace(oldIdentity domain.OldIdentity) (trace.Report, error) {
	return trace.OldNameAcrossRepository(adapter.client, oldIdentity, adapter.options)
}

type rendererAdapter struct{}

func (rendererAdapter) Render(traceReport trace.Report) string {
	return report.RenderTraceReport(traceReport)
}

type menderAdapter struct{}

func (menderAdapter) Generate(oldIdentity domain.OldIdentity, newIdentity domain.NewIdentity) (string, error) {
	return mend.GenerateMailmap(oldIdentity, newIdentity)
}

func (menderAdapter) Write(repositoryPath, mailmapContent string) error {
	return mend.WriteMailmapFile(repositoryPath, mailmapContent)
}

type backuperAdapter struct {
	client  *gitclient.GitClient
	options backup.Options
}

func (adapter backuperAdapter) CreateVerified() (backup.Record, error) {
	return backup.CreateVerified(adapter.client, adapter.options)
}

type reclaimerAdapter struct {
	client *gitclient.GitClient
}

func (adapter reclaimerAdapter) History(backupRecord backup.Record, plan reclaim.Plan) error {
	return reclaim.History(adapter.client, backupRecord, plan)
}

func newGuideCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "guide",
		Short: "The guided, held-by-the-hand walkthrough (same as running `alive` with no command)",
		RunE: func(command *cobra.Command, _ []string) error {
			return runGuide(command)
		},
	}
}

func runGuide(command *cobra.Command) error {
	client, clientError := clientFromCommand(command)
	if clientError != nil {
		return clientError
	}
	repositoryPath := repositoryPathFromCommand(command)
	stateDirectoryPath := resolveStateDirectory("")

	guidedFlow := &walkthrough.Walkthrough{
		Prompter:       newTerminalPrompter(command.InOrStdin(), command.OutOrStdout()),
		Tracer:         traceAdapter{client: client, options: trace.Options{IncludeHistoricalFileContents: true}},
		Renderer:       rendererAdapter{},
		Mender:         menderAdapter{},
		Backuper:       backuperAdapter{client: client, options: backup.Options{StateDirectoryPath: stateDirectoryPath}},
		Reclaimer:      reclaimerAdapter{client: client},
		RepositoryPath: repositoryPath,
	}
	return guidedFlow.Run()
}
