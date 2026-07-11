package reclaim

import (
	"bytes"
	"fmt"
	"os/exec"
)

// defaultFilterRepoRunner runs the real git filter-repo tool. It only ever
// invokes "git filter-repo" with reclaim's own arguments; it has no path to
// push.
type defaultFilterRepoRunner struct{}

func (defaultFilterRepoRunner) EnsureAvailable() error {
	command := exec.Command("git", "filter-repo", "--version")
	if runError := command.Run(); runError != nil {
		return fmt.Errorf("git filter-repo could not be run: %w", runError)
	}
	return nil
}

func (defaultFilterRepoRunner) Run(repositoryPath string, arguments []string) (string, error) {
	fullArguments := append([]string{"filter-repo"}, arguments...)
	command := exec.Command("git", fullArguments...)
	command.Dir = repositoryPath
	var combinedOutput bytes.Buffer
	command.Stdout = &combinedOutput
	command.Stderr = &combinedOutput
	runError := command.Run()
	return combinedOutput.String(), runError
}
