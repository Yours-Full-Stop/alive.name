// Package cleanup produces advice for purging an old name from the local caches
// that using a shell leaves behind: shell history, terminal scrollback, and a
// few git and OS locations. It only ever generates guidance text; it never edits
// history files itself, because the live shell writes those and a tool editing
// them mid-session can lose or corrupt entries. The shell's own commands do it
// safely, so this hands those to the user.
//
// It deliberately never asks for the old name. It prints commands with a clear
// placeholder for the user to fill in, so the cleanup step itself never handles
// the deadname and cannot leak it.
package cleanup

import (
	"fmt"
	"path/filepath"
	"strings"
)

// placeholder is what the user replaces with their old name or email in the
// printed commands.
const placeholder = "OLD NAME OR EMAIL"

// Shell identifies the user's shell for tailoring history advice.
type Shell int

const (
	ShellUnknown Shell = iota
	ShellPowerShell
	ShellBash
	ShellZsh
	ShellFish
	ShellCmd
)

// Environment is the resolved context the advice is generated from. It is built
// purely from the operating system name and an environment lookup, so it can be
// produced deterministically in tests.
type Environment struct {
	OperatingSystem       string
	Shell                 Shell
	PowerShellHistoryPath string
	BashHistoryPath       string
	ZshHistoryPath        string
	FishHistoryPath       string
}

// DetectEnvironment resolves the environment from the OS name and a lookup
// function (os.LookupEnv in production). It performs no I/O.
func DetectEnvironment(operatingSystem string, lookupEnvironment func(string) (string, bool)) Environment {
	homeDirectory := firstNonEmptyEnvironment(lookupEnvironment, "HOME", "USERPROFILE")

	environment := Environment{
		OperatingSystem:       operatingSystem,
		Shell:                 detectShell(operatingSystem, lookupEnvironment),
		PowerShellHistoryPath: resolvePowerShellHistoryPath(lookupEnvironment),
		BashHistoryPath:       resolveHistoryFilePath(lookupEnvironment, homeDirectory, ".bash_history"),
		ZshHistoryPath:        resolveHistoryFilePath(lookupEnvironment, homeDirectory, ".zsh_history"),
		FishHistoryPath:       resolveFishHistoryPath(lookupEnvironment, homeDirectory),
	}
	return environment
}

func detectShell(operatingSystem string, lookupEnvironment func(string) (string, bool)) Shell {
	if shellValue, present := lookupEnvironment("SHELL"); present && shellValue != "" {
		shellName := strings.ToLower(filepath.Base(shellValue))
		switch {
		case strings.Contains(shellName, "bash"):
			return ShellBash
		case strings.Contains(shellName, "zsh"):
			return ShellZsh
		case strings.Contains(shellName, "fish"):
			return ShellFish
		}
	}
	if operatingSystem == "windows" {
		// On Windows, PowerShell is the overwhelming default for this tool.
		return ShellPowerShell
	}
	return ShellUnknown
}

func resolvePowerShellHistoryPath(lookupEnvironment func(string) (string, bool)) string {
	if appData, present := lookupEnvironment("APPDATA"); present && appData != "" {
		return filepath.Join(appData, "Microsoft", "Windows", "PowerShell", "PSReadLine", "ConsoleHost_history.txt")
	}
	return `$env:APPDATA\Microsoft\Windows\PowerShell\PSReadLine\ConsoleHost_history.txt`
}

func resolveHistoryFilePath(lookupEnvironment func(string) (string, bool), homeDirectory, defaultFileName string) string {
	if histFile, present := lookupEnvironment("HISTFILE"); present && histFile != "" {
		return histFile
	}
	if homeDirectory == "" {
		return "$HOME/" + defaultFileName
	}
	return filepath.Join(homeDirectory, defaultFileName)
}

func resolveFishHistoryPath(lookupEnvironment func(string) (string, bool), homeDirectory string) string {
	if xdgDataHome, present := lookupEnvironment("XDG_DATA_HOME"); present && xdgDataHome != "" {
		return filepath.Join(xdgDataHome, "fish", "fish_history")
	}
	if homeDirectory == "" {
		return "$HOME/.local/share/fish/fish_history"
	}
	return filepath.Join(homeDirectory, ".local", "share", "fish", "fish_history")
}

