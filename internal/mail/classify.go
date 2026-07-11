package mail

import (
	"strconv"
	"strings"

	"github.com/obr-grp/teams-knowledge-sync/internal/domain"
)

// NormalizeAddress reduces an address expression (display name, angle
// brackets, mailto:) to a lowercase bare address for exact comparison.
func NormalizeAddress(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, "<"); i >= 0 {
		if j := strings.Index(s[i:], ">"); j > 0 {
			s = s[i+1 : i+j]
		} else {
			s = s[i+1:]
		}
	}
	s = strings.TrimSpace(s)
	if len(s) >= 7 && strings.EqualFold(s[:7], "mailto:") {
		s = s[7:]
	}
	return strings.ToLower(strings.TrimSpace(s))
}

// SplitAddressList splits a header value that may carry several addresses.
// Commas inside quoted display names must not split.
func SplitAddressList(s string) []string {
	var out []string
	var b strings.Builder
	inQuote := false
	for _, r := range s {
		switch {
		case r == '"':
			inQuote = !inQuote
			b.WriteRune(r)
		case r == ',' && !inQuote:
			if v := strings.TrimSpace(b.String()); v != "" {
				out = append(out, v)
			}
			b.Reset()
		default:
			b.WriteRune(r)
		}
	}
	if v := strings.TrimSpace(b.String()); v != "" {
		out = append(out, v)
	}
	return out
}

// Classify returns every match between the message and the registered
// addresses, in the documented priority order: Graph recipient properties,
// then RFC headers, then subject rules. matched_by "folder_rule" is reserved
// for future folder-based rules.
func Classify(m *domain.MailMessage, regs []domain.RegisteredAddress) []domain.AddressMatch {
	var out []domain.AddressMatch
	for _, reg := range regs {
		if !reg.Enabled {
			continue
		}
		addr := NormalizeAddress(reg.Address)
		if addr == "" {
			continue
		}
		for _, r := range m.Recipients {
			if r.Normalized == addr && r.Type != "reply_to" {
				out = append(out, domain.AddressMatch{RegisteredID: reg.ID, MatchedBy: r.Type, MatchedValue: r.Address})
			}
		}
		if NormalizeAddress(m.FromAddress) == addr {
			out = append(out, domain.AddressMatch{RegisteredID: reg.ID, MatchedBy: "from", MatchedValue: m.FromAddress})
		}
		if NormalizeAddress(m.SenderAddress) == addr {
			out = append(out, domain.AddressMatch{RegisteredID: reg.ID, MatchedBy: "sender", MatchedValue: m.SenderAddress})
		}
		for _, h := range m.Headers {
			if !headerListed(h.Name, reg.Headers) {
				continue
			}
			for _, v := range SplitAddressList(h.Value) {
				if NormalizeAddress(v) == addr {
					out = append(out, domain.AddressMatch{RegisteredID: reg.ID, MatchedBy: "header", MatchedValue: h.Name})
					break
				}
			}
		}
		subject := strings.TrimSpace(m.Subject)
		for _, p := range reg.SubjectPrefixes {
			if p != "" && len(subject) >= len(p) && strings.EqualFold(subject[:len(p)], p) {
				out = append(out, domain.AddressMatch{RegisteredID: reg.ID, MatchedBy: "subject_rule", MatchedValue: p})
			}
		}
	}
	return dedupeMatches(out)
}
func headerListed(name string, headers []string) bool {
	for _, h := range headers {
		if strings.EqualFold(h, name) {
			return true
		}
	}
	return false
}
func dedupeMatches(in []domain.AddressMatch) []domain.AddressMatch {
	seen := map[string]bool{}
	out := in[:0]
	for _, m := range in {
		k := strings.Join([]string{strconv.FormatInt(m.RegisteredID, 10), m.MatchedBy, m.MatchedValue}, "\x00")
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, m)
	}
	return out
}
