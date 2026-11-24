package fasedscript

import (
	"errors"
	"fmt"
	"strconv"
)

type stmt interface {
	stmtNode()
}

type expr interface {
	exprNode()
}

type varDeclStmt struct {
	name    string
	isConst bool
	init    expr
	line    int
}

func (*varDeclStmt) stmtNode() {}

type assignStmt struct {
	name string
	val  expr
	line int
}

func (*assignStmt) stmtNode() {}

type blockStmt struct {
	stmts []stmt
}

func (*blockStmt) stmtNode() {}

type ifStmt struct {
	cond expr
	then stmt
	els  stmt
}

func (*ifStmt) stmtNode() {}

type exprStmt struct {
	e expr
}

func (*exprStmt) stmtNode() {}

type literalExpr struct {
	kind string // "float", "bool", "string", "null"
	f    float64
	b    bool
	s    string
}

func (*literalExpr) exprNode() {}

type identExpr struct {
	name string
	line int
}

func (*identExpr) exprNode() {}

type unaryExpr struct {
	op  tokenKind
	rhs expr
}

func (*unaryExpr) exprNode() {}

type binaryExpr struct {
	op  tokenKind
	lhs expr
	rhs expr
}

func (*binaryExpr) exprNode() {}

type ternaryExpr struct {
	cond expr
	t    expr
	f    expr
}

func (*ternaryExpr) exprNode() {}

type callExpr struct {
	callee expr
	args   []expr
	line   int
}

func (*callExpr) exprNode() {}

type memberExpr struct {
	obj  expr
	prop string
	line int
}

func (*memberExpr) exprNode() {}

type objectField struct {
	key string
	val expr
}

type objectExpr struct {
	fields []objectField
}

func (*objectExpr) exprNode() {}

type arrayExpr struct {
	elems []expr
}

func (*arrayExpr) exprNode() {}

// Program represents a validated, sandbox-checked immutable FasedScript AST.
type Program struct {
	Source    string
	Inputs    []InputSpec
	stmts     []stmt
	NodeCount int
}

type parser struct {
	tokens []token
	pos    int
	nodes  int
	depth  int
	inputs []InputSpec
	seenIn map[string]bool
}

// Compile lexes and parses FasedScript source into an immutable sandboxed Program.
func Compile(source string) (*Program, error) {
	tokens, err := tokenize(source)
	if err != nil {
		return nil, err
	}
	p := &parser{
		tokens: tokens,
		seenIn: make(map[string]bool),
	}
	var stmts []stmt
	for !p.at(tokEOF) {
		for p.match(tokSemi) {
		}
		if p.at(tokEOF) {
			break
		}
		st, sErr := p.parseStmt()
		if sErr != nil {
			return nil, sErr
		}
		stmts = append(stmts, st)
	}
	if len(stmts) == 0 {
		return nil, errors.New("empty FasedScript program")
	}
	return &Program{
		Source:    source,
		Inputs:    p.inputs,
		stmts:     stmts,
		NodeCount: p.nodes,
	}, nil
}

func (p *parser) enterNode() error {
	p.nodes++
	if p.nodes > MaxASTNodes {
		return fmt.Errorf("script AST exceeds %d nodes", MaxASTNodes)
	}
	return nil
}

func (p *parser) enterDepth() error {
	p.depth++
	if p.depth > MaxRecursionDepth {
		return fmt.Errorf("nesting depth exceeds %d", MaxRecursionDepth)
	}
	return nil
}

func (p *parser) leaveDepth() {
	if p.depth > 0 {
		p.depth--
	}
}

func (p *parser) peek() token {
	if p.pos >= len(p.tokens) {
		return token{kind: tokEOF}
	}
	return p.tokens[p.pos]
}

func (p *parser) peekNext() token {
	if p.pos+1 >= len(p.tokens) {
		return token{kind: tokEOF}
	}
	return p.tokens[p.pos+1]
}

func (p *parser) at(k tokenKind) bool { return p.peek().kind == k }

func (p *parser) advance() token {
	t := p.peek()
	if p.pos < len(p.tokens) {
		p.pos++
	}
	return t
}

