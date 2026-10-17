package crdt

import (
	"fmt"
	"strings"
)

// Pos represents a fractional-index position key.
// Pos strings sort in standard ASCII lexicographical order (a < b).
type Pos string

const (
	Base62 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
)

// Less returns true if a is lexicographically less than b.
func Less(a, b Pos) bool {
	return a < b
}

// DefaultPos generates the canonical baseline position for integer index n.
func DefaultPos(n int) Pos {
	return IntegerToPos(n)
}

// IntegerToPos converts an integer n >= 0 into a variable-length sortable key.
// n = 0 -> "a0", n = 1 -> "a1", ..., n = 61 -> "az"
// n = 62 -> "b00", ..., n = 3905 -> "bzz"
// n = 3906 -> "c000", ...
func IntegerToPos(n int) Pos {
	if n < 0 {
		// Negative integers: 'Z', 'Y', 'X', ...
		posN := -n - 1
		if posN < 62 {
			d := Base62[61-posN]
			return Pos(fmt.Sprintf("Z%c", d))
		}
		posN -= 62
		if posN < 62*62 {
			d0 := Base62[61-(posN/62)]
			d1 := Base62[61-(posN%62)]
			return Pos(fmt.Sprintf("Y%c%c", d0, d1))
		}
		posN -= 62 * 62
		d0 := Base62[61-(posN/(62*62))]
		d1 := Base62[61-((posN/62)%62)]
		d2 := Base62[61-(posN%62)]
		return Pos(fmt.Sprintf("X%c%c%c", d0, d1, d2))
	}

	if n < 62 {
		return Pos(fmt.Sprintf("a%c", Base62[n]))
	}
	n -= 62
	if n < 62*62 {
		d0 := Base62[n/62]
		d1 := Base62[n%62]
		return Pos(fmt.Sprintf("b%c%c", d0, d1))
	}
	n -= 62 * 62
	if n < 62*62*62 {
		d0 := Base62[n/(62*62)]
		d1 := Base62[(n/62)%62]
		d2 := Base62[n%62]
		return Pos(fmt.Sprintf("c%c%c%c", d0, d1, d2))
	}
	n -= 62 * 62 * 62
	d0 := Base62[n/(62*62*62)]
	d1 := Base62[(n/(62*62))%62]
	d2 := Base62[(n/62)%62]
	d3 := Base62[n%62]
	return Pos(fmt.Sprintf("d%c%c%c%c", d0, d1, d2, d3))
}

// PosToInteger attempts to parse a baseline position back to an integer index.
func PosToInteger(p Pos) (int, bool) {
	s := string(p)
	if len(s) < 2 {
		return 0, false
	}
	head := s[0]
	if head >= 'a' && head <= 'd' {
		expectedLen := int(head-'a') + 2
		if len(s) != expectedLen {
			return 0, false
		}
		val := 0
		for i := 1; i < len(s); i++ {
			idx := strings.IndexByte(Base62, s[i])
			if idx < 0 {
				return 0, false
			}
			val = val*62 + idx
		}
		switch head {
		case 'a':
			return val, true
		case 'b':
			return val + 62, true
		case 'c':
			return val + 62 + 62*62, true
		case 'd':
			return val + 62 + 62*62 + 62*62*62, true
		}
	}
	return 0, false
}

func getIntegerPartLength(head byte) int {
	if head >= 'a' && head <= 'z' {
		return int(head-'a') + 2
	}
	if head >= 'A' && head <= 'Z' {
		return int('Z'-head) + 2
	}
	return 2
}

func incrementInteger(intPart string) string {
	head := intPart[0]
	if head >= 'a' && head <= 'd' {
		val, ok := PosToInteger(Pos(intPart))
		if ok {
			return string(IntegerToPos(val + 1))
		}
	}
	// Fallback general increment
	runes := []byte(intPart)
	for i := len(runes) - 1; i >= 1; i-- {
		idx := strings.IndexByte(Base62, runes[i])
		if idx < len(Base62)-1 {
			runes[i] = Base62[idx+1]
			return string(runes)
		}
		runes[i] = Base62[0]
	}
	// Overflow
	if head < 'z' {
		newHead := head + 1
		newLen := getIntegerPartLength(newHead)
		b := make([]byte, newLen)
		b[0] = newHead
		for i := 1; i < newLen; i++ {
			b[i] = Base62[0]
		}
		return string(b)
	}
	return intPart + "V"
}

func decrementInteger(intPart string) string {
	head := intPart[0]
	if head >= 'a' && head <= 'd' {
		val, ok := PosToInteger(Pos(intPart))
		if ok {
			return string(IntegerToPos(val - 1))
		}
	}
	runes := []byte(intPart)
	for i := len(runes) - 1; i >= 1; i-- {
		idx := strings.IndexByte(Base62, runes[i])
		if idx > 0 {
			runes[i] = Base62[idx-1]
			return string(runes)
		}
		runes[i] = Base62[len(Base62)-1]
	}
	// Underflow from Z0 -> Yzz -> Xzzz ...
	if head > 'A' && head <= 'Z' {
		newHead := head - 1
		newLen := int('Z'-newHead) + 2
		b := make([]byte, newLen)
		b[0] = newHead
		for i := 1; i < newLen; i++ {
			b[i] = Base62[len(Base62)-1]
		}
		return string(b)
	}
	return "A00000000000000000000000000"
}

