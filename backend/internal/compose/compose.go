// Package compose turns a message template into the text one recipient gets.
//
// Two mechanisms, applied in a fixed order:
//
//	spintax     {Halo|Hai|Selamat pagi}   — one branch chosen at random
//	variables   <<nama|Kak>>              — replaced by this recipient's value
//
// Variables also accept the older {{nama|Kak}} spelling; see Normalize.
//
// Spinning happens first and deliberately steps over anything wrapped in double
// braces. Doing it the other way round would let a contact whose name happens to
// contain a brace or a pipe alter the message that gets sent to them, which is
// a small thing until the day it puts half a sentence in front of a customer.
//
// Both are pure functions of their input plus a random source, so the same
// template with the same seed renders identically — which is what makes the
// preview on the review screen the real message rather than an illustration of
// one.
package compose

import (
	"errors"
	"fmt"
	"math/rand"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// MaxDepth bounds nested spintax.
//
// Nesting is legitimate — {Halo {pagi|siang}|Hai} is a normal thing to write —
// but a template with hundreds of levels is either a mistake or an attempt to
// make rendering expensive, and neither deserves the CPU.
const MaxDepth = 8

// ErrDepth is returned when spintax nests past MaxDepth.
var ErrDepth = errors.New("compose: spintax bersarang terlalu dalam")

// Spin resolves spintax, picking one branch of each group.
//
// Text in double braces is copied through untouched so variables survive to be
// substituted afterwards. An unclosed brace is treated as a literal character
// rather than an error: somebody writing "diskon 50% { hari ini" has made a
// typo, not a template, and refusing to send is a worse answer than sending
// what they wrote.
func Spin(template string, rng *rand.Rand) (string, error) {
	if rng == nil {
		rng = rand.New(rand.NewSource(rand.Int63()))
	}
	out, _, err := spin([]rune(template), 0, 0, rng)
	return out, err
}

// spin renders from position i until it meets an unescaped '}' or the end.
// It returns the text produced and the position it stopped at.
func spin(src []rune, i, depth int, rng *rand.Rand) (string, int, error) {
	if depth > MaxDepth {
		return "", i, ErrDepth
	}

	// Alternatives of the group currently being read. At depth 0 there is only
	// ever one, which is the whole message.
	branches := []strings.Builder{{}}
	cur := func() *strings.Builder { return &branches[len(branches)-1] }

	for i < len(src) {
		switch src[i] {
		case '{':
			// "{{" opens a variable. Copy it through verbatim, including its
			// closing braces, so the pipe inside is a fallback rather than a
			// spintax separator.
			if i+1 < len(src) && src[i+1] == '{' {
				end := findVarEnd(src, i)
				if end < 0 {
					cur().WriteRune(src[i])
					i++
					continue
				}
				cur().WriteString(string(src[i : end+1]))
				i = end + 1
				continue
			}

			text, next, err := spin(src, i+1, depth+1, rng)
			if err != nil {
				return "", next, err
			}
			cur().WriteString(text)
			i = next

		case '}':
			if depth == 0 {
				// A stray closing brace at the top level is just a character.
				cur().WriteRune(src[i])
				i++
				continue
			}
			return pick(branches, rng), i + 1, nil

		case '|':
			if depth == 0 {
				cur().WriteRune(src[i])
				i++
				continue
			}
			branches = append(branches, strings.Builder{})
			i++

		default:
			cur().WriteRune(src[i])
			i++
		}
	}

	if depth > 0 {
		// Ran off the end inside a group: the author never closed it. Treat the
		// whole thing as literal text, brace included.
		return "{" + joinBranches(branches), i, nil
	}
	return branches[0].String(), i, nil
}

func pick(branches []strings.Builder, rng *rand.Rand) string {
	if len(branches) == 1 {
		return branches[0].String()
	}
	return branches[rng.Intn(len(branches))].String()
}

func joinBranches(branches []strings.Builder) string {
	parts := make([]string, 0, len(branches))
	for i := range branches {
		parts = append(parts, branches[i].String())
	}
	return strings.Join(parts, "|")
}

// findVarEnd returns the index of the final '}' of a "{{...}}" starting at i,
// or -1 when there is no closing pair.
func findVarEnd(src []rune, i int) int {
	for j := i + 2; j+1 < len(src); j++ {
		if src[j] == '}' && src[j+1] == '}' {
			return j + 1
		}
		// A newline inside a variable means it was never one.
		if src[j] == '\n' {
			return -1
		}
	}
	return -1
}

// Render substitutes {{key}} and {{key|fallback}}.
//
// The returned slice names every placeholder that had neither a value nor a
// fallback. Those are not filled with an empty string silently: "Halo ," in
// front of a customer is the visible result of a variable nobody noticed was
// missing, so the caller is told and the review screen can refuse to send.
func Render(template string, vars map[string]string) (string, []string) {
	src := []rune(template)
	var out strings.Builder
	missing := map[string]bool{}

	for i := 0; i < len(src); {
		if src[i] == '{' && i+1 < len(src) && src[i+1] == '{' {
			end := findVarEnd(src, i)
			if end < 0 {
				out.WriteRune(src[i])
				i++
				continue
			}
			inner := string(src[i+2 : end-1])
			name, fallback, hasFallback := splitVar(inner)
			key := normalizeKey(name)

			switch {
			case key == "":
				// "{{}}" or "{{ | }}" — not a variable at all.
				out.WriteString(string(src[i : end+1]))
			case vars[key] != "":
				out.WriteString(vars[key])
			case hasFallback:
				out.WriteString(fallback)
			default:
				missing[key] = true
				// Left in place rather than blanked, so a message that somehow
				// reaches the wire shows an obvious defect instead of a
				// plausible-looking sentence with a hole in it.
				out.WriteString(string(src[i : end+1]))
			}
			i = end + 1
			continue
		}
		out.WriteRune(src[i])
		i++
	}

	names := make([]string, 0, len(missing))
	for k := range missing {
		names = append(names, k)
	}
	sort.Strings(names)
	return out.String(), names
}

// splitVar separates "nama|Kak" into its name and fallback.
func splitVar(inner string) (name, fallback string, hasFallback bool) {
	if idx := strings.Index(inner, "|"); idx >= 0 {
		return strings.TrimSpace(inner[:idx]), strings.TrimSpace(inner[idx+1:]), true
	}
	return strings.TrimSpace(inner), "", false
}

// normalizeKey lowercases a placeholder name and rejects anything that is not a
// plain identifier, so "{{ 2+2 }}" is left alone rather than treated as a
// variable that happens to be missing.
func normalizeKey(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return ""
	}
	for _, r := range raw {
		if !unicode.IsLower(r) && !unicode.IsDigit(r) && r != '_' {
			return ""
		}
	}
	return raw
}

