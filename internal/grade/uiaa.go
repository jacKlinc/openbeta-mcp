package grade

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// uiaaModifiers is the width of a UIAA number: -, bare, +.
const uiaaModifiers = 3

// uiaaMaxNumber bounds the scale.
const uiaaMaxNumber = 12

// uiaaPattern matches the Arabic-numeral forms: 5, 6+, 7-, and the straddling
// 6/6+ and 7-/7.
//
// Roman numerals are deliberately unmatched. The schema says they are not
// supported (generated.go), so a "VII-" in the data is bad data rather than a
// form to accept.
var uiaaPattern = regexp.MustCompile(`^(\d{1,2})([+-])?(?:/(\d{1,2})?([+-])?)?$`)

// uiaaOrdinal packs a UIAA grade into a sortable integer.
//
// The modifiers are steps of their own here, unlike the YDS +/- which only
// orders routes within a number: 7-, 7 and 7+ are three grades.
func uiaaOrdinal(number int, modifier string) int {
	return number*uiaaModifiers + uiaaModifierIndex(modifier)
}

func uiaaModifierIndex(s string) int {
	switch s {
	case "-":
		return 0
	case "+":
		return 2
	default:
		return 1
	}
}

// ParseUIAA turns a recorded UIAA grade into the span it covers.
//
// A bare number is exact here, where a bare YDS "5.10" is a span: "7" is a
// grade sitting between 7- and 7+, not a letter nobody wrote down.
func ParseUIAA(s string) (Span, error) {
	t := strings.TrimSpace(s)
	m := uiaaPattern.FindStringSubmatch(t)
	if m == nil {
		return Span{}, fmt.Errorf("not a UIAA grade: %q", s)
	}

	number, err := uiaaNumber(m[1])
	if err != nil {
		return Span{}, fmt.Errorf("not a UIAA grade: %q", s)
	}

	lo := uiaaOrdinal(number, m[2])
	hi := lo

	// "6/6+" and "7-/7" both appear; the second half may repeat the number or
	// carry only the modifier.
	if m[3] != "" || m[4] != "" {
		second := number
		if m[3] != "" {
			if second, err = uiaaNumber(m[3]); err != nil {
				return Span{}, fmt.Errorf("not a UIAA grade: %q", s)
			}
		}
		hi = uiaaOrdinal(second, m[4])
		if hi < lo {
			lo, hi = hi, lo
		}
	}

	return Span{Lo: lo, Hi: hi, System: UIAA}, nil
}

func uiaaNumber(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, err
	}
	if n < 1 || n > uiaaMaxNumber {
		return 0, fmt.Errorf("outside the UIAA scale: %d", n)
	}
	return n, nil
}
