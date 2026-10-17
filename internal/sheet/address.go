package sheet

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

const (
	MaxRow = 1048576
	MaxCol = 16384 // XFD
)

type CellAddr struct {
	Row int // 0-indexed (0 to MaxRow-1)
	Col int // 0-indexed (0 to MaxCol-1)
}

func ColToString(col int) string {
	if col < 0 {
		return ""
	}
	var sb strings.Builder
	col++
	for col > 0 {
		rem := (col - 1) % 26
		sb.WriteByte(byte('A' + rem))
		col = (col - 1) / 26
	}
	bytes := []byte(sb.String())
	for i, j := 0, len(bytes)-1; i < j; i, j = i+1, j-1 {
		bytes[i], bytes[j] = bytes[j], bytes[i]
	}
	return string(bytes)
}

func StringToCol(s string) (int, error) {
	s = strings.ToUpper(s)
	if len(s) == 0 {
		return 0, fmt.Errorf("empty column string")
	}
	col := 0
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch < 'A' || ch > 'Z' {
			return 0, fmt.Errorf("invalid column character: %c", ch)
		}
		col = col*26 + int(ch-'A'+1)
	}
	col--
	if col < 0 || col >= MaxCol {
		return 0, fmt.Errorf("column out of bounds: %s", s)
	}
	return col, nil
}

func Parse(a1 string) (CellAddr, error) {
	s := strings.TrimSpace(a1)
	if len(s) == 0 {
		return CellAddr{}, fmt.Errorf("empty cell address")
	}

	// Strip sheet prefix if any (e.g. Sheet1!A1)
	if idx := strings.LastIndex(s, "!"); idx != -1 {
		s = s[idx+1:]
	}

	var colPart strings.Builder
	var rowPart strings.Builder
	inRow := false

	for _, r := range s {
		if r == '$' {
			continue // ignore absolute marker
		}
		if unicode.IsLetter(r) {
			if inRow {
				return CellAddr{}, fmt.Errorf("invalid cell address: %s", a1)
			}
			colPart.WriteRune(unicode.ToUpper(r))
		} else if unicode.IsDigit(r) {
			inRow = true
			rowPart.WriteRune(r)
		} else {
			return CellAddr{}, fmt.Errorf("invalid character in address: %c", r)
		}
	}

	if colPart.Len() == 0 || rowPart.Len() == 0 {
		return CellAddr{}, fmt.Errorf("invalid cell address: %s", a1)
	}

	col, err := StringToCol(colPart.String())
	if err != nil {
		return CellAddr{}, err
	}

	rowNum, err := strconv.Atoi(rowPart.String())
	if err != nil || rowNum < 1 || rowNum > MaxRow {
		return CellAddr{}, fmt.Errorf("row out of bounds: %s", rowPart.String())
	}
	row := rowNum - 1

	return CellAddr{Row: row, Col: col}, nil
}

func MustParse(a1 string) CellAddr {
	addr, err := Parse(a1)
	if err != nil {
		panic(err)
	}
	return addr
}

func (a CellAddr) Format() string {
	return ColToString(a.Col) + strconv.Itoa(a.Row+1)
}

func (a CellAddr) String() string {
	return a.Format()
}

type Area struct {
	TopLeft     CellAddr
	BottomRight CellAddr
}

func NewArea(start, end CellAddr) Area {
	minRow := start.Row
	maxRow := end.Row
	if minRow > maxRow {
		minRow, maxRow = maxRow, minRow
	}
	minCol := start.Col
	maxCol := end.Col
	if minCol > maxCol {
		minCol, maxCol = maxCol, minCol
	}
	return Area{
		TopLeft:     CellAddr{Row: minRow, Col: minCol},
		BottomRight: CellAddr{Row: maxRow, Col: maxCol},
	}
}

func ParseArea(s string) (Area, error) {
	s = strings.TrimSpace(s)
	parts := strings.Split(s, ":")
	if len(parts) == 1 {
		addr, err := Parse(parts[0])
		if err != nil {
			return Area{}, err
		}
		return Area{TopLeft: addr, BottomRight: addr}, nil
	}
	if len(parts) == 2 {
		start, err := Parse(parts[0])
		if err != nil {
			return Area{}, err
		}
		end, err := Parse(parts[1])
		if err != nil {
			return Area{}, err
		}
		return NewArea(start, end), nil
	}
	return Area{}, fmt.Errorf("invalid area syntax: %s", s)
}

func (area Area) Cells() []CellAddr {
	var cells []CellAddr
	for r := area.TopLeft.Row; r <= area.BottomRight.Row; r++ {
		for c := area.TopLeft.Col; c <= area.BottomRight.Col; c++ {
			cells = append(cells, CellAddr{Row: r, Col: c})
		}
	}
	return cells
}

func (area Area) Contains(c CellAddr) bool {
	return c.Row >= area.TopLeft.Row && c.Row <= area.BottomRight.Row &&
		c.Col >= area.TopLeft.Col && c.Col <= area.BottomRight.Col
}

func (area Area) String() string {
	if area.TopLeft == area.BottomRight {
		return area.TopLeft.Format()
	}
	return area.TopLeft.Format() + ":" + area.BottomRight.Format()
}
