package sheet

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

type TokenType int

const (
	tokEOF TokenType = iota
	tokNumber
	tokString
	tokBool
	tokError
	tokRef
	tokArea
	tokFuncName
	tokName
	tokComma
	tokLParen
	tokRParen
	tokColon
	tokPlus
	tokMinus
	tokStar
	tokSlash
	tokCaret
	tokAmpersand
	tokPercent
	tokEq
	tokNe
	tokLt
	tokLe
	tokGt
	tokGe
)

type Token struct {
	Type TokenType
	Val  string
	Num  float64
	Bool bool
	Err  string
}

func (t Token) String() string {
	return fmt.Sprintf("Tok(%d, %q)", t.Type, t.Val)
}

type Lexer struct {
	input []rune
	pos   int
	len   int
}

func NewLexer(input string) *Lexer {
	r := []rune(strings.TrimSpace(input))
	if len(r) > 0 && r[0] == '=' {
		r = r[1:]
	}
	return &Lexer{
		input: r,
		pos:   0,
		len:   len(r),
	}
}

func (l *Lexer) peek() rune {
	if l.pos >= l.len {
		return 0
	}
	return l.input[l.pos]
}

func (l *Lexer) next() rune {
	if l.pos >= l.len {
		return 0
	}
	ch := l.input[l.pos]
	l.pos++
	return ch
}

func (l *Lexer) skipWhitespace() {
	for l.pos < l.len && unicode.IsSpace(l.input[l.pos]) {
		l.pos++
	}
}

