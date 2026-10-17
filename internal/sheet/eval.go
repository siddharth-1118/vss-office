package sheet

import (
	"math"
	"strings"
	"time"
)

type EvalContext interface {
	Resolve(addr CellAddr) Value
	ResolveArea(area Area) []Value
	Now() time.Time
	Rand() float64
	IsVolatile(name string) bool
}

type DefaultEvalContext struct {
	Cells map[CellAddr]Value
	Clock time.Time
	RNG   float64
}

func (c *DefaultEvalContext) Resolve(addr CellAddr) Value {
	if v, ok := c.Cells[addr]; ok {
		return v
	}
	return NewBlank()
}

func (c *DefaultEvalContext) ResolveArea(area Area) []Value {
	var vals []Value
	for _, cell := range area.Cells() {
		vals = append(vals, c.Resolve(cell))
	}
	return vals
}

func (c *DefaultEvalContext) Now() time.Time {
	if !c.Clock.IsZero() {
		return c.Clock
	}
	return time.Now()
}

func (c *DefaultEvalContext) Rand() float64 {
	return c.RNG
}

func (c *DefaultEvalContext) IsVolatile(name string) bool {
	upper := strings.ToUpper(name)
	return upper == "RAND" || upper == "RANDBETWEEN" || upper == "NOW" || upper == "TODAY"
}

func Evaluate(node Node, ctx EvalContext) Value {
	if node == nil {
		return NewBlank()
	}

	switch n := node.(type) {
	case LiteralNode:
		return n.Val

	case RefNode:
		return ctx.Resolve(n.Addr)

	case AreaNode:
		// Range used in scalar context returns first cell or #VALUE!
		cells := n.Area.Cells()
		if len(cells) > 0 {
			return ctx.Resolve(cells[0])
		}
		return NewError(ErrValue)

	case UnaryNode:
		rVal := Evaluate(n.Right, ctx)
		if rVal.IsError() {
			return rVal
		}
		num, errVal := rVal.ToNumber()
		if errVal.IsError() {
			return errVal
		}
		if n.Op == tokMinus {
			return NewNumber(-num)
		}
		return NewNumber(num)

	case PostfixNode:
		lVal := Evaluate(n.Left, ctx)
		if lVal.IsError() {
			return lVal
		}
		num, errVal := lVal.ToNumber()
		if errVal.IsError() {
			return errVal
		}
		return NewNumber(num * 0.01)

	case BinaryNode:
		lVal := Evaluate(n.Left, ctx)
		if lVal.IsError() {
			return lVal
		}
		rVal := Evaluate(n.Right, ctx)
		if rVal.IsError() {
			return rVal
		}

		switch n.Op {
		case tokPlus, tokMinus, tokStar, tokSlash, tokCaret:
			lNum, errVal := lVal.ToNumber()
			if errVal.IsError() {
				return errVal
			}
			rNum, errVal := rVal.ToNumber()
			if errVal.IsError() {
				return errVal
			}

			switch n.Op {
			case tokPlus:
				return NewNumber(lNum + rNum)
			case tokMinus:
				return NewNumber(lNum - rNum)
			case tokStar:
				return NewNumber(lNum * rNum)
			case tokSlash:
				if rNum == 0 {
					return NewError(ErrDiv0)
				}
				return NewNumber(lNum / rNum)
			case tokCaret:
				res := math.Pow(lNum, rNum)
				if math.IsNaN(res) || math.IsInf(res, 0) {
					return NewError(ErrValue)
				}
				return NewNumber(res)
			}

		case tokAmpersand:
			return NewText(lVal.ToDisplayString() + rVal.ToDisplayString())

		case tokEq, tokNe, tokLt, tokLe, tokGt, tokGe:
			cmp, errVal := CompareValues(lVal, rVal)
			if errVal.IsError() {
				return errVal
			}
			switch n.Op {
			case tokEq:
				return NewBool(cmp == 0)
			case tokNe:
				return NewBool(cmp != 0)
			case tokLt:
				return NewBool(cmp < 0)
			case tokLe:
				return NewBool(cmp <= 0)
			case tokGt:
				return NewBool(cmp > 0)
			case tokGe:
				return NewBool(cmp >= 0)
			}
		}
		return NewError(ErrSyntax)

	case FuncNode:
		return dispatchFunction(n, ctx)

	default:
		return NewError(ErrSyntax)
	}
}

func dispatchFunction(fnNode FuncNode, ctx EvalContext) Value {
	name := strings.ToUpper(fnNode.Name)
	fnDef, ok := FunctionTable[name]
	if !ok {
		return NewError(ErrName)
	}

	// Arity validation
	if fnDef.MinArgs >= 0 && len(fnNode.Args) < fnDef.MinArgs {
		return NewError(ErrValue)
	}
	if fnDef.MaxArgs >= 0 && len(fnNode.Args) > fnDef.MaxArgs {
		return NewError(ErrValue)
	}

	// Handle Lazy Functions (IF, IFERROR, IFNA, AND, OR)
	if fnDef.Lazy && fnDef.LazyFn != nil {
		return fnDef.LazyFn(fnNode.Args, ctx)
	}

	// Non-lazy functions: evaluate arguments
	var evaledArgs []Value
	for _, argNode := range fnNode.Args {
		if areaNode, isArea := argNode.(AreaNode); isArea {
			vals := ctx.ResolveArea(areaNode.Area)
			evaledArgs = append(evaledArgs, vals...)
		} else {
			v := Evaluate(argNode, ctx)
			evaledArgs = append(evaledArgs, v)
		}
	}

	return fnDef.Fn(evaledArgs, ctx)
}
