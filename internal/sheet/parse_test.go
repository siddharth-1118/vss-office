package sheet

import (
	"strings"
	"testing"
	"time"
)

func TestAddressFormattingAndParsing(t *testing.T) {
	tests := []struct {
		a1  string
		row int
		col int
	}{
		{"A1", 0, 0},
		{"B2", 1, 1},
		{"Z26", 25, 25},
		{"AA1", 0, 26},
		{"XFD1048576", 1048575, 16383},
		{"$A$1", 0, 0},
		{"$B5", 4, 1},
	}

	for _, tc := range tests {
		addr, err := Parse(tc.a1)
		if err != nil {
			t.Fatalf("Parse(%q) failed: %v", tc.a1, err)
		}
		if addr.Row != tc.row || addr.Col != tc.col {
			t.Errorf("Parse(%q) = (%d, %d), want (%d, %d)", tc.a1, addr.Row, addr.Col, tc.row, tc.col)
		}
		cleanA1 := strings.ReplaceAll(tc.a1, "$", "")
		if addr.Format() != cleanA1 {
			t.Errorf("addr.Format() = %q, want %q", addr.Format(), cleanA1)
		}
	}
}

func TestAreaCells(t *testing.T) {
	area, err := ParseArea("A1:B3")
	if err != nil {
		t.Fatalf("ParseArea failed: %v", err)
	}
	cells := area.Cells()
	if len(cells) != 6 {
		t.Fatalf("expected 6 cells, got %d", len(cells))
	}
	expected := []string{"A1", "B1", "A2", "B2", "A3", "B3"}
	for i, c := range cells {
		if c.Format() != expected[i] {
			t.Errorf("cell[%d] = %s, want %s", i, c.Format(), expected[i])
		}
	}
}

func TestValueCoercionAndComparison(t *testing.T) {
	// Blank < Number < Text < Bool
	blank := NewBlank()
	num := NewNumber(100)
	text := NewText("apple")
	b := NewBool(false)

	cmp, _ := CompareValues(blank, num)
	if cmp != -1 {
		t.Errorf("expected Blank < Number, got cmp=%d", cmp)
	}
	cmp, _ = CompareValues(num, text)
	if cmp != -1 {
		t.Errorf("expected Number < Text, got cmp=%d", cmp)
	}
	cmp, _ = CompareValues(text, b)
	if cmp != -1 {
		t.Errorf("expected Text < Bool, got cmp=%d", cmp)
	}

	// Number to text formatting
	if FormatGeneralNumber(12345.67) != "12345.67" {
		t.Errorf("FormatGeneralNumber failed: got %s", FormatGeneralNumber(12345.67))
	}
	if FormatGeneralNumber(100.0) != "100" {
		t.Errorf("FormatGeneralNumber trailing zero: got %s", FormatGeneralNumber(100.0))
	}

	// Coercions
	nVal, errVal := NewText("42").ToNumber()
	if errVal.IsError() || nVal != 42 {
		t.Errorf("text '42' to number failed: %v, %v", nVal, errVal)
	}
	nVal, errVal = NewText("abc").ToNumber()
	if !errVal.IsError() || errVal.Err != ErrValue {
		t.Errorf("text 'abc' should fail coercion to number, got %v", errVal)
	}
	nVal, errVal = NewBool(true).ToNumber()
	if errVal.IsError() || nVal != 1 {
		t.Errorf("bool true to number should be 1, got %v", nVal)
	}
}

func TestParserForms(t *testing.T) {
	forms := []string{
		"=1+2*3",
		"=\"hello \"\"world\"\"\"",
		"=TRUE",
		"=FALSE",
		"=#DIV/0!",
		"=#N/A",
		"=SUM(A1:B5, 10, \"test\")",
		"=A1 + B1",
		"=100%",
		"=-(2+3)",
	}

	for _, form := range forms {
		node, err := ParseFormula(form)
		if err != nil {
			t.Errorf("ParseFormula(%q) failed: %v", form, err)
		}
		if node == nil {
			t.Errorf("ParseFormula(%q) returned nil node", form)
		}
	}
}

func TestPrecedencePowerAndUnary(t *testing.T) {
	// In Excel, unary binds tighter than ^:
	// -2^2 = (-2)^2 = 4
	node, err := ParseFormula("=-2^2")
	if err != nil {
		t.Fatalf("ParseFormula failed: %v", err)
	}
	// AST structure must be BinaryNode(^, Left: UnaryNode(-, 2), Right: 2)
	bin, ok := node.(BinaryNode)
	if !ok || bin.Op != tokCaret {
		t.Fatalf("expected BinaryNode with caret, got %T: %v", node, node)
	}
	unary, ok := bin.Left.(UnaryNode)
	if !ok || unary.Op != tokMinus {
		t.Fatalf("expected UnaryNode on left of caret, got %T: %v", bin.Left, bin.Left)
	}
}

func TestLexerGuardErrorConsumption(t *testing.T) {
	// Lexer guard test: parsing error literal must not spin or allocate forever
	start := time.Now()
	node, err := ParseFormula("=IFNA(#N/A, \"none\")")
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("ParseFormula failed: %v", err)
	}
	if duration > 100*time.Millisecond {
		t.Errorf("lexing took too long: %v (possible spin)", duration)
	}
	fn, ok := node.(FuncNode)
	if !ok || fn.Name != "IFNA" {
		t.Errorf("expected FuncNode IFNA, got %T: %v", node, node)
	}
}

func TestNestingBound(t *testing.T) {
	// 101-deep parens should trigger ErrSyntax
	deep := strings.Repeat("(", 101) + "1" + strings.Repeat(")", 101)
	_, err := ParseFormula("=" + deep)
	if err == nil || err.Error() != ErrSyntax {
		t.Fatalf("expected ErrSyntax on 101-deep nesting, got %v", err)
	}

	// 50-deep parens should succeed
	shallow := strings.Repeat("(", 50) + "1" + strings.Repeat(")", 50)
	_, err = ParseFormula("=" + shallow)
	if err != nil {
		t.Fatalf("expected 50-deep nesting to succeed, got %v", err)
	}
}

func TestRefsExtraction(t *testing.T) {
	node, err := ParseFormula("=SUM(A1:A3) + B1 + $C$5")
	if err != nil {
		t.Fatalf("ParseFormula failed: %v", err)
	}
	refs := Refs(node)
	expected := []string{"A1", "A2", "A3", "B1", "C5"}
	if len(refs) != len(expected) {
		t.Fatalf("expected %d refs, got %d: %v", len(expected), len(refs), refs)
	}
	for i, r := range refs {
		if r.Format() != expected[i] {
			t.Errorf("ref[%d] = %s, want %s", i, r.Format(), expected[i])
		}
	}
}

func TestHasVolatile(t *testing.T) {
	node, err := ParseFormula("=RAND() + A1")
	if err != nil {
		t.Fatalf("ParseFormula failed: %v", err)
	}
	volatileFns := map[string]bool{"RAND": true, "NOW": true}
	if !HasVolatile(node, volatileFns) {
		t.Errorf("expected HasVolatile to be true for RAND()")
	}

	node2, _ := ParseFormula("=SUM(A1:B2)")
	if HasVolatile(node2, volatileFns) {
		t.Errorf("expected HasVolatile to be false for SUM")
	}
}