func (l *Lexer) NextToken() (Token, error) {
	l.skipWhitespace()
	if l.pos >= l.len {
		return Token{Type: tokEOF}, nil
	}

	startPos := l.pos
	ch := l.next()

	// Lexer guard: ensure pos increases or error is returned
	defer func() {
		if l.pos <= startPos {
			// Panic or fail-safe to guarantee no infinite loop
			l.pos = startPos + 1
		}
	}()

	switch ch {
	case ',':
		return Token{Type: tokComma, Val: ","}, nil
	case '(':
		return Token{Type: tokLParen, Val: "("}, nil
	case ')':
		return Token{Type: tokRParen, Val: ")"}, nil
	case ':':
		return Token{Type: tokColon, Val: ":"}, nil
	case '+':
		return Token{Type: tokPlus, Val: "+"}, nil
	case '-':
		return Token{Type: tokMinus, Val: "-"}, nil
	case '*':
		return Token{Type: tokStar, Val: "*"}, nil
	case '/':
		return Token{Type: tokSlash, Val: "/"}, nil
	case '^':
		return Token{Type: tokCaret, Val: "^"}, nil
	case '&':
		return Token{Type: tokAmpersand, Val: "&"}, nil
	case '%':
		return Token{Type: tokPercent, Val: "%"}, nil
	case '=':
		return Token{Type: tokEq, Val: "="}, nil
	case '<':
		if l.peek() == '>' {
			l.next()
			return Token{Type: tokNe, Val: "<>"}, nil
		}
		if l.peek() == '=' {
			l.next()
			return Token{Type: tokLe, Val: "<="}, nil
		}
		return Token{Type: tokLt, Val: "<"}, nil
	case '>':
		if l.peek() == '=' {
			l.next()
			return Token{Type: tokGe, Val: ">="}, nil
		}
		return Token{Type: tokGt, Val: ">"}, nil
	case '"':
		// String literal with "" escape
		var sb strings.Builder
		for {
			if l.pos >= l.len {
				return Token{}, errors.New(ErrSyntax)
			}
			curr := l.next()
			if curr == '"' {
				if l.peek() == '"' {
					l.next()
					sb.WriteRune('"')
				} else {
					break
				}
			} else {
				sb.WriteRune(curr)
			}
		}
		return Token{Type: tokString, Val: sb.String()}, nil
	case '#':
		// Error literal: #DIV/0!, #VALUE!, #REF!, #NAME?, #N/A, #CYCLE!, #ERROR!
		var sb strings.Builder
		sb.WriteRune('#')
		for l.pos < l.len {
			c := l.peek()
			if unicode.IsLetter(c) || unicode.IsDigit(c) || c == '/' || c == '0' || c == '!' || c == '?' {
				sb.WriteRune(l.next())
				if c == '!' || c == '?' {
					break
				}
			} else {
				break
			}
		}
		errStr := strings.ToUpper(sb.String())
		switch errStr {
		case ErrDiv0, ErrValue, ErrRef, ErrName, ErrNA, ErrCycle, ErrSyntax:
			return Token{Type: tokError, Val: errStr, Err: errStr}, nil
		default:
			return Token{Type: tokError, Val: errStr, Err: ErrSyntax}, nil
		}
	}

	// Numbers
	if unicode.IsDigit(ch) || (ch == '.' && unicode.IsDigit(l.peek())) {
		var sb strings.Builder
		sb.WriteRune(ch)
		hasDot := ch == '.'
		hasExp := false

		for l.pos < l.len {
			c := l.peek()
			if unicode.IsDigit(c) {
				sb.WriteRune(l.next())
			} else if c == '.' && !hasDot && !hasExp {
				hasDot = true
				sb.WriteRune(l.next())
			} else if (c == 'e' || c == 'E') && !hasExp {
				hasExp = true
				sb.WriteRune(l.next())
				if l.peek() == '+' || l.peek() == '-' {
					sb.WriteRune(l.next())
				}
			} else {
				break
			}
		}
		numStr := sb.String()
		n, err := strconv.ParseFloat(numStr, 64)
		if err != nil {
			return Token{}, errors.New(ErrSyntax)
		}
		return Token{Type: tokNumber, Val: numStr, Num: n}, nil
	}

	// Identifiers, Functions, Cell References, or Boolean literals
	if unicode.IsLetter(ch) || ch == '_' || ch == '$' {
		var sb strings.Builder
		sb.WriteRune(ch)
		for l.pos < l.len {
			c := l.peek()
			if unicode.IsLetter(c) || unicode.IsDigit(c) || c == '_' || c == '$' {
				sb.WriteRune(l.next())
			} else {
				break
			}
		}
		word := sb.String()
		upper := strings.ToUpper(word)

		// Check if followed by ':' for an area reference like A1:B5
		l.skipWhitespace()
		if l.peek() == ':' {
			// Lookahead to see if next token is also a reference
			savedPos := l.pos
			l.next() // consume ':'
			l.skipWhitespace()
			if l.pos < l.len && (unicode.IsLetter(l.peek()) || l.peek() == '$') {
				var sb2 strings.Builder
				for l.pos < l.len {
					c := l.peek()
					if unicode.IsLetter(c) || unicode.IsDigit(c) || c == '$' {
						sb2.WriteRune(l.next())
					} else {
						break
					}
				}
				secondWord := sb2.String()
				areaStr := word + ":" + secondWord
				if area, err := ParseArea(areaStr); err == nil {
					return Token{Type: tokArea, Val: area.String()}, nil
				}
			}
			// Not an area, revert
			l.pos = savedPos
		}

		// Check if function call
		if l.peek() == '(' {
			return Token{Type: tokFuncName, Val: upper}, nil
		}

		// Booleans
		if upper == "TRUE" {
			return Token{Type: tokBool, Val: "TRUE", Bool: true}, nil
		}
		if upper == "FALSE" {
			return Token{Type: tokBool, Val: "FALSE", Bool: false}, nil
		}

		// Check if valid Cell Reference
		if addr, err := Parse(word); err == nil {
			return Token{Type: tokRef, Val: addr.Format()}, nil
		}

		return Token{Type: tokName, Val: upper}, nil
	}

	return Token{}, errors.New(ErrSyntax)
}

// AST Nodes
type Node interface {
	isNode()
	String() string
}

type LiteralNode struct {
	Val Value
}

func (LiteralNode) isNode() {}
func (n LiteralNode) String() string {
	return n.Val.String()
}

type RefNode struct {
	Addr CellAddr
	Raw  string
}

func (RefNode) isNode() {}
func (n RefNode) String() string {
	return n.Addr.Format()
}

type AreaNode struct {
	Area Area
	Raw  string
}

func (AreaNode) isNode() {}
func (n AreaNode) String() string {
	return n.Area.String()
}

type UnaryNode struct {
	Op    TokenType
	Right Node
}

