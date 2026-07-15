package mail

import (
	"testing"

	"github.com/obr-grp/teams-knowledge-sync/internal/domain"
)

func TestNormalizeAddress(t *testing.T) {
	cases := map[string]string{
		"  User@Example.COM  ":                      "user@example.com",
		"Foo Bar <User@example.com>":                "user@example.com",
		"\"Bar, Foo\" <user@example.com>":           "user@example.com",
		"mailto:User@Example.com":                   "user@example.com",
		"Foo <mailto:User@Example.com>":             "user@example.com",
		"MAILTO:user@example.com":                   "user@example.com",
		"":                                          "",
		"user@example.com":                          "user@example.com",
		"External User <a@b.example> <c@d.example>": "c@d.example",
	}
	for in, want := range cases {
		if got := NormalizeAddress(in); got != want {
			t.Errorf("NormalizeAddress(%q)=%q want %q", in, got, want)
		}
	}
}

func TestSplitAddressList(t *testing.T) {
	got := SplitAddressList(`"Bar, Foo" <a@x.example>, b@y.example , <c@z.example>`)
	if len(got) != 3 {
		t.Fatalf("got %v", got)
	}
	if NormalizeAddress(got[0]) != "a@x.example" || NormalizeAddress(got[1]) != "b@y.example" || NormalizeAddress(got[2]) != "c@z.example" {
		t.Fatalf("got %v", got)
	}
}

func regs() []domain.RegisteredAddress {
	return []domain.RegisteredAddress{
		{ID: 1, Address: "me@example.com", Enabled: true, Headers: []string{"To", "Cc", "Delivered-To"}},
		{ID: 2, Address: "ml@example.com", Enabled: true, Headers: []string{"Delivered-To"}, SubjectPrefixes: []string{"[ml]"}},
		{ID: 3, Address: "off@example.com", Enabled: false},
	}
}

func TestClassifyRecipients(t *testing.T) {
	m := domain.MailMessage{Recipients: []domain.MailRecipient{
		{Type: "to", Address: "Me <me@example.com>", Normalized: "me@example.com"},
		{Type: "cc", Address: "me@example.com", Normalized: "me@example.com"},
		{Type: "reply_to", Address: "me@example.com", Normalized: "me@example.com"},
	}}
	got := Classify(&m, regs())
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if got[0].MatchedBy != "to" || got[0].RegisteredID != 1 || got[1].MatchedBy != "cc" {
		t.Fatalf("got %+v", got)
	}
}

func TestClassifyHeaderOnly(t *testing.T) {
	m := domain.MailMessage{
		Recipients: []domain.MailRecipient{{Type: "to", Address: "list@example.com", Normalized: "list@example.com"}},
		Headers:    []domain.MailHeader{{Name: "delivered-to", Value: "ML Member <ml@example.com>, other@example.com"}},
	}
	got := Classify(&m, regs())
	if len(got) != 1 || got[0].MatchedBy != "header" || got[0].RegisteredID != 2 || got[0].MatchedValue != "delivered-to" {
		t.Fatalf("got %+v", got)
	}
}

func TestClassifyHeaderNotConfigured(t *testing.T) {
	m := domain.MailMessage{Headers: []domain.MailHeader{{Name: "X-Envelope-To", Value: "me@example.com"}}}
	if got := Classify(&m, regs()); len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestClassifySubjectPrefix(t *testing.T) {
	m := domain.MailMessage{Subject: "  [ML] weekly report"}
	got := Classify(&m, regs())
	if len(got) != 1 || got[0].MatchedBy != "subject_rule" || got[0].RegisteredID != 2 || got[0].MatchedValue != "[ml]" {
		t.Fatalf("got %+v", got)
	}
}

func TestClassifySent(t *testing.T) {
	m := domain.MailMessage{FromAddress: "Me <me@example.com>", SenderAddress: "me@example.com"}
	got := Classify(&m, regs())
	if len(got) != 2 || got[0].MatchedBy != "from" || got[1].MatchedBy != "sender" {
		t.Fatalf("got %+v", got)
	}
}

func TestClassifyDisabledIgnored(t *testing.T) {
	m := domain.MailMessage{Recipients: []domain.MailRecipient{{Type: "to", Normalized: "off@example.com"}}}
	if got := Classify(&m, regs()); len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestClassifyDedupes(t *testing.T) {
	m := domain.MailMessage{
		Recipients: []domain.MailRecipient{{Type: "to", Address: "me@example.com", Normalized: "me@example.com"}},
		Headers:    []domain.MailHeader{{Name: "To", Value: "me@example.com"}, {Name: "Cc", Value: "me@example.com"}},
	}
	got := Classify(&m, regs())
	if len(got) != 3 {
		t.Fatalf("got %+v", got)
	}
}
