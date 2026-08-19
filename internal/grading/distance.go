package grading

// damerauLevenshtein returns the optimal string alignment distance between a
// and b, counting a transposition of two adjacent characters as a single edit.
//
// It operates on runes, not bytes, so multi-byte characters count as one.
func damerauLevenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	la, lb := len(ra), len(rb)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}

	twoBack := make([]int, lb+1)
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}

	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			curr[j] = min(curr[j-1]+1, prev[j]+1, prev[j-1]+cost)
			if i > 1 && j > 1 && ra[i-1] == rb[j-2] && ra[i-2] == rb[j-1] {
				if transposed := twoBack[j-2] + 1; transposed < curr[j] {
					curr[j] = transposed
				}
			}
		}
		twoBack, prev, curr = prev, curr, twoBack
	}
	return prev[lb]
}
