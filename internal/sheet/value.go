package sheet

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

type ValueKind int

const (
	KindBlank ValueKind = iota
	KindNumber
	KindText
	KindBool
	KindError
)

const (
	ErrDiv0   = "#DIV/0!"
	ErrValue  = "#VALUE!"
	ErrRef    = "#REF!"
	ErrName   = "#NAME?"
	ErrNA     = "#N/A"
	ErrCycle  = "#CYCLE!"
	ErrSyntax = "#ERROR!"
)

type Value struct {
	Kind    ValueKind
	Num     float64
	Str     string
	Boolean bool
	Err     string
}

func NewBlank() Value {
	return Value{Kind: KindBlank}
}

func NewNumber(n float64) Value {
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return Value{Kind: KindError, Err: ErrDiv0}
	}
	return Value{Kind: KindNumber, Num: n}
}

func NewText(s string) Value {
	return Value{Kind: KindText, Str: s}
}

func NewBool(b bool) Value {
	return Value{Kind: KindBool, Boolean: b}
}

func NewError(e string) Value {
	return Value{Kind: KindError, Err: e}
}

func (v Value) IsError() bool {
	return v.Kind == KindError
}

func (v Value) IsBlank() bool {
	return v.Kind == KindBlank
}

func FormatGeneralNumber(n float64) string {
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return ErrDiv0
	}
	abs := math.Abs(n)
	if abs != 0 && (abs >= 1e21 || abs < 1e-9) {
		s := strconv.FormatFloat(n, 'E', 10, 64)
		return strings.Replace(s, "E+", "E+", 1)
	}
	// General format: up to 15 digits of precision, no trailing zeros
	s := strconv.FormatFloat(n, 'g', 15, 64)
	if strings.Contains(s, "e") {
		s = strings.ToUpper(s)
	}
	return s
}

func (v Value) ToDisplayString() string {
	switch v.Kind {
	case KindBlank:
		return ""
	case KindNumber:
		return FormatGeneralNumber(v.Num)
	case KindText:
		return v.Str
	case KindBool:
		if v.Boolean {
			return "TRUE"
		}
		return "FALSE"
	case KindError:
		return v.Err
	default:
		return ""
	}
}

func (v Value) ToNumber() (float64, Value) {
	switch v.Kind {
	case KindBlank:
		return 0, NewBlank()
	case KindNumber:
		return v.Num, NewBlank()
	case KindBool:
		if v.Boolean {
			return 1, NewBlank()
		}
		return 0, NewBlank()
	case KindText:
		trimmed := strings.TrimSpace(v.Str)
		if trimmed == "" {
			return 0, NewBlank()
		}
		n, err := strconv.ParseFloat(trimmed, 64)
		if err != nil {
			return 0, NewError(ErrValue)
		}
		return n, NewBlank()
	case KindError:
		return 0, v
	default:
		return 0, NewError(ErrValue)
	}
}

func (v Value) ToBool() (bool, Value) {
	switch v.Kind {
	case KindBlank:
		return false, NewBlank()
	case KindBool:
		return v.Boolean, NewBlank()
	case KindNumber:
		return v.Num != 0, NewBlank()
	case KindText:
		upper := strings.ToUpper(strings.TrimSpace(v.Str))
		if upper == "TRUE" {
			return true, NewBlank()
		}
		if upper == "FALSE" {
			return false, NewBlank()
		}
		return false, NewError(ErrValue)
	case KindError:
		return false, v
	default:
		return false, NewError(ErrValue)
	}
}

// CompareValues implements Excel's comparison order:
// Blank < Number < Text < Bool
// Returns -1 if a < b, 0 if a == b, 1 if a > b.
// Errors propagate; if either is an error, returns ErrValue or first error.
func CompareValues(a, b Value) (int, Value) {
	if a.Kind == KindError {
		return 0, a
	}
	if b.Kind == KindError {
		return 0, b
	}

	if a.Kind != b.Kind {
		if a.Kind < b.Kind {
			return -1, NewBlank()
		}
		return 1, NewBlank()
	}

	switch a.Kind {
	case KindBlank:
		return 0, NewBlank()
	case KindNumber:
		if a.Num < b.Num {
			return -1, NewBlank()
		} else if a.Num > b.Num {
			return 1, NewBlank()
		}
		return 0, NewBlank()
	case KindText:
		// Excel comparisons are case-insensitive
		cmp := strings.Compare(strings.ToUpper(a.Str), strings.ToUpper(b.Str))
		if cmp < 0 {
			return -1, NewBlank()
		} else if cmp > 0 {
			return 1, NewBlank()
		}
		return 0, NewBlank()
	case KindBool:
		if !a.Boolean && b.Boolean {
			return -1, NewBlank()
		} else if a.Boolean && !b.Boolean {
			return 1, NewBlank()
		}
		return 0, NewBlank()
	}
	return 0, NewBlank()
}

type CellValue struct {
	Kind    string   `json:"kind"`
	Display string   `json:"display"`
	Number  *float64 `json:"number,omitempty"`
	Error   string   `json:"error,omitempty"`
	Formula string   `json:"formula,omitempty"`
}

func (v Value) ToCellValue(formula string) CellValue {
	cv := CellValue{
		Display: v.ToDisplayString(),
		Formula: formula,
	}
	switch v.Kind {
	case KindBlank:
		cv.Kind = "blank"
	case KindNumber:
		cv.Kind = "number"
		num := v.Num
		cv.Number = &num
	case KindText:
		cv.Kind = "text"
	case KindBool:
		cv.Kind = "bool"
	case KindError:
		cv.Kind = "error"
		cv.Error = v.Err
	}
	return cv
}

func (cv CellValue) ToValue() Value {
	switch cv.Kind {
	case "blank":
		return NewBlank()
	case "number":
		if cv.Number != nil {
			return NewNumber(*cv.Number)
		}
		n, err := strconv.ParseFloat(cv.Display, 64)
		if err == nil {
			return NewNumber(n)
		}
		return NewNumber(0)
	case "text":
		return NewText(cv.Display)
	case "bool":
		return NewBool(strings.ToUpper(cv.Display) == "TRUE")
	case "error":
		return NewError(cv.Error)
	default:
		if cv.Formula != "" {
			return NewBlank()
		}
		if cv.Display == "" {
			return NewBlank()
		}
		return NewText(cv.Display)
	}
}

func (v Value) String() string {
	switch v.Kind {
	case KindBlank:
		return "<blank>"
	case KindNumber:
		return fmt.Sprintf("Num(%s)", FormatGeneralNumber(v.Num))
	case KindText:
		return fmt.Sprintf("Text(%q)", v.Str)
	case KindBool:
		return fmt.Sprintf("Bool(%t)", v.Boolean)
	case KindError:
		return fmt.Sprintf("Error(%s)", v.Err)
	default:
		return "<unknown>"
	}
}
