// Package identity parses and validates the old and new identity inputs.
//
// It normalises inputs, refuses an empty new identity, and, rather than
// blocking short old-name entries that risk matching inside unrelated words,
// returns them as warnings with a clear path forward. The user is never
// blocked, only informed.
package identity

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"alive.name/internal/domain"
)

// shortSingleTokenRuneThreshold is the length below which a single-word old name
// is flagged as a broad-match risk. It is a tunable heuristic, not a hard rule.
const shortSingleTokenRuneThreshold = 4

// commonWordsThatOverMatch are single words that are also ordinary English words,
// so matching them broadly is likely to catch unrelated text. The list is
// illustrative, not exhaustive; it exists to make the warning concrete.
var commonWordsThatOverMatch = map[string]struct{}{
	"rose":  {},
	"mark":  {},
	"hope":  {},
	"grace": {},
	"will":  {},
	"art":   {},
	"dawn":  {},
	"may":   {},
	"june":  {},
	"jean":  {},
	"sky":   {},
	"joy":   {},
}

// ValidationWarning is a non-blocking notice about an input, together with a
// concrete suggestion for what the user can do about it.
type ValidationWarning struct {
	Subject string
	Message string
}

// ParseOld builds and validates an OldIdentity from raw name and email inputs.
// It trims whitespace, drops blank entries, and de-duplicates. It returns an
// error only when nothing at all remains to search for. Short or common
// single-word names are returned as warnings, never as errors.
func ParseOld(rawNames, rawEmails []string) (domain.OldIdentity, []ValidationWarning, error) {
	cleanedNames := trimAndDeduplicate(rawNames)
	cleanedEmails := trimAndDeduplicate(rawEmails)

	if len(cleanedNames) == 0 && len(cleanedEmails) == 0 {
		return domain.OldIdentity{}, nil, errors.New("identity: at least one old name or old email is required to search for")
	}

	warnings := make([]ValidationWarning, 0)
	for _, candidateName := range cleanedNames {
		if warning, isRisky := broadMatchWarningFor(candidateName); isRisky {
			warnings = append(warnings, warning)
		}
	}

	oldIdentity := domain.OldIdentity{Names: cleanedNames, Emails: cleanedEmails}
	return oldIdentity, warnings, nil
}

// ParseNew builds and validates a NewIdentity. Both the name and the email are
// required; anything else about their shape is left alone so that unusual but
// legitimate addresses (for example plus-tagged emails) are accepted.
func ParseNew(rawName, rawEmail string) (domain.NewIdentity, error) {
	trimmedName := strings.TrimSpace(rawName)
	trimmedEmail := strings.TrimSpace(rawEmail)
	if trimmedName == "" {
		return domain.NewIdentity{}, errors.New("identity: the new name must not be empty")
	}
	if trimmedEmail == "" {
		return domain.NewIdentity{}, errors.New("identity: the new email must not be empty")
	}
	return domain.NewIdentity{Name: trimmedName, Email: trimmedEmail}, nil
}

// broadMatchWarningFor reports whether a single old name is likely to match
// inside unrelated words, and if so returns a warning explaining the risk and
// the path forward.
func broadMatchWarningFor(candidateName string) (ValidationWarning, bool) {
	if strings.ContainsAny(candidateName, " \t") {
		// A multi-word name is very unlikely to over-match.
		return ValidationWarning{}, false
	}
	runeLength := utf8.RuneCountInString(candidateName)
	_, isCommonWord := commonWordsThatOverMatch[strings.ToLower(candidateName)]
	if runeLength >= shortSingleTokenRuneThreshold && !isCommonWord {
		return ValidationWarning{}, false
	}
	return ValidationWarning{
		Subject: candidateName,
		Message: fmt.Sprintf(
			"The old name %q is a single short or common word, so a broad, case-insensitive search may also match it inside unrelated words. "+
				"You can proceed as-is, or narrow the search by pairing it with one of your old emails, or by using --case-sensitive.",
			candidateName,
		),
	}, true
}

// trimAndDeduplicate trims each entry, discards blanks, and removes exact
// duplicates while preserving first-seen order.
func trimAndDeduplicate(rawValues []string) []string {
	cleanedValues := make([]string, 0, len(rawValues))
	alreadySeen := make(map[string]struct{})
	for _, rawValue := range rawValues {
		trimmedValue := strings.TrimSpace(rawValue)
		if trimmedValue == "" {
			continue
		}
		if _, seen := alreadySeen[trimmedValue]; seen {
			continue
		}
		alreadySeen[trimmedValue] = struct{}{}
		cleanedValues = append(cleanedValues, trimmedValue)
	}
	return cleanedValues
}