func (UnaryNode) isNode() {}
func (n UnaryNode) String() string {
	opStr := "+"
	if n.Op == tokMinus {
		opStr = "-"
	}
	return fmt.Sprintf("(%s%s)", opStr, n.Right.String())
}

type PostfixNode struct {
	Op   TokenType
	Left Node
}

func (PostfixNode) isNode() {}
func (n PostfixNode) String() string {
	return fmt.Sprintf("(%s%%)", n.Left.String())
}

type BinaryNode struct {
	Op    TokenType
	Left  Node
	Right Node
}

func (BinaryNode) isNode() {}
func (n BinaryNode) String() string {
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
	return fmt.Sprintf("(%s %s %s)", n.Left.String(), opStr, n.Right.String())
}

type FuncNode struct {
	Name string
	Args []Node
}

func (FuncNode) isNode() {}
func (n FuncNode) String() string {
	var argStrs []string
	for _, a := range n.Args {
		argStrs = append(argStrs, a.String())
	}
	return fmt.Sprintf("%s(%s)", n.Name, strings.Join(argStrs, ", "))
}

// Parser
type Parser struct {
	tokens  []Token
	pos     int
	nesting int
}

const MaxNesting = 100

func ParseFormula(input string) (Node, error) {
	lexer := NewLexer(input)
	var tokens []Token
	for {
		tok, err := lexer.NextToken()
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, tok)
		if tok.Type == tokEOF {
			break
		}
	}
	p := &Parser{tokens: tokens, pos: 0, nesting: 0}
	node, err := p.parseExpression()
	if err != nil {
		return nil, err
	}
	if p.current().Type != tokEOF {
		return nil, errors.New(ErrSyntax)
	}
	return node, nil
}

func (p *Parser) current() Token {
	if p.pos >= len(p.tokens) {
		return Token{Type: tokEOF}
	}
	return p.tokens[p.pos]
}

func (p *Parser) advance() Token {
	tok := p.current()
	if p.pos < len(p.tokens) {
		p.pos++
	}
	return tok
}

func (p *Parser) check(t TokenType) bool {
	return p.current().Type == t
}

func (p *Parser) match(t TokenType) bool {
	if p.check(t) {
		p.advance()
		return true
	}
	return false
}

func (p *Parser) parseExpression() (Node, error) {
	p.nesting++
	if p.nesting > MaxNesting {
		return nil, errors.New(ErrSyntax)
	}
	defer func() { p.nesting-- }()

	return p.parseComparison()
}

// Comparison: = <> < <= > >=
func (p *Parser) parseComparison() (Node, error) {
	node, err := p.parseConcat()
	if err != nil {
		return nil, err
	}

	for p.check(tokEq) || p.check(tokNe) || p.check(tokLt) || p.check(tokLe) || p.check(tokGt) || p.check(tokGe) {
		op := p.advance().Type
		right, err := p.parseConcat()
		if err != nil {
			return nil, err
		}
		node = BinaryNode{Op: op, Left: node, Right: right}
	}
	return node, nil
}

// Concat: &
func (p *Parser) parseConcat() (Node, error) {
	node, err := p.parseAdditive()
	if err != nil {
		return nil, err
	}

	for p.match(tokAmpersand) {
		right, err := p.parseAdditive()
		if err != nil {
			return nil, err
		}
		node = BinaryNode{Op: tokAmpersand, Left: node, Right: right}
	}
	return node, nil
}

// Additive: + -
func (p *Parser) parseAdditive() (Node, error) {
	node, err := p.parseMultiplicative()
	if err != nil {
		return nil, err
	}

	for p.check(tokPlus) || p.check(tokMinus) {
		op := p.advance().Type
		right, err := p.parseMultiplicative()
		if err != nil {
			return nil, err
		}
		node = BinaryNode{Op: op, Left: node, Right: right}
	}
	return node, nil
}

// Multiplicative: * /
func (p *Parser) parseMultiplicative() (Node, error) {
	node, err := p.parsePower()
	if err != nil {
		return nil, err
	}

	for p.check(tokStar) || p.check(tokSlash) {
		op := p.advance().Type
		right, err := p.parsePower()
		if err != nil {
			return nil, err
		}
		node = BinaryNode{Op: op, Left: node, Right: right}
	}
	return node, nil
}