func (p *parser) match(k tokenKind) bool {
	if p.at(k) {
		p.advance()
		return true
	}
	return false
}

func (p *parser) expect(k tokenKind, msg string) (token, error) {
	if p.at(k) {
		return p.advance(), nil
	}
	t := p.peek()
	return token{}, fmt.Errorf("line %d: expected %s near %q", t.line, msg, t.lex)
}

func (p *parser) parseStmt() (stmt, error) {
	if err := p.enterDepth(); err != nil {
		return nil, err
	}
	defer p.leaveDepth()
	if err := p.enterNode(); err != nil {
		return nil, err
	}

	switch p.peek().kind {
	case tokConst, tokLet, tokVar:
		kw := p.advance()
		ident, err := p.expect(tokIdent, "variable name")
		if err != nil {
			return nil, err
		}
		// Optional simple TypeScript annotation: `: number` / `: string` / `: boolean` / `: Series`
		if p.match(tokColon) {
			if _, err := p.expect(tokIdent, "type annotation"); err != nil {
				return nil, err
			}
			if p.match(tokLBracket) {
				if _, err := p.expect(tokRBracket, "]"); err != nil {
					return nil, err
				}
			}
		}
		if _, err := p.expect(tokAssign, "'='"); err != nil {
			return nil, err
		}
		initExpr, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		p.match(tokSemi)
		return &varDeclStmt{
			name:    ident.lex,
			isConst: kw.kind == tokConst,
			init:    initExpr,
			line:    kw.line,
		}, nil

	case tokLBrace:
		p.advance()
		var list []stmt
		for !p.at(tokRBrace) && !p.at(tokEOF) {
			for p.match(tokSemi) {
			}
			if p.at(tokRBrace) {
				break
			}
			st, err := p.parseStmt()
			if err != nil {
				return nil, err
			}
			list = append(list, st)
		}
		if _, err := p.expect(tokRBrace, "'}'"); err != nil {
			return nil, err
		}
		return &blockStmt{stmts: list}, nil

	case tokIf:
		p.advance()
		if _, err := p.expect(tokLParen, "'('"); err != nil {
			return nil, err
		}
		cond, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tokRParen, "')'"); err != nil {
			return nil, err
		}
		thenBranch, err := p.parseStmt()
		if err != nil {
			return nil, err
		}
		var elseBranch stmt
		if p.match(tokElse) {
			elseBranch, err = p.parseStmt()
			if err != nil {
				return nil, err
			}
		}
		return &ifStmt{cond: cond, then: thenBranch, els: elseBranch}, nil
	}

	if p.at(tokIdent) && p.peekNext().kind == tokAssign {
		ident := p.advance()
		p.advance() // '='
		val, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		p.match(tokSemi)
		return &assignStmt{name: ident.lex, val: val, line: ident.line}, nil
	}

	e, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	p.match(tokSemi)
	return &exprStmt{e: e}, nil
}

func (p *parser) parseExpr() (expr, error) {
	if err := p.enterDepth(); err != nil {
		return nil, err
	}
	defer p.leaveDepth()
	return p.parseTernary()
}

func (p *parser) parseTernary() (expr, error) {
	cond, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.match(tokQuestion) {
		if err := p.enterNode(); err != nil {
			return nil, err
		}
		tExpr, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tokColon, "':'"); err != nil {
			return nil, err
		}
		fExpr, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		return &ternaryExpr{cond: cond, t: tExpr, f: fExpr}, nil
	}
	return cond, nil
}

func (p *parser) parseOr() (expr, error) {
	lhs, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.match(tokOr) {
		if err := p.enterNode(); err != nil {
			return nil, err
		}
		rhs, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		lhs = &binaryExpr{op: tokOr, lhs: lhs, rhs: rhs}
	}
	return lhs, nil
}

func (p *parser) parseAnd() (expr, error) {
	lhs, err := p.parseEquality()
	if err != nil {
		return nil, err
	}
	for p.match(tokAnd) {
		if err := p.enterNode(); err != nil {
			return nil, err
		}
		rhs, err := p.parseEquality()
		if err != nil {
			return nil, err
		}
		lhs = &binaryExpr{op: tokAnd, lhs: lhs, rhs: rhs}
	}
	return lhs, nil
}

