package sheet

import (
	"testing"
)

func TestRewriteFormula(t *testing.T) {
	tests := []struct {
		name     string
		formula  string
		axis     string
		index    int
		count    int
		isDelete bool
		expected string
	}{
		{
			name:     "Insert row shifts references at or below index",
			formula:  "=A1+A2",
			axis:     "row",
			index:    1,
			count:    1,
			isDelete: false,
			expected: "=A1+A3",
		},
		{
			name:     "Delete row turns deleted cell reference into #REF!",
			formula:  "=A2",
			axis:     "row",
			index:    1,
			count:    1,
			isDelete: true,
			expected: "=#REF!",
		},
		{
			name:     "Delete row shifts references below deleted range",
			formula:  "=A3",
			axis:     "row",
			index:    1,
			count:    1,
			isDelete: true,
			expected: "=A2",
		},
		{
			name:     "Insert column shifts column references",
			formula:  "=A1+B1",
			axis:     "col",
			index:    1,
			count:    1,
			isDelete: false,
			expected: "=A1+C1",
		},
		{
			name:     "Delete column turns deleted cell into #REF!",
			formula:  "=B1",
			axis:     "col",
			index:    1,
			count:    1,
			isDelete: true,
			expected: "=#REF!",
		},
		{
			name:     "Insert row expands area",
			formula:  "=SUM(A1:A5)",
			axis:     "row",
			index:    2,
			count:    1,
			isDelete: false,
			expected: "=SUM(A1:A6)",
		},
		{
			name:     "Delete row shrinks area",
			formula:  "=SUM(A1:A5)",
			axis:     "row",
			index:    2,
			count:    1,
			isDelete: true,
			expected: "=SUM(A1:A4)",
		},
		{
			name:     "Delete rows destroys area completely into #REF!",
			formula:  "=SUM(A2:A3)",
			axis:     "row",
			index:    1,
			count:    3,
			isDelete: true,
			expected: "=SUM(#REF!)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RewriteFormula(tt.formula, tt.axis, tt.index, tt.count, tt.isDelete)
			if got != tt.expected {
				t.Errorf("RewriteFormula(%q): expected %q, got %q", tt.formula, tt.expected, got)
			}
		})
	}
}
