package fasedscript

import (
	"fmt"
	"strings"
	"unicode"
)

type tokenKind int

const (
	tokEOF tokenKind = iota
	tokIdent
	tokNumber
	tokString
	tokConst
	tokLet
	tokVar
	tokIf
	tokElse
	tokTrue
	tokFalse
	tokNull
	tokPlus
	tokMinus
	tokStar
	tokSlash
	tokPercent
	tokLT
	tokLE
	tokGT
	tokGE
	tokEQ
	tokNE
	tokAnd
	tokOr
	tokBang
	tokQuestion
	tokColon
	tokAssign
	tokLParen
	tokRParen
	tokLBrace
	tokRBrace
	tokLBracket
	tokRBracket
	tokComma
	tokDot
	tokSemi
)

type token struct {
	kind tokenKind
	lex  string
	line int
}

var forbiddenKeywords = map[string]bool{
	"import":      true,
	"export":      true,
	"eval":        true,
	"Function":    true,
	"while":       true,
	"for":         true,
	"do":          true,
	"new":         true,
	"class":       true,
	"async":       true,
	"await":       true,
	"fetch":       true,
	"require":     true,
	"process":     true,
	"window":      true,
	"globalThis":  true,
	"document":    true,
	"setTimeout":  true,
	"setInterval": true,
	"function":    true,
	"return":      true,
	"throw":       true,
	"try":         true,
	"catch":       true,
}