// Placeholders lists every variable a template refers to, in first-seen order.
// Used by the composer to show which values a campaign is going to need.
func Placeholders(template string) []string {
	src := []rune(Normalize(template))
	seen := map[string]bool{}
	var out []string

	for i := 0; i < len(src); {
		if src[i] == '{' && i+1 < len(src) && src[i+1] == '{' {
			end := findVarEnd(src, i)
			if end >= 0 {
				name, _, _ := splitVar(string(src[i+2 : end-1]))
				if key := normalizeKey(name); key != "" && !seen[key] {
					seen[key] = true
					out = append(out, key)
				}
				i = end + 1
				continue
			}
		}
		i++
	}
	return out
}

// Message is one fully rendered message plus what it took to get there.
type Message struct {
	Body    string
	Missing []string
}

// angleVar matches the composer's variable syntax: <<nama>> or <<nama|Kak>>.
var angleVar = regexp.MustCompile(`<<\s*([A-Za-z0-9_]+)\s*(\|[^<>]*)?>>`)

// Normalize rewrites <<nama>> into {{nama}} so one template can be written
// either way.
//
// The composer writes angle brackets because braces already mean spintax on the
// same screen, and "{Halo|Hai} {{nama|Kak}}" asks somebody to read two different
// meanings out of the same character. Everything below this line has always
// spoken braces, so the two syntaxes meet here rather than in five places.
//
// Braces are left exactly as they are: campaigns written before the composer
// changed are stored with them, and a template that still renders is a template
// nobody has to go back and edit.
func Normalize(template string) string {
	if !strings.Contains(template, "<<") {
		return template
	}
	return angleVar.ReplaceAllString(template, "{{$1$2}}")
}

// Build spins then renders, which is the order the whole feature depends on.
func Build(template string, vars map[string]string, rng *rand.Rand) (Message, error) {
	spun, err := Spin(Normalize(template), rng)
	if err != nil {
		return Message{}, err
	}
	body, missing := Render(spun, vars)
	return Message{Body: body, Missing: missing}, nil
}

// Validate checks a template before a campaign is saved.
//
// It reports the problems that only show up at send time otherwise: spintax
// nested past the limit, a group with an empty branch (which quietly sends a
// message with a word missing), and unbalanced braces.
func Validate(template string) []string {
	var problems []string
	template = Normalize(template)

	if _, err := Spin(template, rand.New(rand.NewSource(1))); err != nil {
		problems = append(problems, err.Error())
	}

	depth, opens := 0, 0
	src := []rune(template)
	for i := 0; i < len(src); i++ {
		switch src[i] {
		case '{':
			if i+1 < len(src) && src[i+1] == '{' {
				if end := findVarEnd(src, i); end >= 0 {
					i = end
					continue
				}
			}
			depth++
			opens++
			if depth > MaxDepth {
				problems = append(problems, ErrDepth.Error())
				return problems
			}
		case '}':
			depth--
		}
	}
	if depth != 0 {
		problems = append(problems,
			fmt.Sprintf("kurung kurawal tidak seimbang (%d dibuka, %d ditutup)", opens, opens-depth))
	}

	for _, group := range emptyBranchGroups(template) {
		problems = append(problems, fmt.Sprintf("pilihan spintax kosong pada %q", group))
	}
	return problems
}

// emptyBranchGroups finds top-level spintax groups with a blank alternative.
func emptyBranchGroups(template string) []string {
	var out []string
	src := []rune(template)
	for i := 0; i < len(src); i++ {
		if src[i] != '{' {
			continue
		}
		if i+1 < len(src) && src[i+1] == '{' {
			if end := findVarEnd(src, i); end >= 0 {
				i = end
				continue
			}
		}
		depth, j := 1, i+1
		for ; j < len(src) && depth > 0; j++ {
			switch src[j] {
			case '{':
				depth++
			case '}':
				depth--
			}
		}
		if depth != 0 {
			break
		}
		group := string(src[i:j])
		inner := string(src[i+1 : j-1])
		if strings.Contains(inner, "|") {
			for _, part := range strings.Split(inner, "|") {
				if strings.TrimSpace(part) == "" {
					out = append(out, group)
					break
				}
			}
		}
		i = j - 1
	}
	return out
}
