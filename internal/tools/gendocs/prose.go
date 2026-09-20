package main

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// deGo turns a Go doc comment into prose that reads correctly under a
// heading that already names the identifier.
//
// Go requires a doc comment to open with the identifier it documents, so
// "Instance is this meridian instance's identity" is correct in source
// but reads as a stutter on a page whose term is already `instance`.
// Stripping the identifier (and a following "is") leaves an ordinary
// sentence: "This meridian instance's identity".
//
// A comment that doesn't open with its identifier is an error rather
// than a passthrough: silently emitting the raw comment is how one
// oddly-phrased field would quietly reintroduce the stutter this exists
// to remove.
func deGo(ident, doc string) (string, error) {
	doc = strings.TrimSpace(doc)
	if doc == "" {
		return "", fmt.Errorf("%s: empty doc comment", ident)
	}
	rest, ok := strings.CutPrefix(doc, ident)
	if !ok {
		return "", fmt.Errorf("%s: doc comment must start with the identifier, got %q", ident, firstWords(doc, 6))
	}
	// Require a word boundary, so a field named "ID" can't swallow the
	// leading "ID" of a comment that actually opens with "IDs are ...".
	if rest != "" && !unicode.IsSpace(rune(rest[0])) {
		return "", fmt.Errorf("%s: doc comment must start with the identifier as a whole word, got %q", ident, firstWords(doc, 6))
	}
	rest = strings.TrimSpace(rest)
	// "are" as well as "is", so a plural field can be documented the way Go
	// expects ("Identities are the addresses...") instead of being forced
	// into a singular verb to survive this function.
	for _, verb := range []string{"is ", "are "} {
		if after, found := strings.CutPrefix(rest, verb); found {
			rest = after
			break
		}
	}
	if rest == "" {
		return "", fmt.Errorf("%s: doc comment says nothing beyond the identifier", ident)
	}
	return upperFirst(rest), nil
}

func upperFirst(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError {
		return s
	}
	return string(unicode.ToUpper(r)) + s[size:]
}

func firstWords(s string, n int) string {
	fields := strings.Fields(s)
	if len(fields) > n {
		return strings.Join(fields[:n], " ") + "..."
	}
	return strings.Join(fields, " ")
}

// checkMarkup rejects text the docs site would render wrongly.
//
// The site runs Goldmark with unsafe=true, so a bare "<old-id>" outside
// a code span parses as a custom-element tag and vanishes in the
// browser. Inside a code span the same text is escaped and renders
// literally, which is what placeholders always want — so the rule is
// simply that "<" must be backticked at the source. A lone ">" is left
// alone: it renders literally mid-sentence, and "->" is common in prose.
//
// Enforcing this here is what lets the templates render generated text
// as ordinary markdown instead of hand-parsing it.
func checkMarkup(where, text string) error {
	segments := strings.Split(text, "`")
	if len(segments)%2 == 0 {
		return fmt.Errorf("%s: unbalanced backtick in %q", where, firstWords(text, 8))
	}
	for i := 0; i < len(segments); i += 2 {
		if strings.Contains(segments[i], "<") {
			return fmt.Errorf("%s: %q has a bare `<` outside a code span — wrap placeholders in backticks", where, firstWords(segments[i], 8))
		}
	}
	return nil
}
