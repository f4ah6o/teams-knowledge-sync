package mail

import (
	"net/mail"
	"regexp"
	"strings"

	"github.com/obr-grp/teams-knowledge-sync/internal/config"
	"github.com/obr-grp/teams-knowledge-sync/internal/domain"
)

var angleAddress = regexp.MustCompile(`<([^<>]+)>`)

func NormalizeAddress(raw string) string {
	raw = strings.TrimSpace(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(raw)), "mailto:"))
	if m := angleAddress.FindStringSubmatch(raw); len(m) == 2 {
		raw = m[1]
	}
	raw = strings.TrimPrefix(strings.TrimSpace(raw), "mailto:")
	if a, err := mail.ParseAddress(raw); err == nil {
		raw = a.Address
	}
	return strings.TrimSpace(strings.Trim(raw, "<>\"'"))
}

func Classify(m domain.MailMessage, addresses []config.MailAddress, includeReceived, includeSent bool) []domain.MailMatch {
	var out []domain.MailMatch
	seen := map[string]bool{}
	add := func(address, by, value string) {
		key := address + "\x00" + by
		if !seen[key] {
			seen[key] = true
			out = append(out, domain.MailMatch{Address: address, MatchedBy: by, MatchedValue: value})
		}
	}
	for _, configured := range addresses {
		if !configured.IsEnabled() {
			continue
		}
		want := NormalizeAddress(configured.Address)
		if want == "" {
			continue
		}
		if includeSent {
			if NormalizeAddress(m.FromAddress) == want {
				add(want, "from", m.FromAddress)
			}
			if NormalizeAddress(m.SenderAddress) == want {
				add(want, "sender", m.SenderAddress)
			}
		}
		if includeReceived {
			for _, r := range m.Recipients {
				if NormalizeAddress(r.Address) == want && (r.Type == "to" || r.Type == "cc" || r.Type == "bcc") {
					add(want, r.Type, r.Address)
				}
			}
			headers := configured.Match.Headers
			if len(headers) == 0 {
				headers = []string{"to", "cc", "delivered-to", "x-original-to", "envelope-to", "x-envelope-to"}
			}
			for _, h := range m.Headers {
				if !containsFold(headers, h.Name) {
					continue
				}
				for _, part := range strings.FieldsFunc(h.Value, func(r rune) bool { return r == ',' || r == ';' }) {
					if NormalizeAddress(part) == want {
						add(want, "header", h.Name+": "+strings.TrimSpace(part))
					}
				}
			}
			for _, prefix := range configured.Match.SubjectPrefixes {
				if strings.HasPrefix(strings.ToLower(strings.TrimSpace(m.Subject)), strings.ToLower(prefix)) {
					add(want, "subject_rule", prefix)
				}
			}
		}
	}
	return out
}

func containsFold(values []string, value string) bool {
	for _, v := range values {
		if strings.EqualFold(strings.TrimSpace(v), strings.TrimSpace(value)) {
			return true
		}
	}
	return false
}
