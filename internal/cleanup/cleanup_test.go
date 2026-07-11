package cleanup

import (
	"strings"
	"testing"
)

func environmentLookup(pairs map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, present := pairs[name]
		return value, present
	}
}

func TestDetectShell(testingHandle *testing.T) {
	testCases := []struct {
		caseName        string
		operatingSystem string
		environment     map[string]string
		expectedShell   Shell
	}{
		{caseName: "windows defaults to PowerShell", operatingSystem: "windows", environment: map[string]string{}, expectedShell: ShellPowerShell},
		{caseName: "bash from SHELL", operatingSystem: "linux", environment: map[string]string{"SHELL": "/bin/bash"}, expectedShell: ShellBash},
		{caseName: "zsh from SHELL", operatingSystem: "darwin", environment: map[string]string{"SHELL": "/usr/bin/zsh"}, expectedShell: ShellZsh},
		{caseName: "fish from SHELL", operatingSystem: "linux", environment: map[string]string{"SHELL": "/usr/local/bin/fish"}, expectedShell: ShellFish},
		{caseName: "git-bash on windows detected as bash", operatingSystem: "windows", environment: map[string]string{"SHELL": "C:/Program Files/Git/usr/bin/bash.exe"}, expectedShell: ShellBash},
		{caseName: "unknown on unix without SHELL", operatingSystem: "linux", environment: map[string]string{}, expectedShell: ShellUnknown},
	}
	for _, testCase := range testCases {
		testingHandle.Run(testCase.caseName, func(subTest *testing.T) {
			environment := DetectEnvironment(testCase.operatingSystem, environmentLookup(testCase.environment))
			if environment.Shell != testCase.expectedShell {
				subTest.Errorf("expected shell %d, got %d", testCase.expectedShell, environment.Shell)
			}
		})
	}
}

func TestHistoryPathResolution(testingHandle *testing.T) {
	testingHandle.Run("APPDATA is used for PowerShell", func(subTest *testing.T) {
		environment := DetectEnvironment("windows", environmentLookup(map[string]string{"APPDATA": `C:\Users\me\AppData\Roaming`}))
		if !strings.Contains(environment.PowerShellHistoryPath, "AppData") || !strings.Contains(environment.PowerShellHistoryPath, "ConsoleHost_history.txt") {
			subTest.Errorf("unexpected PowerShell history path: %q", environment.PowerShellHistoryPath)
		}
	})
	testingHandle.Run("missing APPDATA falls back to a placeholder", func(subTest *testing.T) {
		environment := DetectEnvironment("windows", environmentLookup(map[string]string{}))
		if !strings.Contains(environment.PowerShellHistoryPath, "$env:APPDATA") {
			subTest.Errorf("expected an env-var placeholder, got %q", environment.PowerShellHistoryPath)
		}
	})
	testingHandle.Run("HISTFILE override is respected", func(subTest *testing.T) {
		environment := DetectEnvironment("linux", environmentLookup(map[string]string{"SHELL": "/bin/bash", "HISTFILE": "/custom/histfile"}))
		if environment.BashHistoryPath != "/custom/histfile" {
			subTest.Errorf("expected the HISTFILE override, got %q", environment.BashHistoryPath)
		}
	})
	testingHandle.Run("HOME builds the default bash path", func(subTest *testing.T) {
		environment := DetectEnvironment("linux", environmentLookup(map[string]string{"SHELL": "/bin/bash", "HOME": "/home/me"}))
		if !strings.Contains(environment.BashHistoryPath, ".bash_history") || !strings.Contains(environment.BashHistoryPath, "me") {
			subTest.Errorf("unexpected bash history path: %q", environment.BashHistoryPath)
		}
	})
	testingHandle.Run("XDG_DATA_HOME drives the fish path", func(subTest *testing.T) {
		environment := DetectEnvironment("linux", environmentLookup(map[string]string{"XDG_DATA_HOME": "/xdg"}))
		if !strings.Contains(environment.FishHistoryPath, "xdg") || !strings.Contains(environment.FishHistoryPath, "fish_history") {
			subTest.Errorf("unexpected fish history path: %q", environment.FishHistoryPath)
		}
	})
}

func TestRenderHappyPowerShell(testingHandle *testing.T) {
	environment := DetectEnvironment("windows", environmentLookup(map[string]string{"APPDATA": `C:\Users\me\AppData\Roaming`}))
	rendered := Render(environment)
	for _, expected := range []string{
		"PSReadLine",
		"ConsoleHost_history.txt",
		"-notlike",
		"Clear-History",
		"alive backup rm",
		"git reflog expire",
		".mailmap",
		"swap/page file",
		"no command", // the gentler-next-time note
		placeholder,
		"Clear Buffer", // windows scrollback
	} {
		if !strings.Contains(rendered, expected) {
			testingHandle.Errorf("expected rendered advice to contain %q", expected)
		}
	}
}

func TestRenderUnknownShellShowsEverything(testingHandle *testing.T) {
	environment := DetectEnvironment("linux", environmentLookup(map[string]string{}))
	rendered := Render(environment)
	for _, expected := range []string{"PowerShell", "bash", "zsh", "fish", "cmd.exe", "--shell"} {
		if !strings.Contains(rendered, expected) {
			testingHandle.Errorf("unknown shell should show guidance for %q", expected)
		}
	}
	// A non-windows environment should give the unix scrollback advice.
	if !strings.Contains(rendered, "printf") {
		testingHandle.Error("expected unix scrollback guidance")
	}
}

func TestRenderShellSpecific(testingHandle *testing.T) {
	testCases := []struct {
		caseName    string
		environment map[string]string
		expected    string
	}{
		{caseName: "bash uses sed", environment: map[string]string{"SHELL": "/bin/bash", "HOME": "/home/me"}, expected: "sed -i"},
		{caseName: "fish uses history delete", environment: map[string]string{"SHELL": "/usr/bin/fish", "HOME": "/home/me"}, expected: "history delete --contains"},
	}
	for _, testCase := range testCases {
		testingHandle.Run(testCase.caseName, func(subTest *testing.T) {
			rendered := Render(DetectEnvironment("linux", environmentLookup(testCase.environment)))
			if !strings.Contains(rendered, testCase.expected) {
				subTest.Errorf("expected %q in the advice", testCase.expected)
			}
		})
	}
}

// TestRenderZeroValueDoesNotPanic covers the degenerate case of an empty
// environment: it should still render complete advice without panicking.
func TestRenderZeroValueDoesNotPanic(testingHandle *testing.T) {
	rendered := Render(Environment{})
	if !strings.Contains(rendered, "Shell history") || !strings.Contains(rendered, placeholder) {
		testingHandle.Errorf("zero-value environment should still render advice, got:\n%s", rendered)
	}
}
