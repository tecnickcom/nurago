/*
Package stringmetric provides string distance functions for approximate text
matching, comparison, and fuzzy search.

This package currently implements the Damerau-Levenshtein edit distance via
[DLDistance], which measures the minimum number of edit operations required to
transform one string into another.

# What It Computes

[DLDistance] returns an integer distance where:

  - 0 means the strings are identical.
  - Higher values mean less similarity.
  - Allowed operations are insertion, deletion, substitution, and adjacent
    transposition.

The implementation is rune-based (not byte-based), so it handles Unicode text
correctly and does not break on multi-byte UTF-8 characters.

Distance is measured over Unicode code points, so canonically-equivalent but
differently-normalized strings compare as different: NFC "é" (one rune) and NFD
"e" + combining accent (two runes) render identically yet have distance 1.
Callers that want visual equivalence should normalize both inputs (for example
to NFC) before calling.

# Guarantees

[DLDistance] is a true metric. For all strings a, b, c it is non-negative,
returns 0 if and only if a == b, is symmetric (DLDistance(a, b) equals
DLDistance(b, a)), is bounded above by max(len(a), len(b)) counted in runes, and
satisfies the triangle inequality (DLDistance(a, c) <= DLDistance(a, b) +
DLDistance(b, c)). It is a pure function and safe for concurrent use.

# Implementation Notes

The algorithm uses dynamic programming with:

  - an alphabet index map for tracking prior rune positions,
  - a distance matrix initialized with sentinel boundaries,
  - transition costs for substitution, insertion, deletion, and transposition.

This delivers deterministic O(|a|*|b|) time and O(|a|*|b|) memory: the full
matrix must be retained because the transposition term can reference any earlier
row, so the two-row optimization used for plain Levenshtein does not apply.

# Usage

	d := stringmetric.DLDistance("a cat", "a act") // 1 (adjacent transposition)
	if d <= 2 {
	    // treat as likely typo match
	}

# When To Use

  - Search results are ranked by closeness rather than exact match.
  - Typos in user input should still find the intended record.
*/
package stringmetric

// DLDistance computes Damerau-Levenshtein edit distance between two rune-based strings, counting insertion/deletion/substitution/transposition operations.
func DLDistance(sa, sb string) int {
	if sa == sb {
		return 0
	}

	ra := []rune(sa)
	rb := []rune(sb)
	ralen := len(ra)
	rblen := len(rb)
	maxdist := ralen + rblen

	if (ralen == 0) || (rblen == 0) {
		return maxdist
	}

	// initialize alphabet (Σ)
	da := initDLAlphabet(ra, rb, maxdist)

	// initialize distance matrix (flattened to a single slice, row-major, so the
	// whole grid is one allocation with good cache locality)
	nrows := ralen + 2 // one row per rune of sa, plus two sentinel rows
	ncols := rblen + 2 // one column per rune of sb, plus two sentinel columns
	dist := initDLMatrix(nrows, ncols, maxdist)

	// fill the distance matrix
	for i := 2; i < nrows; i++ {
		db := 1 // matrix column of the last match in the current row (1 = none)
		row := i * ncols
		prev := row - ncols

		for j := 2; j < ncols; j++ {
			k := da[rb[j-2]] // matrix row of the last occurrence of rb[j-2] in ra (1 = none)
			l := db
			tcost := (i - k - 1) + 1 + (j - l - 1) // transposition cost = deletions + swap + insertions
			scost := 1                             // substitution cost

			if ra[i-2] == rb[j-2] {
				scost = 0
				db = j
			}

			dist[row+j] = min(
				dist[prev+j-1]+scost,          // substitution
				dist[row+j-1]+1,               // insertion
				dist[prev+j]+1,                // deletion
				dist[(k-1)*ncols+(l-1)]+tcost, // transposition
			)
		}

		da[ra[i-2]] = i
	}

	// Example:
	// "a cat" (one transposition)-> "a act" (one insertion)-> "a abct"
	//
	//	              a  ·  a  b  c  t    (· denotes the space in "a abct")
	//	        0  1  2  3  4  5  6  7
	//	     +-------------------------+
	//	   0 | 11 11 11 11 11 11 11 11 |
	//	   1 | 11  0  1  2  3  4  5  6 |
	//	a  2 | 11  1  0  1  2  3  4  5 |
	//	·  3 | 11  2  1  0  1  2  3  4 |
	//	c  4 | 11  3  2  1  1  2  2  3 |
	//	a  5 | 11  4  3  2  1  2  2  3 |
	//	t  6 | 11  5  4  3  2  2  3  2 |
	//	     +-------------------------+

	return dist[nrows*ncols-1] // bottom-right value
}

// initDLAlphabet initialize the alphabet (Σ) for the Damerau-Levenshtein distance calculation.
// Each rune starts at matrix index 1 ("never seen"), so that the transposition
// lookup at row k-1, column l-1 hits the maxdist sentinel row/column.
func initDLAlphabet(ra, rb []rune, maxdist int) map[rune]int {
	da := make(map[rune]int, maxdist)

	for _, c := range ra {
		da[c] = 1
	}

	for _, c := range rb {
		da[c] = 1
	}

	return da
}

// initDLMatrix create and initialize the Damerau-Levenshtein distance matrix,
// flattened to a single row-major slice (element [i][j] lives at i*ncols+j), by
// populating the first two rows and columns.
//
// Example:
//
//	              a  ·  a  b  c  t    (· denotes the space in "a abct")
//	        0  1  2  3  4  5  6  7
//	     +-------------------------+
//	   0 | 11 11 11 11 11 11 11 11 |
//	   1 | 11  0  1  2  3  4  5  6 |
//	a  2 | 11  1  0  0  0  0  0  0 |
//	·  3 | 11  2  0  0  0  0  0  0 |
//	c  4 | 11  3  0  0  0  0  0  0 |
//	a  5 | 11  4  0  0  0  0  0  0 |
//	t  6 | 11  5  0  0  0  0  0  0 |
//	     +-------------------------+
func initDLMatrix(nrows, ncols, maxdist int) []int {
	dist := make([]int, nrows*ncols)

	for i := range nrows {
		dist[i*ncols] = maxdist // column 0 sentinel
		dist[i*ncols+1] = i - 1 // column 1 boundary
	}

	dist[1] = maxdist // [0][1] sentinel, overrides the -1 written above

	for j := 2; j < ncols; j++ {
		dist[j] = maxdist     // row 0 sentinel
		dist[ncols+j] = j - 1 // row 1 boundary
	}

	return dist
}
