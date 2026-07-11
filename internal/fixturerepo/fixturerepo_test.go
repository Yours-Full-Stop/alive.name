package fixturerepo

import "testing"

// Unit tests: pure logic only, no filesystem and no git. The filesystem-touching
// helpers (firstLooseObjectPath against a real directory) are tested under the
// integration tag.

func TestValueOrFallback(testingHandle *testing.T) {
	testCases := []struct {
		caseName  string
		candidate string
		fallback  string
		expected  string
	}{
		{caseName: "blank uses fallback", candidate: "   ", fallback: "fallback", expected: "fallback"},
		{caseName: "empty uses fallback", candidate: "", fallback: "fallback", expected: "fallback"},
		{caseName: "value is kept", candidate: "actual", fallback: "fallback", expected: "actual"},
	}
	for _, testCase := range testCases {
		testingHandle.Run(testCase.caseName, func(subTest *testing.T) {
			if got := valueOrFallback(testCase.candidate, testCase.fallback); got != testCase.expected {
				subTest.Errorf("expected %q, got %q", testCase.expected, got)
			}
		})
	}
}