func tokenize(src string) ([]token, error) {
	if len(src) > MaxSourceBytes {
		return nil, fmt.Errorf("FasedScript exceeds %d bytes", MaxSourceBytes)
	}
	var tokens []token
	line := 1
	n := len(src)
	i := 0

	for i < n {
		c := src[i]
		if c == '\n' {
			line++
			i++
			continue
		}
		if c == ' ' || c == '\t' || c == '\r' {
			i++
			continue
		}
		// Line comment //
		if c == '/' && i+1 < n && src[i+1] == '/' {
			i += 2
			for i < n && src[i] != '\n' {
				i++
			}
			continue
		}
		// Block comment /* ... */
		if c == '/' && i+1 < n && src[i+1] == '*' {
			i += 2
			closed := false
			for i+1 < n {
				if src[i] == '\n' {
					line++
				}
				if src[i] == '*' && src[i+1] == '/' {
					i += 2
					closed = true
					break
				}
				i++
			}
			if !closed {
				return nil, fmt.Errorf("line %d: unterminated block comment", line)
			}
			continue
		}

		// String literal
		if c == '"' || c == '\'' {
			quote := c
			i++
			var sb strings.Builder
			closed := false
			for i < n {
				ch := src[i]
				if ch == '\n' {
					return nil, fmt.Errorf("line %d: unterminated string literal", line)
				}
				if ch == '\\' && i+1 < n {
					next := src[i+1]
					switch next {
					case 'n':
						sb.WriteByte('\n')
					case 't':
						sb.WriteByte('\t')
					case '\\', '"', '\'':
						sb.WriteByte(next)
					default:
						sb.WriteByte(next)
					}
					i += 2
					continue
				}
				if ch == quote {
					closed = true
					i++
					break
				}
				sb.WriteByte(ch)
				i++
			}
			if !closed {
				return nil, fmt.Errorf("line %d: unterminated string literal", line)
			}
			tokens = append(tokens, token{kind: tokString, lex: sb.String(), line: line})
			continue
		}

		// Numbers
		if c >= '0' && c <= '9' {
			start := i
			hasDot := false
			for i < n {
				ch := src[i]
				if ch >= '0' && ch <= '9' {
					i++
					continue
				}
				if ch == '.' && !hasDot && i+1 < n && src[i+1] >= '0' && src[i+1] <= '9' {
					hasDot = true
					i++
					continue
				}
				if ch == '_' {
					i++
					continue
				}
				break
			}
			numLex := strings.ReplaceAll(src[start:i], "_", "")
			tokens = append(tokens, token{kind: tokNumber, lex: numLex, line: line})
			continue
		}

		// Identifiers & keywords
		if isIdentStart(c) {
			start := i
			i++
			for i < n && isIdentCont(src[i]) {
				i++
			}
			ident := src[start:i]
			if forbiddenKeywords[ident] {
				return nil, fmt.Errorf("line %d: keyword %q is forbidden in FasedScript sandbox", line, ident)
			}
			switch ident {
			case "const":
				tokens = append(tokens, token{kind: tokConst, lex: ident, line: line})
			case "let":
				tokens = append(tokens, token{kind: tokLet, lex: ident, line: line})
			case "var":
				tokens = append(tokens, token{kind: tokVar, lex: ident, line: line})
			case "if":
				tokens = append(tokens, token{kind: tokIf, lex: ident, line: line})
			case "else":
				tokens = append(tokens, token{kind: tokElse, lex: ident, line: line})
			case "true":
				tokens = append(tokens, token{kind: tokTrue, lex: ident, line: line})
			case "false":
				tokens = append(tokens, token{kind: tokFalse, lex: ident, line: line})
			case "null":
				tokens = append(tokens, token{kind: tokNull, lex: ident, line: line})
			default:
				tokens = append(tokens, token{kind: tokIdent, lex: ident, line: line})
			}
			continue
		}

		// Multi-char operators
		if i+2 < n {
			three := src[i : i+3]
			if three == "===" {
				tokens = append(tokens, token{kind: tokEQ, lex: three, line: line})
				i += 3
				continue
			}
			if three == "!==" {
				tokens = append(tokens, token{kind: tokNE, lex: three, line: line})
				i += 3
				continue
			}
		}
		if i+1 < n {
			two := src[i : i+2]
			switch two {
			case "<=":
				tokens = append(tokens, token{kind: tokLE, lex: two, line: line})
				i += 2
				continue
			case ">=":
				tokens = append(tokens, token{kind: tokGE, lex: two, line: line})
				i += 2
				continue
			case "==":
				tokens = append(tokens, token{kind: tokEQ, lex: two, line: line})
				i += 2
				continue
			case "!=":
				tokens = append(tokens, token{kind: tokNE, lex: two, line: line})
				i += 2
				continue
			case "&&":
				tokens = append(tokens, token{kind: tokAnd, lex: two, line: line})
				i += 2
				continue
			case "||":
				tokens = append(tokens, token{kind: tokOr, lex: two, line: line})
				i += 2
				continue
			}
		}

		// Single-char tokens
		var tk tokenKind
		switch c {
		case '+':
			tk = tokPlus
		case '-':
			tk = tokMinus
		case '*':
			tk = tokStar
		case '/':
			tk = tokSlash
		case '%':
			tk = tokPercent
		case '<':
			tk = tokLT
		case '>':
			tk = tokGT
		case '!':
			tk = tokBang
		case '?':
			tk = tokQuestion
		case ':':
			tk = tokColon
		case '=':
			tk = tokAssign
		case '(':
			tk = tokLParen
		case ')':
			tk = tokRParen
		case '{':
			tk = tokLBrace
		case '}':
			tk = tokRBrace
		case '[':
			tk = tokLBracket
		case ']':
			tk = tokRBracket
		case ',':
			tk = tokComma
		case '.':
			tk = tokDot
		case ';':
			tk = tokSemi
		default:
			if unicode.IsPrint(rune(c)) {
				return nil, fmt.Errorf("line %d: unexpected character %q", line, c)
			}
			return nil, fmt.Errorf("line %d: unexpected byte 0x%02x", line, c)
		}
		tokens = append(tokens, token{kind: tk, lex: string(c), line: line})
		i++
	}

	tokens = append(tokens, token{kind: tokEOF, line: line})
	return tokens, nil
}

func isIdentStart(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_' || c == '$'
}

func isIdentCont(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}
