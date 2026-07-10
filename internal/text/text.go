package text

import (
	"html"
	"regexp"
	"strings"

	"github.com/ikawaha/kagome-dict/ipa"
	"github.com/ikawaha/kagome/v2/tokenizer"
)

var breaks = regexp.MustCompile(`(?is)<\s*(br\s*/?|/p|/div|/li|/pre|/h[1-6])\s*>`)
var tags = regexp.MustCompile(`(?is)<[^>]+>`)
var space = regexp.MustCompile(`[ \t\r\f\v]+`)
var tokenizerInstance, _ = tokenizer.New(ipa.Dict(), tokenizer.OmitBosEos())

func PlainHTML(s string) string {
	s = breaks.ReplaceAllString(s, "\n")
	s = tags.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = space.ReplaceAllString(s, " ")
	return strings.TrimSpace(strings.ReplaceAll(s, " \n", "\n"))
}
func SearchTokens(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	if tokenizerInstance == nil {
		return s
	}
	words := tokenizerInstance.Wakati(s)
	return strings.Join(words, " ")
}
func Snippet(s, q string) string {
	s = strings.TrimSpace(s)
	if len([]rune(s)) <= 180 {
		return s
	}
	r := []rune(s)
	return string(r[:180]) + "…"
}
