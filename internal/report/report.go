// Package report renders a trace result as a calm, plain-language, three-zone
// report. The tone is deliberately non-clinical and never blaming: someone may
// be reading this at a hard moment.
package report

import (
	"fmt"
	"sort"
	"strings"

	"alive.name/internal/domain"
	"alive.name/internal/trace"
)

// githubSensitiveDataURL points at GitHub's guidance for removing sensitive data
// that may persist in forks, clones, and caches.
const githubSensitiveDataURL = "https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/removing-sensitive-data-from-a-repository"

// RenderTraceReport turns a trace report into readable text for the terminal.
func RenderTraceReport(traceReport trace.Report) string {
	var builder strings.Builder

	totalOccurrences := len(traceReport.Occurrences)
	if totalOccurrences == 0 {
		writeNothingFound(&builder, traceReport)
		return builder.String()
	}

	fmt.Fprintf(&builder, "Found %s of your old name.\n\n", pluralise(totalOccurrences, "occurrence", "occurrences"))

	writeZoneSection(
		&builder,
		traceReport,
		domain.ZoneLocalRewritable,
		"Only here, on your machine",
		"These live only in your local history. You can rewrite them freely; nothing outside this machine is affected.",
	)
	writeZoneSection(
		&builder,
		traceReport,
		domain.ZoneControlledRemote,
		"On a remote you control",
		"These are also reachable from a remote you have configured. Rewriting them means a force-push, and that is always yours to run, never something this tool does for you.",
	)

	writeUnreachableNarrative(&builder, traceReport)
	writeStalenessNote(&builder, traceReport)
	writeWarnings(&builder, traceReport)

	return builder.String()
}

func writeNothingFound(builder *strings.Builder, traceReport trace.Report) {
	builder.WriteString("Nothing found.\n\n")
	builder.WriteString("Your old name does not appear anywhere this scan looked. There is nothing to do here.\n")
	writeStalenessNote(builder, traceReport)
	writeWarnings(builder, traceReport)
}

func writeZoneSection(builder *strings.Builder, traceReport trace.Report, zone domain.OccurrenceZone, heading, explanation string) {
	count := traceReport.OccurrenceCountByZone[zone]
	if count == 0 {
		return
	}
	fmt.Fprintf(builder, "%s: %s\n", heading, pluralise(count, "occurrence", "occurrences"))
	fmt.Fprintf(builder, "  %s\n", explanation)
	for _, occurrence := range occurrencesForZone(traceReport, zone) {
		fmt.Fprintf(builder, "    %s\n", describeOccurrence(occurrence))
	}
	builder.WriteString("\n")
}

func writeUnreachableNarrative(builder *strings.Builder, traceReport trace.Report) {
	if !traceReport.RepositoryHasAnyRemote {
		return
	}
	builder.WriteString("Beyond your reach, and that is not your fault\n")
	builder.WriteString("  Once history has been shared, other people's forks, clones, and archived copies may still hold the old name. ")
	builder.WriteString("These cannot be reached programmatically, so this tool does not pretend to. ")
	builder.WriteString("If the old name is on a hosted copy you care about, GitHub can help scrub cached views:\n")
	fmt.Fprintf(builder, "    %s\n\n", githubSensitiveDataURL)
}

func writeStalenessNote(builder *strings.Builder, traceReport trace.Report) {
	if !traceReport.RepositoryHasAnyRemote {
		return
	}
	if traceReport.RemoteTrackingRefsWereRefreshed {
		builder.WriteString("Remote state was refreshed for this scan, so the zones above are current.\n")
		return
	}
	builder.WriteString("These zones reflect the last-known remote state. Re-run with --fetch to refresh it before deciding.\n")
}

func writeWarnings(builder *strings.Builder, traceReport trace.Report) {
	if len(traceReport.Warnings) == 0 {
		return
	}
	builder.WriteString("\nNotes:\n")
	for _, warning := range traceReport.Warnings {
		fmt.Fprintf(builder, "  - %s\n", warning)
	}
}

func occurrencesForZone(traceReport trace.Report, zone domain.OccurrenceZone) []domain.DeadnameOccurrence {
	matching := make([]domain.DeadnameOccurrence, 0)
	for _, occurrence := range traceReport.Occurrences {
		if occurrence.Zone == zone {
			matching = append(matching, occurrence)
		}
	}
	sort.SliceStable(matching, func(firstIndex, secondIndex int) bool {
		return matching[firstIndex].Location < matching[secondIndex].Location
	})
	return matching
}

func describeOccurrence(occurrence domain.DeadnameOccurrence) string {
	locationDescription := humanLocation(occurrence.Location)
	if occurrence.Location == domain.LocationFileContent && occurrence.FilePath != "" {
		locationDescription = fmt.Sprintf("in the file %s", occurrence.FilePath)
	}
	shortCommit := occurrence.CommitHash
	if len(shortCommit) > 8 {
		shortCommit = shortCommit[:8]
	}
	if shortCommit != "" {
		locationDescription = fmt.Sprintf("%s (commit %s)", locationDescription, shortCommit)
	}
	if occurrence.SurroundingContext != "" {
		return fmt.Sprintf("%s: %q", locationDescription, occurrence.SurroundingContext)
	}
	return locationDescription
}

func humanLocation(location domain.OccurrenceLocation) string {
	switch location {
	case domain.LocationAuthorName:
		return "in an author name"
	case domain.LocationAuthorEmail:
		return "in an author email"
	case domain.LocationCommitterName:
		return "in a committer name"
	case domain.LocationCommitterEmail:
		return "in a committer email"
	case domain.LocationCommitMessage:
		return "in a commit message"
	case domain.LocationAnnotatedTagMetadata:
		return "in an annotated tag"
	case domain.LocationFileContent:
		return "in a file"
	default:
		return "somewhere in the repository"
	}
}

func pluralise(count int, singular, plural string) string {
	if count == 1 {
		return fmt.Sprintf("1 %s", singular)
	}
	return fmt.Sprintf("%d %s", count, plural)
}