// Power: ^ (in Excel, left-associative, and unary binds tighter than ^!)
func (p *Parser) parsePower() (Node, error) {
	node, err := p.parseUnary()
	if err != nil {
		return nil, err
	}

	for p.match(tokCaret) {
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		node = BinaryNode{Op: tokCaret, Left: node, Right: right}
	}
	return node, nil
}

// Unary: + - (binds tighter than ^ in Excel)
func (p *Parser) parseUnary() (Node, error) {
	if p.check(tokPlus) || p.check(tokMinus) {
		op := p.advance().Type
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return UnaryNode{Op: op, Right: right}, nil
	}
	return p.parsePostfix()
}

// Postfix: %
func (p *Parser) parsePostfix() (Node, error) {
	node, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}

	for p.match(tokPercent) {
		node = PostfixNode{Op: tokPercent, Left: node}
	}
	return node, nil
}

// Primary
func (p *Parser) parsePrimary() (Node, error) {
	tok := p.current()

	switch tok.Type {
	case tokNumber:
		p.advance()
		return LiteralNode{Val: NewNumber(tok.Num)}, nil
	case tokString:
		p.advance()
		return LiteralNode{Val: NewText(tok.Val)}, nil
	case tokBool:
		p.advance()
		return LiteralNode{Val: NewBool(tok.Bool)}, nil
	case tokError:
		p.advance()
		return LiteralNode{Val: NewError(tok.Err)}, nil
	case tokRef:
		p.advance()
		addr, _ := Parse(tok.Val)
		return RefNode{Addr: addr, Raw: tok.Val}, nil
	case tokArea:
		p.advance()
		area, _ := ParseArea(tok.Val)
		return AreaNode{Area: area, Raw: tok.Val}, nil
	case tokFuncName:
		fnName := tok.Val
		p.advance() // consume func name
		if !p.match(tokLParen) {
			return nil, errors.New(ErrSyntax)
		}
		var args []Node
		if !p.check(tokRParen) {
			for {
				arg, err := p.parseExpression()
				if err != nil {
					return nil, err
				}
				args = append(args, arg)
				if p.match(tokComma) {
					continue
				}
				break
			}
		}
		if !p.match(tokRParen) {
			return nil, errors.New(ErrSyntax)
		}
		return FuncNode{Name: fnName, Args: args}, nil
	case tokLParen:
		p.advance()
		expr, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		if !p.match(tokRParen) {
			return nil, errors.New(ErrSyntax)
		}
		return expr, nil
	default:
		return nil, errors.New(ErrSyntax)
	}
}

// Refs walks the AST and returns all referenced cells in order
func Refs(node Node) []CellAddr {
	var result []CellAddr
	seen := make(map[CellAddr]bool)

	var walk func(n Node)
	walk = func(n Node) {
		if n == nil {
			return
		}
		switch v := n.(type) {
		case RefNode:
			if !seen[v.Addr] {
				seen[v.Addr] = true
				result = append(result, v.Addr)
			}
		case AreaNode:
			for _, cell := range v.Area.Cells() {
				if !seen[cell] {
					seen[cell] = true
					result = append(result, cell)
				}
			}
		case UnaryNode:
			walk(v.Right)
		case PostfixNode:
			walk(v.Left)
		case BinaryNode:
			walk(v.Left)
			walk(v.Right)
		case FuncNode:
			for _, arg := range v.Args {
				walk(arg)
			}
		}
	}

	walk(node)
	return result
}

// HasVolatile checks if the AST contains any volatile functions
func HasVolatile(node Node, volatileFns map[string]bool) bool {
	if node == nil {
		return false
	}
	switch v := node.(type) {
	case FuncNode:
		if volatileFns[strings.ToUpper(v.Name)] {
			return true
		}
		for _, arg := range v.Args {
			if HasVolatile(arg, volatileFns) {
				return true
			}
		}
	case UnaryNode:
		return HasVolatile(v.Right, volatileFns)
	case PostfixNode:
		return HasVolatile(v.Left, volatileFns)
	case BinaryNode:
		return HasVolatile(v.Left, volatileFns) || HasVolatile(v.Right, volatileFns)
	}
	return false
}
