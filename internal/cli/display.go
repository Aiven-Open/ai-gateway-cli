// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"fmt"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/aiven-open/ai-gateway-cli/internal/creds"
	"github.com/aiven-open/ai-gateway-cli/internal/harness"
	"github.com/aiven-open/ai-gateway-cli/internal/harness/claude"
	"github.com/aiven-open/ai-gateway-cli/internal/harness/omp"
	"github.com/aiven-open/ai-gateway-cli/internal/harness/opencode"
	"github.com/aiven-open/ai-gateway-cli/internal/harness/pi"
	"github.com/aiven-open/ai-gateway-cli/internal/models"
	"github.com/aiven-open/ai-gateway-cli/internal/redact"
)

var (
	// multiFields are TOML multi-line strings: a basic one ends at the first unescaped """, a literal one at '''.
	multiFields = []*regexp.Regexp{
		regexp.MustCompile(`(?is)(` + redact.Name + `["']?\s*=\s*)(""")((?:[^"\\]|\\.|"{1,2}(?:[^"\\]|\\.))*)(""")`),
		regexp.MustCompile(`(?is)(` + redact.Name + `["']?\s*=\s*)(''')(.*?)(''')`),
	}
	secretField  = regexp.MustCompile(`(?i)(` + redact.Name + `["']?\s*[:=]\s*)("(?:[^"\\\n]|\\.)*"|'[^'\n]*')`)
	numberField  = regexp.MustCompile(`(?i)(` + redact.Name + `["']?\s*[:=][ \t]*)([+-]?\.?\d[^\s,\]}#/]*)`)
	listField    = regexp.MustCompile(`(?i)(` + redact.Name + `["']?\s*[:=]\s*)[\[{]`)
	placeholder  = regexp.MustCompile(`<redacted (\d+)>`)
	blockComment = regexp.MustCompile(`(?s)((?:^|\s)/\*)((?:[^*]|\*+[^*/])*)(\*+/)`)
	credWord     = regexp.MustCompile(`(?i)` + redact.Word)
	comment      = regexp.MustCompile(`(?im)((?:^|\s)(?://|#|/\*|\*)[ \t]?)(.*` + redact.Word + `.*)$`)
	userinfo     = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)([^/\s"'@]+)@`)
	keyShaped    = regexp.MustCompile(redact.Token)
)

// launcherForms match what aiven-ai-gateway writes into credential fields for any gateway; each names where the key
// is. They are built from one gateway's forms, with its key file widened to any key file name creds can give.
var launcherForms = func() []*regexp.Regexp {
	const origin = "https://gateway.invalid"
	_, args := creds.Command(origin, runtime.GOOS)
	rel := regexp.QuoteMeta(creds.File) + "|" + regexp.QuoteMeta(creds.Dir) + "/[a-z0-9.-]+"
	var forms []*regexp.Regexp
	for _, v := range []string{pi.APIKey(origin), omp.APIKey(origin), opencode.APIKey(origin), claude.APIKeyHelper(origin), args[len(args)-1]} {
		quoted := strings.ReplaceAll(regexp.QuoteMeta(v), regexp.QuoteMeta(creds.Rel(origin)), "(?:"+rel+")")
		forms = append(forms, regexp.MustCompile("^"+quoted+"$"))
	}
	return forms
}()

// launcherValue reports whether v is a value aiven-ai-gateway writes into a credential field.
func launcherValue(v string) bool {
	return v == creds.EnvVar || slices.ContainsFunc(launcherForms, func(re *regexp.Regexp) bool { return re.MatchString(v) })
}

// shown renders both sides of a config change for its preview. A user's config can hold keys of their own, which a
// diff would print as context or removed lines, so they are redacted on both sides. Each hidden value is numbered
// per file, so a changed one still shows as a changed line. In a file listing every gateway model, the aiven
// provider's list collapses to one summary line; the rest of the file is diffed in full. It collapses after the YAML
// pass, which parses the text and would read a summary line as a value or reject it.
func shown(f harness.File, base []byte, table []models.Model) (string, string) {
	old, data := string(base), string(f.Data)
	r := newRedactor(f.Reference, old, data)
	collapse := collapseModels
	if ext := filepath.Ext(f.Path); ext == ".yml" || ext == ".yaml" {
		old, data, collapse = r.yaml(old), r.yaml(data), collapseYAMLModels
	}
	if f.Models {
		block, collapsed := collapse(data, "["+modelNames(table, 2)+"]")
		data = collapsed
		if same, kept := collapse(old, "["+modelNames(table, 2)+"]"); same == block {
			old = kept
		} else {
			_, old = collapse(old, "[the previous model list]")
		}
	}
	return r.redact(old), r.redact(data)
}

// collapseModels replaces the aiven provider's "models" list with summary when it is laid out as harness.JSON writes
// it, and returns the lines it replaced. A list in any other layout is left in full.
func collapseModels(text, summary string) (string, string) {
	lines := strings.SplitAfter(text, "\n")
	parent := ""
	for i, l := range lines {
		if len(l) > 2 && l[:2] == "  " && l[2] != ' ' {
			parent = strings.TrimSpace(l)
		}
		// Only pi's providers.aiven and OpenCode's provider.aiven hold the gateway models.
		if strings.TrimSuffix(l, "\n") != `    "aiven": {` || parent != `"providers": {` && parent != `"provider": {` {
			continue
		}
		in := "      "
		for j := i + 1; j < len(lines) && strings.HasPrefix(lines[j], in); j++ {
			if head := strings.TrimSuffix(lines[j], "\n"); head != in+`"models": [` && head != in+`"models": {` {
				continue
			}
			for k := j + 1; k < len(lines) && strings.HasPrefix(lines[k], in); k++ {
				switch end := strings.TrimSuffix(lines[k], "\n")[len(in):]; end {
				case "]", "],", "}", "},":
					return strings.Join(lines[j:k+1], ""), strings.Join(lines[:j], "") + in + `"models": ` + summary +
						end[1:] + "\n" + strings.Join(lines[k+1:], "")
				}
			}
		}
	}
	return "", text
}

// collapseYAMLModels is collapseModels for OMP's providers.aiven, laid out as its models file is written: mappings
// indented by two and the list's items at the indent of its key. Blank and comment lines between items belong to the
// list; those after its last item stay.
func collapseYAMLModels(text, summary string) (string, string) {
	lines := strings.SplitAfter(text, "\n")
	top, parent := "", ""
	for i, l := range lines {
		head := strings.TrimSuffix(l, "\n")
		switch {
		case head != "" && head[0] != ' ' && head[0] != '#':
			top, parent = head, ""
		case len(head) > 2 && head[:2] == "  " && head[2] != ' ' && head[2] != '#':
			parent = head
		}
		if head != "    models:" || top != "providers:" || parent != "  aiven:" {
			continue
		}
		end := i
		for k := i + 1; k < len(lines); k++ {
			next := strings.TrimSuffix(lines[k], "\n")
			if strings.HasPrefix(next, "    - ") || strings.HasPrefix(next, "      ") && strings.TrimSpace(next) != "" {
				end = k
			} else if strings.TrimSpace(next) != "" && !strings.HasPrefix(next, "    #") {
				break
			}
		}
		if end == i {
			return "", text
		}
		return strings.Join(lines[i:end+1], ""), strings.Join(lines[:i], "") + "    models: " + summary + "\n" +
			strings.Join(lines[end+1:], "")
	}
	return "", text
}

// redactor numbers the values it hides, so equal values on both sides of a diff get the same placeholder.
// reference is the file's harness.File.Reference. Until redact returns, a placeholder starts with marker, which the
// texts do not contain, so a later pass can tell its own placeholders from a value that only reads like one.
type redactor struct {
	seen      map[string]int
	reference func(field, value string) bool
	marker    string
	// taken holds the numbers of placeholders the texts already contain, which new ones skip.
	taken map[int]bool
	last  *int
}

// newRedactor returns a redactor for texts, the sides of one diff.
func newRedactor(reference func(field, value string) bool, texts ...string) redactor {
	r := redactor{seen: map[string]int{}, reference: reference, marker: "\x00", taken: map[int]bool{}, last: new(int)}
	for _, text := range texts {
		for strings.Contains(text, r.marker) {
			r.marker += "\x00"
		}
		for _, m := range placeholder.FindAllStringSubmatch(text, -1) {
			if n, err := strconv.Atoi(m[1]); err == nil {
				r.taken[n] = true
			}
		}
	}
	return r
}

func (r redactor) mask(v string) string {
	if r.seen[v] == 0 {
		for *r.last++; r.taken[*r.last]; *r.last++ {
		}
		r.seen[v] = *r.last
	}
	return fmt.Sprintf("%s<redacted %d>", r.marker, r.seen[v])
}

// hide masks v unless an earlier pass already replaced it with a placeholder.
func (r redactor) hide(v string) string {
	if rest, ok := strings.CutPrefix(v, r.marker); ok && placeholder.FindString(rest) == rest {
		return v
	}
	return r.mask(v)
}

// redact hides what may be a credential: string and number values of fields named like one (TOML multi-line strings
// and the values inside a list or object too), comments that mention one, URL user info, and anything shaped like a
// known credential (redact.Token). A field can hold a key in any shape, spaces included, so only values whose meaning is fixed stay:
// empty ones, the values aiven-ai-gateway writes, and those the file's own syntax makes a reference, such as
// OpenCode's {env:NAME} in opencode.json. The same text in another tool's file is a literal value.
func (r redactor) redact(text string) string {
	for _, multiField := range multiFields {
		text = multiField.ReplaceAllStringFunc(text, func(m string) string {
			sub := multiField.FindStringSubmatch(m)
			return sub[1] + sub[2] + r.hide(sub[3]) + sub[4]
		})
	}
	text = secretField.ReplaceAllStringFunc(text, func(m string) string {
		sub := secretField.FindStringSubmatch(m)
		quote, v := sub[2][:1], sub[2][1:len(sub[2])-1]
		if r.keeps(sub[1], sub[2]) {
			return m
		}
		return sub[1] + quote + r.hide(v) + quote
	})
	text = r.lists(text)
	text = numberField.ReplaceAllStringFunc(text, func(m string) string {
		sub := numberField.FindStringSubmatch(m)
		return sub[1] + r.hide(sub[2])
	})
	// Masking a comment can remove the end of a block comment, so containers are scanned first. A block comment that
	// mentions a credential is hidden up to its end, since the value can sit on a later line than the word.
	text = blockComment.ReplaceAllStringFunc(text, func(m string) string {
		sub := blockComment.FindStringSubmatch(m)
		if !credWord.MatchString(sub[2]) {
			return m
		}
		return sub[1] + r.blank(sub[2]) + sub[3]
	})
	text = comment.ReplaceAllStringFunc(text, func(m string) string {
		sub := comment.FindStringSubmatch(m)
		return sub[1] + r.hide(sub[2])
	})
	text = userinfo.ReplaceAllStringFunc(text, func(m string) string {
		sub := userinfo.FindStringSubmatch(m)
		return sub[1] + r.hide(sub[2]) + "@"
	})
	text = keyShaped.ReplaceAllStringFunc(text, r.hide)
	return strings.ReplaceAll(text, r.marker, "")
}

// lists hides the values and comments inside an array or object that a credential field holds, such as a TOML list
// of keys or a JSON object wrapping one. Names of fields inside it stay; an unclosed one is hidden to the end of text.
func (r redactor) lists(text string) string {
	var b strings.Builder
	for {
		loc := listField.FindStringSubmatchIndex(text)
		if loc == nil {
			return b.String() + text
		}
		field, start := text[loc[2]:loc[3]], loc[1]-1
		b.WriteString(text[:start])
		// "key: [" inside a string or comment is not a field; walking from there would pair later quotes wrongly.
		if quoted(b.String()) {
			b.WriteByte(text[start])
			text = text[start+1:]
			continue
		}
		depth, i := 0, start
		for i < len(text) {
			c := text[i]
			switch {
			case c == '[' || c == '{':
				depth++
			case c == ']' || c == '}':
				depth--
			case c == '#' || strings.HasPrefix(text[i:], "//"):
				open := 1
				if c == '/' {
					open = 2
				}
				end := strings.IndexByte(text[i:], '\n')
				if end < 0 {
					end = len(text) - i
				}
				b.WriteString(text[i:i+open] + r.blank(text[i+open:i+end]))
				i += end
				continue
			case strings.HasPrefix(text[i:], "/*"):
				end := strings.Index(text[i+2:], "*/")
				if end < 0 {
					b.WriteString("/*" + r.blank(text[i+2:]))
					i = len(text)
					continue
				}
				b.WriteString("/*" + r.blank(text[i+2:i+2+end]) + "*/")
				i += end + 4
				continue
			case c == '"' || c == '\'':
				i = r.listString(&b, text, i, field)
				continue
			case bare(c):
				end := i + 1
				for end < len(text) && (bare(text[end]) || text[end] == ':') {
					end++
				}
				tok, rest := text[i:end], strings.TrimLeft(text[end:], " \t")
				// A number can be a key, such as a PIN; other bare words are field names, booleans and the like.
				number := digit(tok[0]) || len(tok) > 1 && strings.IndexByte("+-.", tok[0]) >= 0 && digit(tok[1])
				if number && (rest == "" || rest[0] != '=' && rest[0] != ':') {
					b.WriteString(r.hide(tok))
				} else {
					b.WriteString(text[i:end])
				}
				i = end
				continue
			}
			b.WriteByte(c)
			i++
			if depth == 0 {
				break
			}
		}
		text = text[i:]
	}
}

// listString copies the string at text[i] inside a credential container to b, hidden unless it is a field name or
// keeps allows it, and returns where the string ends.
func (r redactor) listString(b *strings.Builder, text string, i int, field string) int {
	c := text[i]
	delim := text[i : i+1]
	if strings.HasPrefix(text[i:], strings.Repeat(delim, 3)) {
		delim = strings.Repeat(delim, 3)
	}
	multi := len(delim) == 3
	end := i + len(delim)
	for end < len(text) && !strings.HasPrefix(text[end:], delim) && (multi || text[end] != '\n') {
		if c == '"' && text[end] == '\\' && end+1 < len(text) && (multi || text[end+1] != '\n') {
			end++
		}
		end++
	}
	body, closed := text[i+len(delim):end], strings.HasPrefix(text[end:], delim)
	if closed && !multi {
		if rest := strings.TrimLeft(text[end+1:], " \t"); rest != "" && (rest[0] == ':' || rest[0] == '=') ||
			r.keeps(field, delim+body+delim) {
			b.WriteString(text[i : end+1])
			return end + 1
		}
	}
	b.WriteString(delim + r.hide(body))
	if !closed {
		return end
	}
	b.WriteString(delim)
	return end + len(delim)
}

// blank hides a comment's text, keeping one leading space so it still reads as a comment.
func (r redactor) blank(text string) string {
	body := strings.TrimLeft(text, " \t")
	if strings.TrimSpace(body) == "" {
		return text
	}
	return text[:min(len(text)-len(body), 1)] + r.hide(body)
}

// bare reports whether c can start a TOML bare key or literal.
func bare(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || digit(c) || strings.IndexByte("_+.-", c) >= 0
}

func digit(c byte) bool { return c >= '0' && c <= '9' }

// quoted reports whether the end of text lies inside a string or comment opened on its last line.
func quoted(text string) bool {
	var quote byte
	for i := strings.LastIndexByte(text, '\n') + 1; i < len(text); i++ {
		switch c := text[i]; {
		case quote == 0 && (c == '"' || c == '\''):
			quote = c
		case quote == 0 && (c == '#' || strings.HasPrefix(text[i:], "//")):
			return true
		case c == quote:
			quote = 0
		case quote == '"' && c == '\\':
			i++
		}
	}
	return quote != 0
}

// keeps reports whether a credential field's quoted value only says where a key is. field is the name with its
// separator; a JSON value is compared unescaped, since the launcher's helper command holds quotes.
func (r redactor) keeps(field, quoted string) bool {
	v := quoted[1 : len(quoted)-1]
	if quoted[0] == '"' {
		if u, err := strconv.Unquote(quoted); err == nil {
			v = u
		}
	}
	name := strings.ToLower(strings.TrimRight(field, "\"' \t:="))
	return v == "" || launcherValue(v) || r.reference != nil && r.reference(name, v)
}