// midpointBase62 computes a base-62 fractional string strictly between a and b.
// a and b are fractional parts (may be empty). b may be nil string marker.
func midpointBase62(a string, b *string) string {
	if b != nil && a >= *b {
		return a + "V"
	}
	// Strip common prefix
	common := 0
	if b != nil {
		for common < len(a) && common < len(*b) && a[common] == (*b)[common] {
			common++
		}
		if common > 0 {
			remB := (*b)[common:]
			return a[:common] + midpointBase62(a[common:], &remB)
		}
	}

	digitA := 0
	if len(a) > 0 {
		idx := strings.IndexByte(Base62, a[0])
		if idx >= 0 {
			digitA = idx
		}
	}

	digitB := len(Base62)
	if b != nil && len(*b) > 0 {
		idx := strings.IndexByte(Base62, (*b)[0])
		if idx >= 0 {
			digitB = idx
		}
	}

	if digitB-digitA > 1 {
		mid := (digitA + digitB) / 2
		return string(Base62[mid])
	}

	// Consecutive digits
	if b != nil && len(*b) > 1 {
		return (*b)[:1]
	}

	// b is nil or length 1
	remA := ""
	if len(a) > 1 {
		remA = a[1:]
	}
	return string(Base62[digitA]) + midpointBase62(remA, nil)
}

// Between generates a Pos strictly between a and b (a < Between(a, b) < b).
// If a is empty, it returns a key strictly before b.
// If b is empty, it returns a key strictly after a.
// If both are empty, it returns "a0".
func Between(a, b Pos) Pos {
	return BetweenWithActor(a, b, "")
}

// BetweenWithActor generates a Pos strictly between a and b with optional actor tie-breaker.
func BetweenWithActor(a, b Pos, actor string) Pos {
	if a == "" && b == "" {
		if actor != "" {
			return Pos("a0:" + actor)
		}
		return Pos("a0")
	}

	if a == "" {
		// Key before b
		sb := string(b)
		colon := strings.IndexByte(sb, ':')
		cleanB := sb
		if colon >= 0 {
			cleanB = sb[:colon]
		}
		intLen := getIntegerPartLength(cleanB[0])
		if len(cleanB) < intLen {
			intLen = len(cleanB)
		}
		intPart := cleanB[:intLen]
		fracPart := cleanB[intLen:]

		if fracPart != "" {
			res := intPart
			if actor != "" && Pos(res+":"+actor) < b {
				return Pos(res + ":" + actor)
			}
			if Pos(res) < b {
				return Pos(res)
			}
		}
		dec := decrementInteger(intPart)
		if actor != "" {
			return Pos(dec + ":" + actor)
		}
		return Pos(dec)
	}

	if b == "" {
		// Key after a
		sa := string(a)
		colon := strings.IndexByte(sa, ':')
		cleanA := sa
		if colon >= 0 {
			cleanA = sa[:colon]
		}
		intLen := getIntegerPartLength(cleanA[0])
		if len(cleanA) < intLen {
			intLen = len(cleanA)
		}
		intPart := cleanA[:intLen]
		inc := incrementInteger(intPart)
		if actor != "" {
			return Pos(inc + ":" + actor)
		}
		return Pos(inc)
	}

	if a >= b {
		// Fallback for corrupt bounds: force strict order
		res := string(a) + "V"
		if actor != "" {
			res += ":" + actor
		}
		return Pos(res)
	}

	sa := string(a)
	sb := string(b)

	intLenA := getIntegerPartLength(sa[0])
	if len(sa) < intLenA {
		intLenA = len(sa)
	}
	intPartA := sa[:intLenA]
	fracPartA := sa[intLenA:]

	intLenB := getIntegerPartLength(sb[0])
	if len(sb) < intLenB {
		intLenB = len(sb)
	}
	intPartB := sb[:intLenB]
	fracPartB := sb[intLenB:]

	var candidate Pos
	if intPartA < intPartB {
		incA := incrementInteger(intPartA)
		if incA < intPartB {
			candidate = Pos(incA)
		} else {
			// Consecutive integer parts
			mid := midpointBase62(fracPartA, nil)
			candidate = Pos(intPartA + mid)
		}
	} else if intPartA == intPartB {
		mid := midpointBase62(fracPartA, &fracPartB)
		candidate = Pos(intPartA + mid)
	} else {
		// String fallback
		mid := midpointBase62(sa, &sb)
		candidate = Pos(mid)
	}

	if actor != "" {
		withActor := Pos(string(candidate) + ":" + actor)
		if a < withActor && withActor < b {
			return withActor
		}
	}

	if a < candidate && candidate < b {
		return candidate
	}

	// Refined fallback midpoint between exact strings
	fallback := midpointBase62(sa, &sb)
	res := Pos(fallback)
	if !(a < res && res < b) {
		res = Pos(sa + "V")
	}
	if actor != "" {
		resWithActor := Pos(string(res) + ":" + actor)
		if a < resWithActor && resWithActor < b {
			return resWithActor
		}
	}
	return res
}

// ValidatePosBetween checks that pos is strictly between after and before (if provided).
func ValidatePosBetween(pos, after, before Pos) bool {
	if pos == "" {
		return false
	}
	if after != "" && pos <= after {
		return false
	}
	if before != "" && pos >= before {
		return false
	}
	return true
}