func (p *parser) parseEquality() (expr, error) {
	lhs, err := p.parseCompare()
	if err != nil {
		return nil, err
	}
	for p.at(tokEQ) || p.at(tokNE) {
		op := p.advance().kind
		if err := p.enterNode(); err != nil {
			return nil, err
		}
		rhs, err := p.parseCompare()
		if err != nil {
			return nil, err
		}
		lhs = &binaryExpr{op: op, lhs: lhs, rhs: rhs}
	}
	return lhs, nil
}

func (p *parser) parseCompare() (expr, error) {
	lhs, err := p.parseTerm()
	if err != nil {
		return nil, err
	}
	for p.at(tokLT) || p.at(tokLE) || p.at(tokGT) || p.at(tokGE) {
		op := p.advance().kind
		if err := p.enterNode(); err != nil {
			return nil, err
		}
		rhs, err := p.parseTerm()
		if err != nil {
			return nil, err
		}
		lhs = &binaryExpr{op: op, lhs: lhs, rhs: rhs}
	}
	return lhs, nil
}

func (p *parser) parseTerm() (expr, error) {
	lhs, err := p.parseFactor()
	if err != nil {
		return nil, err
	}
	for p.at(tokPlus) || p.at(tokMinus) {
		op := p.advance().kind
		if err := p.enterNode(); err != nil {
			return nil, err
		}
		rhs, err := p.parseFactor()
		if err != nil {
			return nil, err
		}
		lhs = &binaryExpr{op: op, lhs: lhs, rhs: rhs}
	}
	return lhs, nil
}

func (p *parser) parseFactor() (expr, error) {
	lhs, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for p.at(tokStar) || p.at(tokSlash) || p.at(tokPercent) {
		op := p.advance().kind
		if err := p.enterNode(); err != nil {
			return nil, err
		}
		rhs, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		lhs = &binaryExpr{op: op, lhs: lhs, rhs: rhs}
	}
	return lhs, nil
}

func (p *parser) parseUnary() (expr, error) {
	if p.at(tokBang) || p.at(tokMinus) || p.at(tokPlus) {
		op := p.advance().kind
		if err := p.enterNode(); err != nil {
			return nil, err
		}
		rhs, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &unaryExpr{op: op, rhs: rhs}, nil
	}
	return p.parsePostfix()
}

func (p *parser) parsePostfix() (expr, error) {
	base, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	for {
		switch {
		case p.match(tokLParen):
			if err := p.enterNode(); err != nil {
				return nil, err
			}
			line := p.peek().line
			var args []expr
			if !p.at(tokRParen) {
				for {
					a, aErr := p.parseExpr()
					if aErr != nil {
						return nil, aErr
					}
					args = append(args, a)
					if len(args) > 16 {
						return nil, fmt.Errorf("line %d: too many function arguments", line)
					}
					if !p.match(tokComma) {
						break
					}
					if p.at(tokRParen) {
						break
					}
				}
			}
			if _, err := p.expect(tokRParen, "')'"); err != nil {
				return nil, err
			}
			p.maybeCaptureInputSpec(base, args)
			base = &callExpr{callee: base, args: args, line: line}

		case p.match(tokDot):
			if err := p.enterNode(); err != nil {
				return nil, err
			}
			prop, err := p.expect(tokIdent, "property name")
			if err != nil {
				return nil, err
			}
			base = &memberExpr{obj: base, prop: prop.lex, line: prop.line}

		default:
			return base, nil
		}
	}
}

