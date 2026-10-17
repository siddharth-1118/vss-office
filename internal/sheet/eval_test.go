package sheet

import (
	"math"
	"testing"
	"time"
)

func evalFormula(formula string, ctx EvalContext) Value {
	node, err := ParseFormula(formula)
	if err != nil {
		return NewError(err.Error())
	}
	return Evaluate(node, ctx)
}

func TestEvaluator_TableTests(t *testing.T) {
	fixedTime := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	ctx := &DefaultEvalContext{
		Cells: map[CellAddr]Value{
			{Row: 0, Col: 0}: NewNumber(10),     // A1
			{Row: 1, Col: 0}: NewNumber(20),     // A2
			{Row: 2, Col: 0}: NewNumber(30),     // A3
			{Row: 0, Col: 1}: NewText("apple"),  // B1
			{Row: 1, Col: 1}: NewText("banana"), // B2
			{Row: 2, Col: 1}: NewText("cherry"), // B3
			{Row: 0, Col: 2}: NewBool(true),     // C1
			{Row: 1, Col: 2}: NewBool(false),    // C2
			{Row: 2, Col: 2}: NewBlank(),        // C3
		},
		Clock: fixedTime,
		RNG:   0.42,
	}

	tests := []struct {
		name     string
		expr     string
		expected Value
	}{
		// Literals & Arithmetic
		{"Number literal", "42", NewNumber(42)},
		{"String literal", `"hello"`, NewText("hello")},
		{"Bool literal TRUE", "TRUE()", NewBool(true)},
		{"Bool literal FALSE", "FALSE()", NewBool(false)},
		{"Addition", "2+3", NewNumber(5)},
		{"Subtraction", "10-4", NewNumber(6)},
		{"Multiplication", "3*4", NewNumber(12)},
		{"Division", "12/4", NewNumber(3)},
		{"Division by zero", "1/0", NewError(ErrDiv0)},
		{"Power", "2^3", NewNumber(8)},
		{"Exponent precedence", "-2^2", NewNumber(4)}, // In Excel, unary minus is tighter than power: (-2)^2 = 4
		{"Percentage", "50%", NewNumber(0.5)},
		{"Percentage addition", "100%+50%", NewNumber(1.5)},
		{"String concatenation", `"foo" & "bar"`, NewText("foobar")},
		{"Comparison Eq True", "5=5", NewBool(true)},
		{"Comparison Eq False", "5=6", NewBool(false)},
		{"Comparison Ne True", "5<>6", NewBool(true)},
		{"Comparison Lt True", "3<5", NewBool(true)},
		{"Comparison Le True", "5<=5", NewBool(true)},
		{"Comparison Gt True", "6>5", NewBool(true)},
		{"Comparison Ge True", "5>=5", NewBool(true)},
		{"Coercion string to number", `"5" + 3`, NewNumber(8)},
		{"Coercion bool to number", "TRUE + 2", NewNumber(3)},

		// References & Areas
		{"Cell ref A1", "A1", NewNumber(10)},
		{"Cell ref B1", "B1", NewText("apple")},
		{"Area sum", "SUM(A1:A3)", NewNumber(60)},
		{"Area count", "COUNT(A1:A3)", NewNumber(3)},

		// Aggregation Functions (10)
		{"SUM numbers", "SUM(1, 2, 3, 4)", NewNumber(10)},
		{"AVERAGE numbers", "AVERAGE(10, 20, 30)", NewNumber(20)},
		{"COUNT numbers only", `COUNT(1, "abc", TRUE, 4)`, NewNumber(2)},
		{"COUNTA non-blanks", `COUNTA(1, "abc", TRUE)`, NewNumber(3)},
		{"MIN numbers", "MIN(5, 2, 9, 1)", NewNumber(1)},
		{"MAX numbers", "MAX(5, 2, 9, 1)", NewNumber(9)},
		{"PRODUCT numbers", "PRODUCT(2, 3, 4)", NewNumber(24)},
		{"MEDIAN numbers", "MEDIAN(1, 2, 100)", NewNumber(2)},
		{"VAR numbers", "VAR(2, 4, 6)", NewNumber(4)},
		{"STDEV numbers", "STDEV(2, 4, 6)", NewNumber(2)},

		// Logic Functions (9)
		{"IF True", "IF(TRUE, 10, 20)", NewNumber(10)},
		{"IF False", "IF(FALSE, 10, 20)", NewNumber(20)},
		{"IF Short Circuit", "IF(TRUE, 1, 1/0)", NewNumber(1)},
		{"IF False Short Circuit", "IF(FALSE, 1/0, 2)", NewNumber(2)},
		{"IFERROR Caught", `IFERROR(1/0, "err")`, NewText("err")},
		{"IFERROR Pass", `IFERROR(42, "err")`, NewNumber(42)},
		{"IFNA Caught", `IFNA(#N/A, "not found")`, NewText("not found")},
		{"IFNA Uncaught Div0", `IFNA(1/0, "not found")`, NewError(ErrDiv0)},
		{"AND all true", "AND(TRUE, 1=1, 2>1)", NewBool(true)},
		{"AND with false short-circuit", "AND(FALSE, 1/0)", NewBool(false)},
		{"OR one true short-circuit", "OR(TRUE, 1/0)", NewBool(true)},
		{"OR all false", "OR(FALSE, 1>2)", NewBool(false)},
		{"NOT true", "NOT(TRUE)", NewBool(false)},
		{"NOT false", "NOT(FALSE)", NewBool(true)},
		{"XOR one true", "XOR(TRUE, FALSE)", NewBool(true)},
		{"XOR both true", "XOR(TRUE, TRUE)", NewBool(false)},
		{"TRUE function", "TRUE()", NewBool(true)},
		{"FALSE function", "FALSE()", NewBool(false)},

		// Math Functions (17)
		{"ABS negative", "ABS(-42)", NewNumber(42)},
		{"ROUND", "ROUND(3.14159, 2)", NewNumber(3.14)},
		{"ROUNDUP", "ROUNDUP(3.14159, 2)", NewNumber(3.15)},
		{"ROUNDDOWN", "ROUNDDOWN(3.149, 2)", NewNumber(3.14)},
		{"INT", "INT(4.9)", NewNumber(4)},
		{"INT negative", "INT(-4.1)", NewNumber(-5)},
		{"MOD", "MOD(10, 3)", NewNumber(1)},
		{"POWER", "POWER(2, 4)", NewNumber(16)},
		{"SQRT positive", "SQRT(16)", NewNumber(4)},
		{"SQRT negative error", "SQRT(-1)", NewError(ErrValue)},
		{"EXP", "EXP(0)", NewNumber(1)},
		{"LN", "LN(1)", NewNumber(0)},
		{"LOG base 10", "LOG(100, 10)", NewNumber(2)},
		{"LOG10", "LOG10(1000)", NewNumber(3)},
		{"PI", "PI()", NewNumber(math.Pi)},
		{"RAND injected", "RAND()", NewNumber(0.42)},
		{"RANDBETWEEN injected", "RANDBETWEEN(1, 10)", NewNumber(5)}, // 1 + int(0.42 * 10) = 5
		{"SIGN positive", "SIGN(15)", NewNumber(1)},
		{"SIGN negative", "SIGN(-15)", NewNumber(-1)},
		{"SIGN zero", "SIGN(0)", NewNumber(0)},
		{"TRUNC", "TRUNC(4.9)", NewNumber(4)},

		// Text Functions (16)
		{"CONCATENATE", `CONCATENATE("a", "b", "c")`, NewText("abc")},
		{"LEFT", `LEFT("spreadsheet", 6)`, NewText("spread")},
		{"RIGHT", `RIGHT("spreadsheet", 5)`, NewText("sheet")},
		{"MID", `MID("spreadsheet", 7, 5)`, NewText("sheet")},
		{"LEN", `LEN("hello")`, NewNumber(5)},
		{"LOWER", `LOWER("HELLO")`, NewText("hello")},
		{"UPPER", `UPPER("hello")`, NewText("HELLO")},
		{"PROPER", `PROPER("hello world")`, NewText("Hello World")},
		{"TRIM", `TRIM("  hello   world  ")`, NewText("hello world")},
		{"SUBSTITUTE", `SUBSTITUTE("banana", "a", "o")`, NewText("bonono")},
		{"FIND match", `FIND("bar", "foobar")`, NewNumber(4)},
		{"FIND case sensitive mismatch", `FIND("Bar", "foobar")`, NewError(ErrValue)},
		{"REPLACE", `REPLACE("abcdef", 2, 3, "XYZ")`, NewText("aXYZef")},
		{"REPT", `REPT("ha", 3)`, NewText("hahaha")},
		{"TEXTJOIN", `TEXTJOIN("-", TRUE, "a", "", "b")`, NewText("a-b")},
		{"VALUE", `VALUE("123.45")`, NewNumber(123.45)},
		{"TEXT format 0.00", `TEXT(12.3, "0.00")`, NewText("12.30")},

		// Information & Date Functions (10)
		{"ISBLANK true", "ISBLANK(C3)", NewBool(true)},
		{"ISBLANK false", "ISBLANK(A1)", NewBool(false)},
		{"ISNUMBER true", "ISNUMBER(42)", NewBool(true)},
		{"ISNUMBER false", `ISNUMBER("42")`, NewBool(false)},
		{"ISTEXT true", `ISTEXT("hello")`, NewBool(true)},
		{"ISTEXT false", "ISTEXT(42)", NewBool(false)},
		{"ISERROR true", "ISERROR(1/0)", NewBool(true)},
		{"ISERROR false", "ISERROR(42)", NewBool(false)},
		{"ISERR true on DIV0", "ISERR(1/0)", NewBool(true)},
		{"ISERR false on NA", "ISERR(#N/A)", NewBool(false)},
		{"ISNA true", "ISNA(#N/A)", NewBool(true)},
		{"ISNA false", "ISNA(1/0)", NewBool(false)},
		{"NA function", "NA()", NewError(ErrNA)},
		{"ISLOGICAL true", "ISLOGICAL(TRUE)", NewBool(true)},
		{"ISLOGICAL false", "ISLOGICAL(1)", NewBool(false)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := evalFormula(tt.expr, ctx)
			if got.Kind != tt.expected.Kind {
				t.Fatalf("expr %q: expected kind %v, got %v (val=%v)", tt.expr, tt.expected.Kind, got.Kind, got)
			}
			switch got.Kind {
			case KindNumber:
				if math.Abs(got.Num-tt.expected.Num) > 1e-6 {
					t.Errorf("expr %q: expected num %v, got %v", tt.expr, tt.expected.Num, got.Num)
				}
			case KindText:
				if got.Str != tt.expected.Str {
					t.Errorf("expr %q: expected str %q, got %q", tt.expr, tt.expected.Str, got.Str)
				}
			case KindBool:
				if got.Boolean != tt.expected.Boolean {
					t.Errorf("expr %q: expected bool %v, got %v", tt.expr, tt.expected.Boolean, got.Boolean)
				}
			case KindError:
				if got.Err != tt.expected.Err {
					t.Errorf("expr %q: expected err %v, got %v", tt.expr, tt.expected.Err, got.Err)
				}
			}
		})
	}
}

func TestEvaluator_VolatileClock(t *testing.T) {
	fixedTime := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	ctx := &DefaultEvalContext{
		Clock: fixedTime,
	}

	nowVal := evalFormula("NOW()", ctx)
	if nowVal.Kind != KindNumber {
		t.Fatalf("NOW() expected number, got %v", nowVal)
	}
	expectedNowSerial := float64(fixedTime.Unix())/86400.0 + 25569.0
	if math.Abs(nowVal.Num-expectedNowSerial) > 1e-6 {
		t.Errorf("NOW() expected %f, got %f", expectedNowSerial, nowVal.Num)
	}

	todayVal := evalFormula("TODAY()", ctx)
	if todayVal.Kind != KindNumber {
		t.Fatalf("TODAY() expected number, got %v", todayVal)
	}
	expectedTodaySerial := float64(time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC).Unix())/86400.0 + 25569.0
	if math.Abs(todayVal.Num-expectedTodaySerial) > 1e-6 {
		t.Errorf("TODAY() expected %f, got %f", expectedTodaySerial, todayVal.Num)
	}
}
