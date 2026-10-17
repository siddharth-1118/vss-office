package sheet

import (
	"strings"
)

// FormatNode formats an AST Node back into Excel formula expression text.
func FormatNode(node Node) string {
	if node == nil {
		return ""
	}
	switch n := node.(type) {
	case LiteralNode:
		switch n.Val.Kind {
		case KindNumber:
			return FormatGeneralNumber(n.Val.Num)
		case KindText:
			return `"` + strings.ReplaceAll(n.Val.Str, `"`, `""`) + `"`
		case KindBool:
			if n.Val.Boolean {
				return "TRUE"
			}
			return "FALSE"
		case KindError:
			return n.Val.Err
		default:
			return ""
		}
	case RefNode:
		return n.Addr.Format()
	case AreaNode:
		return n.Area.String()
	case UnaryNode:
		opStr := "+"
		if n.Op == tokMinus {
			opStr = "-"
		}
		return opStr + FormatNode(n.Right)
	case PostfixNode:
		return FormatNode(n.Left) + "%"
	case BinaryNode:
		var opStr string
		switch n.Op {
		case tokPlus:
			opStr = "+"
		case tokMinus:
			opStr = "-"
		case tokStar:
			opStr = "*"
		case tokSlash:
			opStr = "/"
		case tokCaret:
			opStr = "^"
		case tokAmpersand:
			opStr = "&"
		case tokEq:
			opStr = "="
		case tokNe:
			opStr = "<>"
		case tokLt:
			opStr = "<"
		case tokLe:
			opStr = "<="
		case tokGt:
			opStr = ">"
		case tokGe:
			opStr = ">="
		}
		return FormatNode(n.Left) + opStr + FormatNode(n.Right)
	case FuncNode:
		var args []string
		for _, a := range n.Args {
			args = append(args, FormatNode(a))
		}
		return n.Name + "(" + strings.Join(args, ", ") + ")"
	default:
		return ""
	}
}

// RewriteFormula updates cell references and areas in a formula when rows/cols are inserted or deleted.
func RewriteFormula(formula string, axis string, index, count int, isDelete bool) string {
	if formula == "" {
		return ""
	}
	node, err := ParseFormula(formula)
	if err != nil {
		return formula
	}

	rewritten := rewriteNode(node, axis, index, count, isDelete)
	return "=" + FormatNode(rewritten)
}

func rewriteNode(node Node, axis string, index, count int, isDelete bool) Node {
	if node == nil {
		return nil
	}
	switch n := node.(type) {
	case RefNode:
		addr := n.Addr
		if axis == "row" {
			if isDelete {
				if addr.Row >= index && addr.Row < index+count {
					return LiteralNode{Val: NewError(ErrRef)}
				}
				if addr.Row >= index+count {
					addr.Row -= count
				}
			} else {
				if addr.Row >= index {
					addr.Row += count
				}
			}
		} else if axis == "col" {
			if isDelete {
				if addr.Col >= index && addr.Col < index+count {
					return LiteralNode{Val: NewError(ErrRef)}
				}
				if addr.Col >= index+count {
					addr.Col -= count
				}
			} else {
				if addr.Col >= index {
					addr.Col += count
				}
			}
		}
		return RefNode{Addr: addr}

	case AreaNode:
		start := n.Area.TopLeft
		end := n.Area.BottomRight
		if axis == "row" {
			if isDelete {
				if start.Row >= index && end.Row < index+count {
					return LiteralNode{Val: NewError(ErrRef)}
				}
				if start.Row >= index+count {
					start.Row -= count
				} else if start.Row >= index {
					start.Row = index
				}
				if end.Row >= index+count {
					end.Row -= count
				} else if end.Row >= index {
					end.Row = index - 1
				}
			} else {
				if start.Row >= index {
					start.Row += count
				}
				if end.Row >= index {
					end.Row += count
				}
			}
		} else if axis == "col" {
			if isDelete {
				if start.Col >= index && end.Col < index+count {
					return LiteralNode{Val: NewError(ErrRef)}
				}
				if start.Col >= index+count {
					start.Col -= count
				} else if start.Col >= index {
					start.Col = index
				}
				if end.Col >= index+count {
					end.Col -= count
				} else if end.Col >= index {
					end.Col = index - 1
				}
			} else {
				if start.Col >= index {
					start.Col += count
				}
				if end.Col >= index {
					end.Col += count
				}
			}
		}
		if start.Row > end.Row || start.Col > end.Col {
			return LiteralNode{Val: NewError(ErrRef)}
		}
		return AreaNode{Area: NewArea(start, end)}

	case UnaryNode:
		return UnaryNode{Op: n.Op, Right: rewriteNode(n.Right, axis, index, count, isDelete)}

	case PostfixNode:
		return PostfixNode{Op: n.Op, Left: rewriteNode(n.Left, axis, index, count, isDelete)}

	case BinaryNode:
		return BinaryNode{
			Op:    n.Op,
			Left:  rewriteNode(n.Left, axis, index, count, isDelete),
			Right: rewriteNode(n.Right, axis, index, count, isDelete),
		}

	case FuncNode:
		var newArgs []Node
		for _, a := range n.Args {
			newArgs = append(newArgs, rewriteNode(a, axis, index, count, isDelete))
		}
		return FuncNode{Name: n.Name, Args: newArgs}

	default:
		return n
	}
}
