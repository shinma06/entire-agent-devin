package devin

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// parsePythonLiteral decodes the subset of Python literal syntax that Devin uses
// when it stringifies tool payloads (repr of dict/list/str/bool/None/number).
func parsePythonLiteral(s string) (any, error) {
	p := &pyParser{s: s}
	p.ws()
	v, err := p.value()
	if err != nil {
		return nil, err
	}
	p.ws()
	if p.i != len(p.s) {
		return nil, fmt.Errorf("trailing data at %d", p.i)
	}
	return v, nil
}

type pyParser struct {
	s string
	i int
}

func (p *pyParser) ws() {
	for p.i < len(p.s) && strings.ContainsRune(" \t\r\n", rune(p.s[p.i])) {
		p.i++
	}
}

func (p *pyParser) value() (any, error) {
	if p.i >= len(p.s) {
		return nil, fmt.Errorf("unexpected end")
	}
	switch c := p.s[p.i]; {
	case c == '{':
		return p.dict()
	case c == '[':
		return p.seq('[', ']')
	case c == '(':
		return p.seq('(', ')')
	case c == '\'' || c == '"':
		return p.str()
	case strings.HasPrefix(p.s[p.i:], "True"):
		p.i += 4
		return true, nil
	case strings.HasPrefix(p.s[p.i:], "False"):
		p.i += 5
		return false, nil
	case strings.HasPrefix(p.s[p.i:], "None"):
		p.i += 4
		return nil, nil
	case c == '-' || c == '+' || (c >= '0' && c <= '9'):
		return p.number()
	default:
		return nil, fmt.Errorf("unexpected %q at %d", c, p.i)
	}
}

func (p *pyParser) dict() (any, error) {
	p.i++
	out := map[string]any{}
	for {
		p.ws()
		if p.i < len(p.s) && p.s[p.i] == '}' {
			p.i++
			return out, nil
		}
		k, err := p.value()
		if err != nil {
			return nil, err
		}
		p.ws()
		if p.i >= len(p.s) || p.s[p.i] != ':' {
			return nil, fmt.Errorf("expected ':' at %d", p.i)
		}
		p.i++
		p.ws()
		v, err := p.value()
		if err != nil {
			return nil, err
		}
		out[fmt.Sprint(k)] = v
		p.ws()
		if p.i < len(p.s) && p.s[p.i] == ',' {
			p.i++
		}
	}
}

func (p *pyParser) seq(open, closer byte) (any, error) {
	p.i++
	out := []any{}
	for {
		p.ws()
		if p.i < len(p.s) && p.s[p.i] == closer {
			p.i++
			return out, nil
		}
		v, err := p.value()
		if err != nil {
			return nil, err
		}
		out = append(out, v)
		p.ws()
		if p.i < len(p.s) && p.s[p.i] == ',' {
			p.i++
		}
		if p.i >= len(p.s) {
			return nil, fmt.Errorf("unterminated %c", open)
		}
	}
}

func (p *pyParser) str() (any, error) {
	q := p.s[p.i]
	p.i++
	var b strings.Builder
	for p.i < len(p.s) {
		c := p.s[p.i]
		switch {
		case c == q:
			p.i++
			return b.String(), nil
		case c == '\\' && p.i+1 < len(p.s):
			p.i++
			e := p.s[p.i]
			p.i++
			switch e {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			case '0':
				b.WriteByte(0)
			case 'x', 'u', 'U':
				n := map[byte]int{'x': 2, 'u': 4, 'U': 8}[e]
				if p.i+n > len(p.s) {
					return nil, fmt.Errorf("bad escape")
				}
				r, err := strconv.ParseUint(p.s[p.i:p.i+n], 16, 32)
				if err != nil {
					return nil, err
				}
				b.WriteRune(rune(r))
				p.i += n
			default:
				b.WriteByte(e)
			}
		default:
			r, size := utf8.DecodeRuneInString(p.s[p.i:])
			b.WriteRune(r)
			p.i += size
		}
	}
	return nil, fmt.Errorf("unterminated string")
}

func (p *pyParser) number() (any, error) {
	start := p.i
	p.i++
	for p.i < len(p.s) && strings.ContainsRune("0123456789.eE+-_", rune(p.s[p.i])) {
		p.i++
	}
	lit := strings.ReplaceAll(p.s[start:p.i], "_", "")
	if n, err := strconv.ParseInt(lit, 10, 64); err == nil {
		return n, nil
	}
	return strconv.ParseFloat(lit, 64)
}