func (p *parser) maybeCaptureInputSpec(callee expr, args []expr) {
	idExpr, ok := callee.(*identExpr)
	if !ok || idExpr.name != "input" || len(args) < 2 {
		return
	}
	nameLit, ok := args[0].(*literalExpr)
	if !ok || nameLit.kind != "string" || nameLit.s == "" || p.seenIn[nameLit.s] || len(p.inputs) >= MaxInputs {
		return
	}
	def, ok := evalNumericLiteral(args[1])
	if !ok {
		return
	}
	spec := InputSpec{
		Name:    nameLit.s,
		Default: def,
		Min:     def,
		Max:     def,
		Step:    1,
	}
	if len(args) >= 3 {
		if v, ok := evalNumericLiteral(args[2]); ok {
			spec.Min = v
		}
	}
	if len(args) >= 4 {
		if v, ok := evalNumericLiteral(args[3]); ok {
			spec.Max = v
		}
	}
	if len(args) >= 5 {
		if v, ok := evalNumericLiteral(args[4]); ok && v > 0 {
			spec.Step = v
		}
	}
	if spec.Max < spec.Min {
		spec.Max = spec.Min
	}
	p.seenIn[spec.Name] = true
	p.inputs = append(p.inputs, spec)
}

func evalNumericLiteral(e expr) (float64, bool) {
	switch node := e.(type) {
	case *literalExpr:
		if node.kind == "float" {
			return node.f, true
		}
	case *unaryExpr:
		if node.op == tokMinus {
			v, ok := evalNumericLiteral(node.rhs)
			return -v, ok
		}
	}
	return 0, false
}

func (p *parser) parsePrimary() (expr, error) {
	if err := p.enterNode(); err != nil {
		return nil, err
	}
	t := p.peek()
	switch t.kind {
	case tokNumber:
		p.advance()
		v, err := strconv.ParseFloat(t.lex, 64)
		if err != nil {
			return nil, fmt.Errorf("line %d: invalid number %q", t.line, t.lex)
		}
		return &literalExpr{kind: "float", f: v}, nil

	case tokString:
		p.advance()
		return &literalExpr{kind: "string", s: t.lex}, nil

	case tokTrue:
		p.advance()
		return &literalExpr{kind: "bool", b: true}, nil

	case tokFalse:
		p.advance()
		return &literalExpr{kind: "bool", b: false}, nil

	case tokNull:
		p.advance()
		return &literalExpr{kind: "null"}, nil

	case tokIdent:
		p.advance()
		return &identExpr{name: t.lex, line: t.line}, nil

	case tokLParen:
		p.advance()
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tokRParen, "')'"); err != nil {
			return nil, err
		}
		return e, nil

	case tokLBracket:
		p.advance()
		var elems []expr
		if !p.at(tokRBracket) {
			for {
				el, err := p.parseExpr()
				if err != nil {
					return nil, err
				}
				elems = append(elems, el)
				if len(elems) > 64 {
					return nil, fmt.Errorf("line %d: array literal exceeds 64 elements", t.line)
				}
				if !p.match(tokComma) {
					break
				}
				if p.at(tokRBracket) {
					break
				}
			}
		}
		if _, err := p.expect(tokRBracket, "']'"); err != nil {
			return nil, err
		}
		return &arrayExpr{elems: elems}, nil

	case tokLBrace:
		p.advance()
		var fields []objectField
		if !p.at(tokRBrace) {
			for {
				var key string
				switch p.peek().kind {
				case tokIdent:
					key = p.advance().lex
				case tokString:
					key = p.advance().lex
				default:
					return nil, fmt.Errorf("line %d: expected object key near %q", p.peek().line, p.peek().lex)
				}
				var valExpr expr
				if p.match(tokColon) {
					var err error
					valExpr, err = p.parseExpr()
					if err != nil {
						return nil, err
					}
				} else {
					valExpr = &identExpr{name: key, line: p.peek().line}
				}
				fields = append(fields, objectField{key: key, val: valExpr})
				if len(fields) > 32 {
					return nil, fmt.Errorf("line %d: object literal exceeds 32 fields", t.line)
				}
				if !p.match(tokComma) {
					break
				}
				if p.at(tokRBrace) {
					break
				}
			}
		}
		if _, err := p.expect(tokRBrace, "'}'"); err != nil {
			return nil, err
		}
		return &objectExpr{fields: fields}, nil
	}

	return nil, fmt.Errorf("line %d: unexpected token %q", t.line, t.lex)
}
