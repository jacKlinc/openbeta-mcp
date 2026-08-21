package grade

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// frenchPattern matches the sport grades OpenBeta records for FR-context areas:
// 4, 5a, 6a+, 6b/6c, 7c+.
//
// A bare number always spans its letters, and cannot do otherwise: French uses
// bare numbers as grades low down, so "5" might be the grade 5 or a 5b whose
// letter went unrecorded, and there is nothing in the string to say which.
// Spanning keeps that ambiguity visible rather than resolving it by fiat.
var frenchPattern = regexp.MustCompile(`^(\d)([abc])?(\+)?(?:/(\d)?([abc])?(\+)?)?$`)

// frenchLetters is the width of a French number: a, b, c.
const frenchLetters = 3

// frenchOrdinal packs a French grade into a sortable integer.
//
// The + is a step of its own here, unlike the YDS +/- which only orders routes
// within a grade: 6a+ sits between 6a and 6b and climbers treat it that way.
func frenchOrdinal(number, letter int, plus bool) int {
	n := (number*frenchLetters + letter) * 2
	if plus {
		n++
	}
	return n
}

// ParseFrench turns a recorded French sport grade into the span it covers.
func ParseFrench(s string) (Span, error) {
	t := strings.ToLower(strings.TrimSpace(s))
	m := frenchPattern.FindStringSubmatch(t)
	if m == nil {
		return Span{}, fmt.Errorf("not a French grade: %q", s)
	}

	number, err := strconv.Atoi(m[1])
	if err != nil {
		return Span{}, fmt.Errorf("not a French grade: %q", s)
	}

	lo := frenchOrdinal(number, letterIndex(m[2]), m[3] == "+")
	hi := lo
	if m[2] == "" {
		// A bare number spans its letters: the letter was never recorded, not
		// absent from the route.
		hi = frenchOrdinal(number, frenchLetters-1, true)
	}

	// "6b/6c" and "6a/b" both appear; the second half may repeat the number or
	// carry only the letter.
	if m[5] != "" || m[4] != "" {
		second := number
		if m[4] != "" {
			if second, err = strconv.Atoi(m[4]); err != nil {
				return Span{}, fmt.Errorf("not a French grade: %q", s)
			}
		}
		hi = frenchOrdinal(second, letterIndex(m[5]), m[6] == "+")
		if hi < lo {
			lo, hi = hi, lo
		}
	}

	return Span{Lo: lo, Hi: hi, System: French}, nil
}
