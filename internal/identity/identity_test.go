package identity

import (
	"strings"
	"testing"
)

func TestParseOld(testingHandle *testing.T) {
	testCases := []struct {
		caseName        string
		category        string // "happy" | "negative" | "edge" | "error"
		rawNames        []string
		rawEmails       []string
		expectError     bool
		expectedNames   []string
		expectedEmails  []string
		expectWarning   bool
		warningContains string
	}{
		{
			caseName:       "full name and email parse",
			category:       "happy",
			rawNames:       []string{"  Old Name  "},
			rawEmails:      []string{"old.name@example.test"},
			expectedNames:  []string{"Old Name"},
			expectedEmails: []string{"old.name@example.test"},
		},
		{
			caseName:       "duplicates are removed and order preserved",
			category:       "happy",
			rawNames:       []string{"Old Name", "Old Name", "Older Name"},
			expectedNames:  []string{"Old Name", "Older Name"},
			expectedEmails: []string{},
		},
		{
			caseName:       "emails only with no names is valid",
			category:       "negative",
			rawNames:       []string{"   "},
			rawEmails:      []string{"old.name@example.test"},
			expectedNames:  []string{},
			expectedEmails: []string{"old.name@example.test"},
		},
		{
			caseName:        "short single word name is flagged not blocked",
			category:        "edge",
			rawNames:        []string{"Al"},
			expectedNames:   []string{"Al"},
			expectedEmails:  []string{},
			expectWarning:   true,
			warningContains: "broad",
		},
		{
			caseName:       "common word name is flagged",
			category:       "edge",
			rawNames:       []string{"Rose"},
			expectedNames:  []string{"Rose"},
			expectedEmails: []string{},
			expectWarning:  true,
		},
		{
			caseName:       "long single word name is not flagged",
			category:       "edge",
			rawNames:       []string{"Alexandra"},
			expectedNames:  []string{"Alexandra"},
			expectedEmails: []string{},
			expectWarning:  false,
		},
		{
			caseName:       "unicode and regex metacharacter names parse literally",
			category:       "edge",
			rawNames:       []string{"Óld Támé", "A.*B(C)"},
			expectedNames:  []string{"Óld Támé", "A.*B(C)"},
			expectedEmails: []string{},
		},
		{
			caseName:    "no names and no emails is an error",
			category:    "error",
			rawNames:    []string{"", "   "},
			rawEmails:   nil,
			expectError: true,
		},
	}
	for _, testCase := range testCases {
		testingHandle.Run(testCase.caseName, func(subTest *testing.T) {
			oldIdentity, warnings, parseError := ParseOld(testCase.rawNames, testCase.rawEmails)
			if testCase.expectError {
				if parseError == nil {
					subTest.Fatal("expected an error but got none")
				}
				return
			}
			if parseError != nil {
				subTest.Fatalf("unexpected error: %v", parseError)
			}
			assertStringSliceEqual(subTest, "names", oldIdentity.Names, testCase.expectedNames)
			assertStringSliceEqual(subTest, "emails", oldIdentity.Emails, testCase.expectedEmails)

			hasWarning := len(warnings) > 0
			if hasWarning != testCase.expectWarning {
				subTest.Fatalf("warning presence: expected %v, got %v (%+v)", testCase.expectWarning, hasWarning, warnings)
			}
			if testCase.warningContains != "" {
				joinedWarnings := ""
				for _, warning := range warnings {
					joinedWarnings += warning.Message
				}
				if !strings.Contains(joinedWarnings, testCase.warningContains) {
					subTest.Errorf("expected a warning containing %q, got %q", testCase.warningContains, joinedWarnings)
				}
			}
		})
	}
}

func TestParseNew(testingHandle *testing.T) {
	testCases := []struct {
		caseName      string
		category      string
		rawName       string
		rawEmail      string
		expectError   bool
		expectedName  string
		expectedEmail string
	}{
		{caseName: "valid new identity", category: "happy", rawName: "  New Name ", rawEmail: " new@example.test ", expectedName: "New Name", expectedEmail: "new@example.test"},
		{caseName: "plus-tagged email is accepted", category: "edge", rawName: "New Name", rawEmail: "new+tag@example.test", expectedName: "New Name", expectedEmail: "new+tag@example.test"},
		{caseName: "empty name is rejected", category: "error", rawName: "   ", rawEmail: "new@example.test", expectError: true},
		{caseName: "empty email is rejected", category: "error", rawName: "New Name", rawEmail: "", expectError: true},
	}
	for _, testCase := range testCases {
		testingHandle.Run(testCase.caseName, func(subTest *testing.T) {
			newIdentity, parseError := ParseNew(testCase.rawName, testCase.rawEmail)
			if testCase.expectError {
				if parseError == nil {
					subTest.Fatal("expected an error but got none")
				}
				return
			}
			if parseError != nil {
				subTest.Fatalf("unexpected error: %v", parseError)
			}
			if newIdentity.Name != testCase.expectedName {
				subTest.Errorf("name: expected %q, got %q", testCase.expectedName, newIdentity.Name)
			}
			if newIdentity.Email != testCase.expectedEmail {
				subTest.Errorf("email: expected %q, got %q", testCase.expectedEmail, newIdentity.Email)
			}
		})
	}
}

func assertStringSliceEqual(testingHandle *testing.T, label string, actual, expected []string) {
	testingHandle.Helper()
	if len(actual) != len(expected) {
		testingHandle.Fatalf("%s length: expected %v, got %v", label, expected, actual)
	}
	for index := range expected {
		if actual[index] != expected[index] {
			testingHandle.Fatalf("%s[%d]: expected %q, got %q", label, index, expected[index], actual[index])
		}
	}
}
