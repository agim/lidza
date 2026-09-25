package schema

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Load parses dir/schema.lidza. It returns nil, nil when the file does not
// exist: a project without a schema is valid.
func Load(dir string) (*Schema, error) {
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	s, err := Parse(string(data))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", FileName, err)
	}
	return s, nil
}

// Parse parses schema source text.
func Parse(src string) (*Schema, error) {
	p := &parser{lines: strings.Split(src, "\n")}
	s := &Schema{}
	for p.i < len(p.lines) {
		line := p.next()
		if line == "" {
			continue
		}
		word, rest := cut(line)
		switch word {
		case "enum":
			e, err := p.enum(rest)
			if err != nil {
				return nil, err
			}
			s.Enums = append(s.Enums, e)
		case "model", "type":
			m, err := p.block(rest, word == "model")
			if err != nil {
				return nil, err
			}
			if m.Persisted {
				s.Models = append(s.Models, m)
			} else {
				s.Types = append(s.Types, m)
			}
		default:
			return nil, fmt.Errorf("line %d: expected enum, model or type, got %q", p.i, word)
		}
	}
	if err := s.validate(); err != nil {
		return nil, err
	}
	return s, nil
}

type parser struct {
	lines []string
	i     int // number of lines consumed; also the 1-based line of the last one
}

// next returns the next line stripped of comments and whitespace.
func (p *parser) next() string {
	line := p.lines[p.i]
	p.i++
	if k := strings.Index(line, "//"); k >= 0 {
		line = line[:k]
	}
	if k := strings.Index(line, "#"); k >= 0 {
		line = line[:k]
	}
	return strings.TrimSpace(line)
}

func cut(s string) (string, string) {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, " \t{"); i >= 0 {
		return s[:i], strings.TrimSpace(s[i:])
	}
	return s, ""
}

// enum parses "Name { a b c }" on one line or across lines.
func (p *parser) enum(rest string) (*Enum, error) {
	name, body := cut(rest)
	e := &Enum{Name: name, Line: p.i}
	if !ident(name) {
		return nil, fmt.Errorf("line %d: bad enum name %q", p.i, name)
	}
	if !strings.HasPrefix(body, "{") {
		return nil, fmt.Errorf("line %d: enum %s: expected {", p.i, name)
	}
	body = body[1:]
	for {
		if k := strings.Index(body, "}"); k >= 0 {
			e.Values = append(e.Values, strings.Fields(body[:k])...)
			break
		}
		e.Values = append(e.Values, strings.Fields(body)...)
		if p.i >= len(p.lines) {
			return nil, fmt.Errorf("line %d: enum %s: missing }", e.Line, name)
		}
		body = p.next()
	}
	for _, v := range e.Values {
		if !ident(v) {
			return nil, fmt.Errorf("line %d: enum %s: bad value %q", e.Line, name, v)
		}
	}
	return e, nil
}

// block parses a model or type: "Name [@table("x")] {" then fields until "}".
func (p *parser) block(rest string, persisted bool) (*Model, error) {
	name, tail := cut(rest)
	m := &Model{Name: name, Line: p.i, Persisted: persisted}
	if !ident(name) || name[0] < 'A' || name[0] > 'Z' {
		return nil, fmt.Errorf("line %d: model name must be an exported identifier, got %q", p.i, name)
	}
	brace := strings.Index(tail, "{")
	if brace < 0 {
		return nil, fmt.Errorf("line %d: %s: expected {", p.i, name)
	}
	for _, a := range splitAttrs(tail[:brace]) {
		an, arg := attr(a)
		if an != "table" || !persisted {
			return nil, fmt.Errorf("line %d: %s: unknown attribute @%s", p.i, name, an)
		}
		m.Table = strings.Trim(arg, `"`)
	}
	if persisted && m.Table == "" {
		m.Table = snake(name)
	}
	if after := strings.TrimSpace(tail[brace+1:]); after != "" && after != "}" {
		return nil, fmt.Errorf("line %d: %s: fields start on the next line", p.i, name)
	}
	for {
		if p.i >= len(p.lines) {
			return nil, fmt.Errorf("line %d: %s: missing }", m.Line, name)
		}
		line := p.next()
		switch {
		case line == "":
			continue
		case line == "}":
			return m, nil
		case strings.HasPrefix(line, "@@"):
			an, arg := attr(line[1:])
			ix := Index{Fields: fields(arg)}
			switch an {
			case "index":
			case "unique":
				ix.Unique = true
			default:
				return nil, fmt.Errorf("line %d: %s: unknown block attribute @@%s", p.i, name, an)
			}
			if len(ix.Fields) == 0 {
				return nil, fmt.Errorf("line %d: %s: @@%s needs field names", p.i, name, an)
			}
			m.Indexes = append(m.Indexes, ix)
		default:
			f, err := p.field(line)
			if err != nil {
				return nil, err
			}
			m.Fields = append(m.Fields, f)
		}
	}
}

