package games

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Properties is a Java .properties file (server.properties) that keeps its comments and order, so
// saving a few values changes only those lines.
type Properties struct {
	lines []string
	index map[string]int // key -> line
	keys  []string
}

func ParseProperties(text string) *Properties {
	p := &Properties{index: map[string]int{}}
	raw := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if len(raw) > 0 && raw[len(raw)-1] == "" {
		raw = raw[:len(raw)-1]
	}
	for i := 0; i < len(raw); i++ {
		line := raw[i]
		// Join continuation lines into one logical line.
		for strings.HasSuffix(line, `\`) && !strings.HasSuffix(line, `\\`) && i+1 < len(raw) {
			i++
			line = strings.TrimSuffix(line, `\`) + strings.TrimLeft(raw[i], " \t")
		}
		p.lines = append(p.lines, line)
		if k, _, ok := splitProperty(line); ok {
			if _, seen := p.index[k]; !seen {
				p.keys = append(p.keys, k)
			}
			p.index[k] = len(p.lines) - 1
		}
	}
	return p
}

func splitProperty(line string) (string, string, bool) {
	t := strings.TrimLeft(line, " \t\f")
	if t == "" || t[0] == '#' || t[0] == '!' {
		return "", "", false
	}
	var key strings.Builder
	i := 0
	for i < len(t) {
		c := t[i]
		if c == '\\' && i+1 < len(t) {
			key.WriteByte(t[i+1])
			i += 2
			continue
		}
		if c == '=' || c == ':' || c == ' ' || c == '\t' {
			break
		}
		key.WriteByte(c)
		i++
	}
	rest := strings.TrimLeft(t[i:], " \t\f")
	if rest != "" && (rest[0] == '=' || rest[0] == ':') {
		rest = strings.TrimLeft(rest[1:], " \t\f")
	}
	return key.String(), unescapeProperty(rest), true
}

func unescapeProperty(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case 'f':
			b.WriteByte('\f')
		case 'u':
			r, n := hexRune(s[i+1:])
			if n == 0 {
				b.WriteByte('u')
				continue
			}
			i += n
			// A surrogate pair is written as two escapes.
			if utf16.IsSurrogate(r) && strings.HasPrefix(s[i+1:], `\u`) {
				if r2, n2 := hexRune(s[i+3:]); n2 > 0 {
					if pair := utf16.DecodeRune(r, r2); pair != utf8.RuneError {
						r = pair
						i += 2 + n2
					}
				}
			}
			b.WriteRune(r)
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// escapeProperty writes a value any Minecraft version reads: non-ASCII as \uXXXX.
func escapeProperty(v string, isKey bool) string {
	var b strings.Builder
	for i, r := range v {
		switch {
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case (r == '=' || r == ':' || r == '#' || r == '!') && isKey:
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == ' ' && (isKey || i == 0):
			b.WriteString(`\ `)
		case r > 0xffff:
			r1, r2 := utf16.EncodeRune(r)
			fmt.Fprintf(&b, `\u%04x\u%04x`, r1, r2)
		case r > 0x7e || r < 0x20:
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// hexRune reads the 4 hex digits of a \u escape; n is 0 when they aren't there.
func hexRune(s string) (rune, int) {
	if len(s) < 4 {
		return 0, 0
	}
	v, err := strconv.ParseUint(s[:4], 16, 32)
	if err != nil {
		return 0, 0
	}
	return rune(v), 4
}

func (p *Properties) Get(key string) (string, bool) {
	i, ok := p.index[key]
	if !ok {
		return "", false
	}
	_, v, _ := splitProperty(p.lines[i])
	return v, true
}

func (p *Properties) Keys() []string { return append([]string(nil), p.keys...) }

func (p *Properties) Values() map[string]string {
	out := map[string]string{}
	for _, k := range p.keys {
		out[k], _ = p.Get(k)
	}
	return out
}

// Set changes a value in place, or adds it at the end.
func (p *Properties) Set(key, value string) {
	line := escapeProperty(key, true) + "=" + escapeProperty(value, false)
	if i, ok := p.index[key]; ok {
		p.lines[i] = line
		return
	}
	p.lines = append(p.lines, line)
	p.index[key] = len(p.lines) - 1
	p.keys = append(p.keys, key)
}

func (p *Properties) String() string {
	if len(p.lines) == 0 {
		return ""
	}
	return strings.Join(p.lines, "\n") + "\n"
}