func firstNonEmptyEnvironment(lookupEnvironment func(string) (string, bool), names ...string) string {
	for _, name := range names {
		if value, present := lookupEnvironment(name); present && value != "" {
			return value
		}
	}
	return ""
}

// Render produces the full advisory text for an environment.
func Render(environment Environment) string {
	var builder strings.Builder

	builder.WriteString("Clearing the old name from your machine\n")
	builder.WriteString("=======================================\n\n")
	builder.WriteString("Reclaiming a name in a repository is only part of it. If you typed the name\n")
	builder.WriteString("into a command (like `alive trace --old-name ...`), it also landed in a few\n")
	builder.WriteString("local caches. Here's how to clear each. Replace " + quote(placeholder) + " with the\n")
	builder.WriteString("value you want gone. Nothing below is run for you; you stay in control.\n\n")

	writeShellHistorySection(&builder, environment)
	writeScrollbackSection(&builder, environment)
	writeBackupsSection(&builder)
	writeReclaimLeftoversSection(&builder)
	writeMailmapSection(&builder)
	writeHonestGapsSection(&builder)
	writeClosingSection(&builder)

	return builder.String()
}

func writeShellHistorySection(builder *strings.Builder, environment Environment) {
	builder.WriteString("1. Shell history (the main one)\n")
	builder.WriteString("-------------------------------\n")

	switch environment.Shell {
	case ShellPowerShell:
		writePowerShellHistory(builder, environment.PowerShellHistoryPath)
	case ShellBash:
		writeBashHistory(builder, environment.BashHistoryPath)
	case ShellZsh:
		writeZshHistory(builder, environment.ZshHistoryPath)
	case ShellFish:
		writeFishHistory(builder, environment.FishHistoryPath)
	case ShellCmd:
		writeCmdHistory(builder)
	default:
		builder.WriteString("I couldn't tell which shell you use, so here's guidance for each. Re-run\n")
		builder.WriteString("with `--shell powershell|bash|zsh|fish` to narrow it down.\n\n")
		writePowerShellHistory(builder, environment.PowerShellHistoryPath)
		writeBashHistory(builder, environment.BashHistoryPath)
		writeZshHistory(builder, environment.ZshHistoryPath)
		writeFishHistory(builder, environment.FishHistoryPath)
		writeCmdHistory(builder)
	}
	builder.WriteString("\n")
}

func writePowerShellHistory(builder *strings.Builder, historyPath string) {
	builder.WriteString("PowerShell (PSReadLine)\n")
	fmt.Fprintf(builder, "  History file: %s\n", historyPath)
	builder.WriteString("  Remove only the lines containing your old name (wildcard match, no regex):\n")
	fmt.Fprintf(builder, "    $historyPath = %s\n", quote(historyPath))
	fmt.Fprintf(builder, "    (Get-Content $historyPath) | Where-Object { $_ -notlike %s } | Set-Content $historyPath\n", quote("*"+placeholder+"*"))
	builder.WriteString("  Clear the current session's in-memory history too:\n")
	builder.WriteString("    Clear-History; [Microsoft.PowerShell.PSConsoleReadLine]::ClearHistory()\n")
	builder.WriteString("  (PSReadLine writes history as you go, so closing this window afterward is wise.)\n\n")
}

func writeBashHistory(builder *strings.Builder, historyPath string) {
	builder.WriteString("bash\n")
	fmt.Fprintf(builder, "  History file: %s\n", historyPath)
	builder.WriteString("  Remove matching lines from the file, then refresh this session:\n")
	fmt.Fprintf(builder, "    sed -i %s %s\n", quote("/"+placeholder+"/d"), quote(historyPath))
	builder.WriteString("    history -c && history -r\n")
	builder.WriteString("  (On macOS use `sed -i '' ...`. If the name has /, escape it or use a simpler pattern.)\n\n")
}

func writeZshHistory(builder *strings.Builder, historyPath string) {
	builder.WriteString("zsh\n")
	fmt.Fprintf(builder, "  History file: %s\n", historyPath)
	builder.WriteString("  Remove matching lines, then reload history into this session:\n")
	fmt.Fprintf(builder, "    sed -i %s %s\n", quote("/"+placeholder+"/d"), quote(historyPath))
	builder.WriteString("    fc -R\n")
	builder.WriteString("  (On macOS use `sed -i '' ...`.)\n\n")
}

