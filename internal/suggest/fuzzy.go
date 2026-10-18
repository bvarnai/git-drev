package suggest

import (
	"strings"
)

// Levenshtein calculates the Levenshtein distance between two strings (case-insensitive).
func Levenshtein(a, b string) int {
	a = strings.ToLower(a)
	b = strings.ToLower(b)

	la := len(a)
	lb := len(b)

	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}

	dp := make([][]int, la+1)
	for i := range dp {
		dp[i] = make([]int, lb+1)
		dp[i][0] = i
	}
	for j := 0; j <= lb; j++ {
		dp[0][j] = j
	}

	for i := 1; i <= la; i++ {
		for j := 1; j <= lb; j++ {
			cost := 0
			if a[i-1] != b[j-1] {
				cost = 1
			}

			del := dp[i-1][j] + 1
			ins := dp[i][j-1] + 1
			sub := dp[i-1][j-1] + cost

			min := del
			if ins < min {
				min = ins
			}
			if sub < min {
				min = sub
			}
			dp[i][j] = min
		}
	}

	return dp[la][lb]
}

// Closest finds the closest matching string from candidates within maxDistance.
// Returns an empty string if no candidate is within the threshold.
func Closest(target string, candidates []string, maxDistance int) string {
	if len(candidates) == 0 || target == "" {
		return ""
	}

	bestMatch := ""
	bestDist := maxDistance + 1

	for _, cand := range candidates {
		if cand == "" || cand == target {
			continue
		}

		dist := Levenshtein(target, cand)
		if dist <= maxDistance && dist < bestDist {
			bestDist = dist
			bestMatch = cand
		}
	}

	return bestMatch
}
