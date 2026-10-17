package sheet

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"
)

type FunctionDef struct {
	MinArgs  int
	MaxArgs  int // -1 for unbounded variadic
	Lazy     bool
	Volatile bool
	Fn       func(args []Value, ctx EvalContext) Value
	LazyFn   func(args []Node, ctx EvalContext) Value
}

var FunctionTable map[string]FunctionDef

func init() {
	FunctionTable = map[string]FunctionDef{
		// ----------------------------------------------------
		// Aggregation Functions (10)
		// ----------------------------------------------------
		"SUM": {
			MinArgs: 0, MaxArgs: -1,
			Fn: func(args []Value, ctx EvalContext) Value {
				total := 0.0
				for _, a := range args {
					if a.IsError() {
						return a
					}
					if a.Kind == KindNumber {
						total += a.Num
					} else if a.Kind == KindText || a.Kind == KindBool {
						n, errVal := a.ToNumber()
						if errVal.IsError() {
							return errVal
						}
						total += n
					}
				}
				return NewNumber(total)
			},
		},
		"AVERAGE": {
			MinArgs: 1, MaxArgs: -1,
			Fn: func(args []Value, ctx EvalContext) Value {
				total := 0.0
				count := 0
				for _, a := range args {
					if a.IsError() {
						return a
					}
					if a.Kind == KindNumber {
						total += a.Num
						count++
					} else if a.Kind == KindText || a.Kind == KindBool {
						n, errVal := a.ToNumber()
						if errVal.IsError() {
							return errVal
						}
						total += n
						count++
					}
				}
				if count == 0 {
					return NewError(ErrDiv0)
				}
				return NewNumber(total / float64(count))
			},
		},
		"COUNT": {
			MinArgs: 0, MaxArgs: -1,
			Fn: func(args []Value, ctx EvalContext) Value {
				count := 0
				for _, a := range args {
					if a.Kind == KindNumber {
						count++
					}
				}
				return NewNumber(float64(count))
			},
		},
		"COUNTA": {
			MinArgs: 0, MaxArgs: -1,
			Fn: func(args []Value, ctx EvalContext) Value {
				count := 0
				for _, a := range args {
					if !a.IsBlank() {
						count++
					}
				}
				return NewNumber(float64(count))
			},
		},
		"MIN": {
			MinArgs: 1, MaxArgs: -1,
			Fn: func(args []Value, ctx EvalContext) Value {
				minVal := math.MaxFloat64
				found := false
				for _, a := range args {
					if a.IsError() {
						return a
					}
					if a.Kind == KindNumber {
						if a.Num < minVal {
							minVal = a.Num
						}
						found = true
					}
				}
				if !found {
					return NewNumber(0)
				}
				return NewNumber(minVal)
			},
		},
		"MAX": {
			MinArgs: 1, MaxArgs: -1,
			Fn: func(args []Value, ctx EvalContext) Value {
				maxVal := -math.MaxFloat64
				found := false
				for _, a := range args {
					if a.IsError() {
						return a
					}
					if a.Kind == KindNumber {
						if a.Num > maxVal {
							maxVal = a.Num
						}
						found = true
					}
				}
				if !found {
					return NewNumber(0)
				}
				return NewNumber(maxVal)
			},
		},
		"PRODUCT": {
			MinArgs: 0, MaxArgs: -1,
			Fn: func(args []Value, ctx EvalContext) Value {
				if len(args) == 0 {
					return NewNumber(0)
				}
				prod := 1.0
				found := false
				for _, a := range args {
					if a.IsError() {
						return a
					}
					if a.Kind == KindNumber {
						prod *= a.Num
						found = true
					}
				}
				if !found {
					return NewNumber(0)
				}
				return NewNumber(prod)
			},
		},
		"MEDIAN": {
			MinArgs: 1, MaxArgs: -1,
			Fn: func(args []Value, ctx EvalContext) Value {
				var nums []float64
				for _, a := range args {
					if a.IsError() {
						return a
					}
					if a.Kind == KindNumber {
						nums = append(nums, a.Num)
					}
				}
				if len(nums) == 0 {
					return NewError(ErrDiv0)
				}
				sort.Float64s(nums)
				n := len(nums)
				if n%2 == 1 {
					return NewNumber(nums[n/2])
				}
				return NewNumber((nums[n/2-1] + nums[n/2]) / 2.0)
			},
		},
		"VAR": {
			MinArgs: 1, MaxArgs: -1,
			Fn: func(args []Value, ctx EvalContext) Value {
				var nums []float64
				sum := 0.0
				for _, a := range args {
					if a.IsError() {
						return a
					}
					if a.Kind == KindNumber {
						nums = append(nums, a.Num)
						sum += a.Num
					}
				}
				n := len(nums)
				if n < 2 {
					return NewError(ErrDiv0)
				}
				mean := sum / float64(n)
				varSum := 0.0
				for _, x := range nums {
					varSum += (x - mean) * (x - mean)
				}
				return NewNumber(varSum / float64(n-1))
			},
		},
		"STDEV": {
			MinArgs: 1, MaxArgs: -1,
			Fn: func(args []Value, ctx EvalContext) Value {
				v := FunctionTable["VAR"].Fn(args, ctx)
				if v.IsError() {
					return v
				}
				return NewNumber(math.Sqrt(v.Num))
			},
		},

		// ----------------------------------------------------
		// Logic Functions (9) - IF, IFERROR, IFNA, AND, OR are Lazy
		// ----------------------------------------------------
		"IF": {
			MinArgs: 2, MaxArgs: 3, Lazy: true,
			LazyFn: func(args []Node, ctx EvalContext) Value {
				if len(args) < 2 || len(args) > 3 {
					return NewError(ErrValue)
				}
				cond := Evaluate(args[0], ctx)
				if cond.IsError() {
					return cond
				}
				b, errVal := cond.ToBool()
				if errVal.IsError() {
					return errVal
				}
				if b {
					return Evaluate(args[1], ctx)
				}
				if len(args) == 3 {
					return Evaluate(args[2], ctx)
				}
				return NewBool(false)
			},
		},
		"IFERROR": {
			MinArgs: 2, MaxArgs: 2, Lazy: true,
			LazyFn: func(args []Node, ctx EvalContext) Value {
				if len(args) != 2 {
					return NewError(ErrValue)
				}
				val := Evaluate(args[0], ctx)
				if val.IsError() {
					return Evaluate(args[1], ctx)
				}
				return val
			},
		},
		"IFNA": {
			MinArgs: 2, MaxArgs: 2, Lazy: true,
			LazyFn: func(args []Node, ctx EvalContext) Value {
				if len(args) != 2 {
					return NewError(ErrValue)
				}
				val := Evaluate(args[0], ctx)
				if val.IsError() && val.Err == ErrNA {
					return Evaluate(args[1], ctx)
				}
				return val
			},
		},
		"AND": {
			MinArgs: 1, MaxArgs: -1, Lazy: true,
			LazyFn: func(args []Node, ctx EvalContext) Value {
				for _, argNode := range args {
					v := Evaluate(argNode, ctx)
					if v.IsError() {
						return v
					}
					b, errVal := v.ToBool()
					if errVal.IsError() {
						return errVal
					}
					if !b {
						return NewBool(false)
					}
				}
				return NewBool(true)
			},
		},
		"OR": {
			MinArgs: 1, MaxArgs: -1, Lazy: true,
			LazyFn: func(args []Node, ctx EvalContext) Value {
				for _, argNode := range args {
					v := Evaluate(argNode, ctx)
					if v.IsError() {
						return v
					}
					b, errVal := v.ToBool()
					if errVal.IsError() {
						return errVal
					}
					if b {
						return NewBool(true)
					}
				}
				return NewBool(false)
			},
		},
		"NOT": {
			MinArgs: 1, MaxArgs: 1,
			Fn: func(args []Value, ctx EvalContext) Value {
				if args[0].IsError() {
					return args[0]
				}
				b, errVal := args[0].ToBool()
				if errVal.IsError() {
					return errVal
				}
				return NewBool(!b)
			},
		},
		"XOR": {
			MinArgs: 1, MaxArgs: -1,
			Fn: func(args []Value, ctx EvalContext) Value {
				count := 0
				for _, a := range args {
					if a.IsError() {
						return a
					}
					b, errVal := a.ToBool()
					if errVal.IsError() {
						return errVal
					}
					if b {
						count++
					}
				}
				return NewBool(count%2 == 1)
			},
		},
		"TRUE": {
			MinArgs: 0, MaxArgs: 0,
			Fn: func(args []Value, ctx EvalContext) Value {
				return NewBool(true)
			},
		},
		"FALSE": {
			MinArgs: 0, MaxArgs: 0,
			Fn: func(args []Value, ctx EvalContext) Value {
				return NewBool(false)
			},
		},

		// ----------------------------------------------------
		// Math Functions (17)
		// ----------------------------------------------------
		"ABS": {
			MinArgs: 1, MaxArgs: 1,
			Fn: func(args []Value, ctx EvalContext) Value {
				n, errVal := args[0].ToNumber()
				if errVal.IsError() {
					return errVal
				}
				return NewNumber(math.Abs(n))
			},
		},
		"ROUND": {
			MinArgs: 1, MaxArgs: 2,
			Fn: func(args []Value, ctx EvalContext) Value {
				n, errVal := args[0].ToNumber()
				if errVal.IsError() {
					return errVal
				}
				digits := 0.0
				if len(args) > 1 {
					d, errVal := args[1].ToNumber()
					if errVal.IsError() {
						return errVal
					}
					digits = d
				}
				factor := math.Pow(10, digits)
				return NewNumber(math.Round(n*factor) / factor)
			},
		},
		"ROUNDUP": {
			MinArgs: 1, MaxArgs: 2,
			Fn: func(args []Value, ctx EvalContext) Value {
				n, errVal := args[0].ToNumber()
				if errVal.IsError() {
					return errVal
				}
				digits := 0.0
				if len(args) > 1 {
					d, errVal := args[1].ToNumber()
					if errVal.IsError() {
						return errVal
					}
					digits = d
				}
				factor := math.Pow(10, digits)
				if n >= 0 {
					return NewNumber(math.Ceil(n*factor) / factor)
				}
				return NewNumber(math.Floor(n*factor) / factor)
			},
		},
		"ROUNDDOWN": {
			MinArgs: 1, MaxArgs: 2,
			Fn: func(args []Value, ctx EvalContext) Value {
				n, errVal := args[0].ToNumber()
				if errVal.IsError() {
					return errVal
				}
				digits := 0.0
				if len(args) > 1 {
					d, errVal := args[1].ToNumber()
					if errVal.IsError() {
						return errVal
					}
					digits = d
				}
				factor := math.Pow(10, digits)
				if n >= 0 {
					return NewNumber(math.Floor(n*factor) / factor)
				}
				return NewNumber(math.Ceil(n*factor) / factor)
			},
		},
		"INT": {
			MinArgs: 1, MaxArgs: 1,
			Fn: func(args []Value, ctx EvalContext) Value {
				n, errVal := args[0].ToNumber()
				if errVal.IsError() {
					return errVal
				}
				return NewNumber(math.Floor(n))
			},
		},
		"MOD": {
			MinArgs: 2, MaxArgs: 2,
			Fn: func(args []Value, ctx EvalContext) Value {
				n, errVal := args[0].ToNumber()
				if errVal.IsError() {
					return errVal
				}
				d, errVal := args[1].ToNumber()
				if errVal.IsError() {
					return errVal
				}
				if d == 0 {
					return NewError(ErrDiv0)
				}
				res := math.Mod(n, d)
				if (res < 0 && d > 0) || (res > 0 && d < 0) {
					res += d
				}
				return NewNumber(res)
			},
		},
		"POWER": {
			MinArgs: 2, MaxArgs: 2,
			Fn: func(args []Value, ctx EvalContext) Value {
				base, errVal := args[0].ToNumber()
				if errVal.IsError() {
					return errVal
				}
				exp, errVal := args[1].ToNumber()
				if errVal.IsError() {
					return errVal
				}
				res := math.Pow(base, exp)
				if math.IsNaN(res) || math.IsInf(res, 0) {
					return NewError(ErrValue)
				}
				return NewNumber(res)
			},
		},
		"SQRT": {
			MinArgs: 1, MaxArgs: 1,
			Fn: func(args []Value, ctx EvalContext) Value {
				n, errVal := args[0].ToNumber()
				if errVal.IsError() {
					return errVal
				}
				if n < 0 {
					return NewError(ErrValue)
				}
				return NewNumber(math.Sqrt(n))
			},
		},
		"EXP": {
			MinArgs: 1, MaxArgs: 1,
			Fn: func(args []Value, ctx EvalContext) Value {
				n, errVal := args[0].ToNumber()
				if errVal.IsError() {
					return errVal
				}
				return NewNumber(math.Exp(n))
			},
		},
		"LN": {
			MinArgs: 1, MaxArgs: 1,
			Fn: func(args []Value, ctx EvalContext) Value {
				n, errVal := args[0].ToNumber()
				if errVal.IsError() {
					return errVal
				}
				if n <= 0 {
					return NewError(ErrValue)
				}
				return NewNumber(math.Log(n))
			},
		},
		"LOG": {
			MinArgs: 1, MaxArgs: 2,
			Fn: func(args []Value, ctx EvalContext) Value {
				n, errVal := args[0].ToNumber()
				if errVal.IsError() {
					return errVal
				}
				if n <= 0 {
					return NewError(ErrValue)
				}
				base := 10.0
				if len(args) > 1 {
					b, errVal := args[1].ToNumber()
					if errVal.IsError() {
						return errVal
					}
					if b <= 0 || b == 1 {
						return NewError(ErrValue)
					}
					base = b
				}
				return NewNumber(math.Log(n) / math.Log(base))
			},
		},
		"LOG10": {
			MinArgs: 1, MaxArgs: 1,
			Fn: func(args []Value, ctx EvalContext) Value {
				n, errVal := args[0].ToNumber()
				if errVal.IsError() {
					return errVal
				}
				if n <= 0 {
					return NewError(ErrValue)
				}
				return NewNumber(math.Log10(n))
			},
		},
		"PI": {
			MinArgs: 0, MaxArgs: 0,
			Fn: func(args []Value, ctx EvalContext) Value {
				return NewNumber(math.Pi)
			},
		},
		"RAND": {
			MinArgs: 0, MaxArgs: 0, Volatile: true,
			Fn: func(args []Value, ctx EvalContext) Value {
				return NewNumber(ctx.Rand())
			},
		},
		"RANDBETWEEN": {
			MinArgs: 2, MaxArgs: 2, Volatile: true,
			Fn: func(args []Value, ctx EvalContext) Value {
				bottom, errVal := args[0].ToNumber()
				if errVal.IsError() {
					return errVal
				}
				top, errVal := args[1].ToNumber()
				if errVal.IsError() {
					return errVal
				}
				if bottom > top {
					return NewError(ErrValue)
				}
				// Use injected ctx.Rand()
				r := ctx.Rand()
				val := bottom + math.Floor(r*(top-bottom+1))
				if val > top {
					val = top
				}
				return NewNumber(val)
			},
		},
		"SIGN": {
			MinArgs: 1, MaxArgs: 1,
			Fn: func(args []Value, ctx EvalContext) Value {
				n, errVal := args[0].ToNumber()
				if errVal.IsError() {
					return errVal
				}
				if n > 0 {
					return NewNumber(1)
				} else if n < 0 {
					return NewNumber(-1)
				}
				return NewNumber(0)
			},
		},
		"TRUNC": {
			MinArgs: 1, MaxArgs: 2,
			Fn: func(args []Value, ctx EvalContext) Value {
				n, errVal := args[0].ToNumber()
				if errVal.IsError() {
					return errVal
				}
				digits := 0.0
				if len(args) > 1 {
					d, errVal := args[1].ToNumber()
					if errVal.IsError() {
						return errVal
					}
					digits = d
				}
				factor := math.Pow(10, digits)
				return NewNumber(math.Trunc(n*factor) / factor)
			},
		},

		// ----------------------------------------------------
		// Text Functions (16)
		// ----------------------------------------------------
		"CONCATENATE": {
			MinArgs: 1, MaxArgs: -1,
			Fn: func(args []Value, ctx EvalContext) Value {
				var sb strings.Builder
				for _, a := range args {
					if a.IsError() {
						return a
					}
					sb.WriteString(a.ToDisplayString())
				}
				return NewText(sb.String())
			},
		},
		"LEFT": {
			MinArgs: 1, MaxArgs: 2,
			Fn: func(args []Value, ctx EvalContext) Value {
				s := args[0].ToDisplayString()
				n := 1
				if len(args) > 1 {
					num, errVal := args[1].ToNumber()
					if errVal.IsError() {
						return errVal
					}
					if num < 0 {
						return NewError(ErrValue)
					}
					n = int(num)
				}
				runes := []rune(s)
				if n > len(runes) {
					n = len(runes)
				}
				return NewText(string(runes[:n]))
			},
		},
		"RIGHT": {
			MinArgs: 1, MaxArgs: 2,
			Fn: func(args []Value, ctx EvalContext) Value {
				s := args[0].ToDisplayString()
				n := 1
				if len(args) > 1 {
					num, errVal := args[1].ToNumber()
					if errVal.IsError() {
						return errVal
					}
					if num < 0 {
						return NewError(ErrValue)
					}
					n = int(num)
				}
				runes := []rune(s)
				if n > len(runes) {
					n = len(runes)
				}
				return NewText(string(runes[len(runes)-n:]))
			},
		},
		"MID": {
			MinArgs: 3, MaxArgs: 3,
			Fn: func(args []Value, ctx EvalContext) Value {
				s := args[0].ToDisplayString()
				startNum, errVal := args[1].ToNumber()
				if errVal.IsError() {
					return errVal
				}
				numChars, errVal := args[2].ToNumber()
				if errVal.IsError() {
					return errVal
				}
				if startNum < 1 || numChars < 0 {
					return NewError(ErrValue)
				}
				runes := []rune(s)
				start := int(startNum) - 1
				if start >= len(runes) {
					return NewText("")
				}
				end := start + int(numChars)
				if end > len(runes) {
					end = len(runes)
				}
				return NewText(string(runes[start:end]))
			},
		},
		"LEN": {
			MinArgs: 1, MaxArgs: 1,
			Fn: func(args []Value, ctx EvalContext) Value {
				return NewNumber(float64(len([]rune(args[0].ToDisplayString()))))
			},
		},
		"LOWER": {
			MinArgs: 1, MaxArgs: 1,
			Fn: func(args []Value, ctx EvalContext) Value {
				return NewText(strings.ToLower(args[0].ToDisplayString()))
			},
		},
		"UPPER": {
			MinArgs: 1, MaxArgs: 1,
			Fn: func(args []Value, ctx EvalContext) Value {
				return NewText(strings.ToUpper(args[0].ToDisplayString()))
			},
		},
		"PROPER": {
			MinArgs: 1, MaxArgs: 1,
			Fn: func(args []Value, ctx EvalContext) Value {
				s := args[0].ToDisplayString()
				var sb strings.Builder
				newWord := true
				for _, r := range s {
					if unicode.IsLetter(r) {
						if newWord {
							sb.WriteRune(unicode.ToUpper(r))
							newWord = false
						} else {
							sb.WriteRune(unicode.ToLower(r))
						}
					} else {
						sb.WriteRune(r)
						newWord = true
					}
				}
				return NewText(sb.String())
			},
		},
		"TRIM": {
			MinArgs: 1, MaxArgs: 1,
			Fn: func(args []Value, ctx EvalContext) Value {
				// Excel TRIM removes leading/trailing spaces and collapses consecutive spaces to one
				fields := strings.Fields(args[0].ToDisplayString())
				return NewText(strings.Join(fields, " "))
			},
		},
		"SUBSTITUTE": {
			MinArgs: 3, MaxArgs: 4,
			Fn: func(args []Value, ctx EvalContext) Value {
				text := args[0].ToDisplayString()
				oldText := args[1].ToDisplayString()
				newText := args[2].ToDisplayString()
				if len(args) == 4 {
					instanceNum, errVal := args[3].ToNumber()
					if errVal.IsError() || instanceNum < 1 {
						return NewError(ErrValue)
					}
					inst := int(instanceNum)
					count := 0
					idx := 0
					for {
						pos := strings.Index(text[idx:], oldText)
						if pos == -1 {
							return NewText(text)
						}
						count++
						if count == inst {
							realPos := idx + pos
							return NewText(text[:realPos] + newText + text[realPos+len(oldText):])
						}
						idx += pos + len(oldText)
					}
				}
				return NewText(strings.ReplaceAll(text, oldText, newText))
			},
		},
		"FIND": {
			MinArgs: 2, MaxArgs: 3,
			Fn: func(args []Value, ctx EvalContext) Value {
				findText := args[0].ToDisplayString()
				withinText := args[1].ToDisplayString()
				start := 1
				if len(args) == 3 {
					s, errVal := args[2].ToNumber()
					if errVal.IsError() || s < 1 {
						return NewError(ErrValue)
					}
					start = int(s)
				}
				runes := []rune(withinText)
				if start > len(runes) {
					return NewError(ErrValue)
				}
				sub := string(runes[start-1:])
				pos := strings.Index(sub, findText)
				if pos == -1 {
					return NewError(ErrValue)
				}
				runeOffset := len([]rune(sub[:pos]))
				return NewNumber(float64(start + runeOffset))
			},
		},
		"REPLACE": {
			MinArgs: 4, MaxArgs: 4,
			Fn: func(args []Value, ctx EvalContext) Value {
				oldText := args[0].ToDisplayString()
				startNum, errVal := args[1].ToNumber()
				if errVal.IsError() || startNum < 1 {
					return NewError(ErrValue)
				}
				numChars, errVal := args[2].ToNumber()
				if errVal.IsError() || numChars < 0 {
					return NewError(ErrValue)
				}
				newText := args[3].ToDisplayString()

				runes := []rune(oldText)
				start := int(startNum) - 1
				if start > len(runes) {
					start = len(runes)
				}
				end := start + int(numChars)
				if end > len(runes) {
					end = len(runes)
				}
				return NewText(string(runes[:start]) + newText + string(runes[end:]))
			},
		},
		"REPT": {
			MinArgs: 2, MaxArgs: 2,
			Fn: func(args []Value, ctx EvalContext) Value {
				s := args[0].ToDisplayString()
				num, errVal := args[1].ToNumber()
				if errVal.IsError() || num < 0 {
					return NewError(ErrValue)
				}
				return NewText(strings.Repeat(s, int(num)))
			},
		},
		"TEXTJOIN": {
			MinArgs: 3, MaxArgs: -1,
			Fn: func(args []Value, ctx EvalContext) Value {
				delimiter := args[0].ToDisplayString()
				ignoreEmpty, errVal := args[1].ToBool()
				if errVal.IsError() {
					return errVal
				}
				var parts []string
				for _, a := range args[2:] {
					if a.IsError() {
						return a
					}
					str := a.ToDisplayString()
					if ignoreEmpty && (a.IsBlank() || str == "") {
						continue
					}
					parts = append(parts, str)
				}
				return NewText(strings.Join(parts, delimiter))
			},
		},
		"VALUE": {
			MinArgs: 1, MaxArgs: 1,
			Fn: func(args []Value, ctx EvalContext) Value {
				n, errVal := args[0].ToNumber()
				if errVal.IsError() {
					return errVal
				}
				return NewNumber(n)
			},
		},
		"TEXT": {
			MinArgs: 1, MaxArgs: 2,
			Fn: func(args []Value, ctx EvalContext) Value {
				if len(args) == 1 {
					return NewText(args[0].ToDisplayString())
				}
				format := args[1].ToDisplayString()
				n, errVal := args[0].ToNumber()
				if errVal.IsError() {
					return NewText(args[0].ToDisplayString())
				}
				if format == "0.00" {
					return NewText(fmt.Sprintf("%.2f", n))
				}
				if format == "0%" {
					return NewText(fmt.Sprintf("%.0f%%", n*100))
				}
				return NewText(FormatGeneralNumber(n))
			},
		},

		// ----------------------------------------------------
		// Information & Date Functions (10)
		// ----------------------------------------------------
		"ISBLANK": {
			MinArgs: 1, MaxArgs: 1,
			Fn: func(args []Value, ctx EvalContext) Value {
				return NewBool(args[0].IsBlank())
			},
		},
		"ISNUMBER": {
			MinArgs: 1, MaxArgs: 1,
			Fn: func(args []Value, ctx EvalContext) Value {
				return NewBool(args[0].Kind == KindNumber)
			},
		},
		"ISTEXT": {
			MinArgs: 1, MaxArgs: 1,
			Fn: func(args []Value, ctx EvalContext) Value {
				return NewBool(args[0].Kind == KindText)
			},
		},
		"ISERROR": {
			MinArgs: 1, MaxArgs: 1,
			Fn: func(args []Value, ctx EvalContext) Value {
				return NewBool(args[0].IsError())
			},
		},
		"ISERR": {
			MinArgs: 1, MaxArgs: 1,
			Fn: func(args []Value, ctx EvalContext) Value {
				return NewBool(args[0].IsError() && args[0].Err != ErrNA)
			},
		},
		"ISNA": {
			MinArgs: 1, MaxArgs: 1,
			Fn: func(args []Value, ctx EvalContext) Value {
				return NewBool(args[0].IsError() && args[0].Err == ErrNA)
			},
		},
		"NA": {
			MinArgs: 0, MaxArgs: 0,
			Fn: func(args []Value, ctx EvalContext) Value {
				return NewError(ErrNA)
			},
		},
		"ISLOGICAL": {
			MinArgs: 1, MaxArgs: 1,
			Fn: func(args []Value, ctx EvalContext) Value {
				return NewBool(args[0].Kind == KindBool)
			},
		},
		"NOW": {
			MinArgs: 0, MaxArgs: 0, Volatile: true,
			Fn: func(args []Value, ctx EvalContext) Value {
				t := ctx.Now()
				// Excel serial date representation: days since Dec 30 1899
				serial := float64(t.Unix())/86400.0 + 25569.0
				return NewNumber(serial)
			},
		},
		"TODAY": {
			MinArgs: 0, MaxArgs: 0, Volatile: true,
			Fn: func(args []Value, ctx EvalContext) Value {
				t := ctx.Now()
				year, month, day := t.Date()
				midnight := time.Date(year, month, day, 0, 0, 0, 0, t.Location())
				serial := float64(midnight.Unix())/86400.0 + 25569.0
				return NewNumber(serial)
			},
		},
	}
}
