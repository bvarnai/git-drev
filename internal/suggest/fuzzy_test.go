package suggest_test

import (
	"testing"

	"github.com/bvarnai/git-drev/internal/suggest"
	"github.com/stretchr/testify/assert"
)

func TestLevenshtein(t *testing.T) {
	tests := []struct {
		a, b     string
		expected int
	}{
		{"", "", 0},
		{"a", "", 1},
		{"", "b", 1},
		{"same", "same", 0},
		{"Same", "same", 0}, // case-insensitive
		{"featrue", "feature", 2},
		{"main", "mainn", 1},
		{"kitten", "sitting", 3},
	}

	for _, tc := range tests {
		dist := suggest.Levenshtein(tc.a, tc.b)
		assert.Equal(t, tc.expected, dist, "Levenshtein(%q, %q)", tc.a, tc.b)
	}
}

func TestClosest(t *testing.T) {
	candidates := []string{"master", "main", "feature/login", "feature/checkout", "issue/102"}

	tests := []struct {
		name     string
		target   string
		maxDist  int
		expected string
	}{
		{
			name:     "typo in prefix",
			target:   "featrue/login",
			maxDist:  3,
			expected: "feature/login",
		},
		{
			name:     "single char typo in main",
			target:   "maiin",
			maxDist:  2,
			expected: "main",
		},
		{
			name:     "case difference",
			target:   "MASTER",
			maxDist:  1,
			expected: "master",
		},
		{
			name:     "too far away returns empty",
			target:   "completely-different",
			maxDist:  3,
			expected: "",
		},
		{
			name:     "empty target returns empty",
			target:   "",
			maxDist:  3,
			expected: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := suggest.Closest(tc.target, candidates, tc.maxDist)
			assert.Equal(t, tc.expected, res)
		})
	}
}