// field parses "name type[?|[]] @attr @attr(arg) ...".
func (p *parser) field(line string) (*Field, error) {
	parts := strings.Fields(line)
	if len(parts) < 2 {
		return nil, fmt.Errorf("line %d: expected \"name type\", got %q", p.i, line)
	}
	f := &Field{Name: parts[0], Type: parts[1], Line: p.i}
	if !ident(f.Name) || f.Name[0] < 'a' || f.Name[0] > 'z' {
		return nil, fmt.Errorf("line %d: field name must start with a lowercase letter, got %q", p.i, f.Name)
	}
	if strings.HasSuffix(f.Type, "?") {
		f.Optional = true
		f.Type = strings.TrimSuffix(f.Type, "?")
	}
	if strings.HasSuffix(f.Type, "[]") {
		f.Array = true
		f.Type = strings.TrimSuffix(f.Type, "[]")
	}
	if !ident(f.Type) {
		return nil, fmt.Errorf("line %d: %s: bad type %q", p.i, f.Name, parts[1])
	}
	for _, a := range splitAttrs(strings.Join(parts[2:], " ")) {
		name, arg := attr(a)
		var err error
		switch name {
		case "id":
			f.ID = true
		case "unique":
			f.Unique = true
		case "index":
			f.Index = true
		case "default":
			f.Default = arg
		case "ref":
			f.Ref = arg
		case "min":
			f.Min, err = num(arg)
		case "max":
			f.Max, err = num(arg)
		case "email":
			f.Email = true
		case "url":
			f.URL = true
		case "pattern":
			f.Pattern = strings.Trim(arg, `"`)
		default:
			return nil, fmt.Errorf("line %d: %s: unknown attribute @%s", p.i, f.Name, name)
		}
		if err != nil {
			return nil, fmt.Errorf("line %d: %s: @%s: %v", p.i, f.Name, name, err)
		}
		if arg == "" && (name == "default" || name == "ref" || name == "min" || name == "max" || name == "pattern") {
			return nil, fmt.Errorf("line %d: %s: @%s needs an argument", p.i, f.Name, name)
		}
	}
	if f.ID && (f.Optional || f.Array) {
		return nil, fmt.Errorf("line %d: %s: @id cannot be optional or an array", p.i, f.Name)
	}
	return f, nil
}

// splitAttrs splits "@a @b(x, y) @c" into ["a", "b(x, y)", "c"], honoring
// parentheses and quotes.
func splitAttrs(s string) []string {
	var out []string
	depth, quoted := 0, false
	start := -1
	for i, r := range s {
		switch {
		case r == '"':
			quoted = !quoted
		case quoted:
		case r == '(':
			depth++
		case r == ')':
			depth--
		case r == '@' && depth == 0:
			if start >= 0 {
				out = append(out, strings.TrimSpace(s[start:i]))
			}
			start = i + 1
		}
	}
	if start >= 0 {
		out = append(out, strings.TrimSpace(s[start:]))
	}
	return out
}

// attr splits "name(arg)" into name and arg.
func attr(s string) (string, string) {
	s = strings.TrimSpace(strings.TrimPrefix(s, "@"))
	if i := strings.Index(s, "("); i >= 0 && strings.HasSuffix(s, ")") {
		return s[:i], strings.TrimSpace(s[i+1 : len(s)-1])
	}
	return s, ""
}

func fields(arg string) []string {
	var out []string
	for _, f := range strings.Split(arg, ",") {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

func num(s string) (*float64, error) {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil, fmt.Errorf("expected a number, got %q", s)
	}
	return &v, nil
}

func ident(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		ok := r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (i > 0 && r >= '0' && r <= '9')
		if !ok {
			return false
		}
	}
	return true
}