func writeFishHistory(builder *strings.Builder, historyPath string) {
	builder.WriteString("fish\n")
	fmt.Fprintf(builder, "  History file: %s\n", historyPath)
	builder.WriteString("  fish has a safe built-in for exactly this:\n")
	fmt.Fprintf(builder, "    history delete --contains %s\n", quote(placeholder))
	builder.WriteString("  (Or `history clear` to wipe all history.)\n\n")
}

func writeCmdHistory(builder *strings.Builder) {
	builder.WriteString("cmd.exe\n")
	builder.WriteString("  cmd keeps history only in memory (doskey), with no history file by default.\n")
	builder.WriteString("  Closing the window clears it. `doskey /history` shows the current buffer.\n\n")
}

func writeScrollbackSection(builder *strings.Builder, environment Environment) {
	builder.WriteString("2. Terminal scrollback\n")
	builder.WriteString("----------------------\n")
	builder.WriteString("  The name was also printed on screen, so it may sit in the scrollback buffer.\n")
	if environment.OperatingSystem == "windows" {
		builder.WriteString("    Clear-Host clears the view; in Windows Terminal use \"Clear Buffer\" (right-click)\n")
		builder.WriteString("    to also drop scrollback, or close the tab.\n\n")
		return
	}
	builder.WriteString("    clear            # clears the screen\n")
	builder.WriteString("    printf '\\033[3J' # also clears the scrollback buffer in many terminals\n\n")
}

func writeBackupsSection(builder *strings.Builder) {
	builder.WriteString("3. alive.name backups\n")
	builder.WriteString("---------------------\n")
	builder.WriteString("  A backup is a full copy of the repository, so it deliberately still contains\n")
	builder.WriteString("  the old name. When you no longer need it:\n")
	builder.WriteString("    alive backup list\n")
	builder.WriteString("    alive backup rm <identifier>      # remove one\n")
	builder.WriteString("    alive backup gc --older-than 0s   # remove all\n\n")
}

func writeReclaimLeftoversSection(builder *strings.Builder) {
	builder.WriteString("4. Leftovers from a history rewrite (only if you ran `alive reclaim`)\n")
	builder.WriteString("--------------------------------------------------------------------\n")
	builder.WriteString("  The pre-rewrite commits can linger in the repo's reflog and object store until\n")
	builder.WriteString("  they are expired. Inside the rewritten repository:\n")
	builder.WriteString("    git reflog expire --expire=now --all\n")
	builder.WriteString("    git gc --prune=now\n")
	builder.WriteString("  filter-repo also leaves a .git/filter-repo/ directory of analysis you can delete.\n\n")
}

func writeMailmapSection(builder *strings.Builder) {
	builder.WriteString("5. .mailmap\n")
	builder.WriteString("-----------\n")
	builder.WriteString("  If you ran `alive mend`, the .mailmap file intentionally maps the old name to\n")
	builder.WriteString("  the new one, so it contains both. That is by design; it is what makes git show\n")
	builder.WriteString("  your new name. If you would rather it weren't recorded there, remove those lines\n")
	builder.WriteString("  (the display remap goes away with them).\n\n")
}

func writeHonestGapsSection(builder *strings.Builder) {
	builder.WriteString("6. What this can't reach\n")
	builder.WriteString("------------------------\n")
	builder.WriteString("  Being honest about the limits: the following aren't practical to scrub here,\n")
	builder.WriteString("  and no tool can promise otherwise:\n")
	builder.WriteString("    - the OS swap/page file or hibernation file,\n")
	builder.WriteString("    - terminal-emulator session logs, if you enabled logging,\n")
	builder.WriteString("    - editor swap/undo files, if you opened files in an editor,\n")
	builder.WriteString("    - the clipboard, if you copied the name (copy something else to clear it),\n")
	builder.WriteString("    - OS search indexes that may have indexed file contents.\n\n")
}

func writeClosingSection(builder *strings.Builder) {
	builder.WriteString("A gentler way next time\n")
	builder.WriteString("-----------------------\n")
	builder.WriteString("  Running `alive` with no command asks for the name at a prompt instead of on the\n")
	builder.WriteString("  command line, so it never enters your shell history in the first place.\n")
	builder.WriteString("  Do as much or as little of the above as gives you peace of mind.\n")
}

func quote(value string) string {
	return "'" + value + "'"
}
