package teamsurl

import "testing"

func TestParseChannelURL(t *testing.T) {
	u, err := Parse("https://teams.microsoft.com/l/message/19:f64d4696d0184edabf879df27151cafc@thread.tacv2/1783665623604?tenantId=t&groupId=e4164c1b-1a38-43b2-a43c-490e9d0fe597&parentMessageId=1783665623604")
	if err != nil {
		t.Fatal(err)
	}
	if u.Kind != Channel || u.TeamID != "e4164c1b-1a38-43b2-a43c-490e9d0fe597" || u.ChannelID != "19:f64d4696d0184edabf879df27151cafc@thread.tacv2" || u.MessageID != "1783665623604" {
		t.Fatalf("unexpected URL: %+v", u)
	}
}
func TestParseReplyAndChat(t *testing.T) {
	r, err := Parse("https://teams.microsoft.com/l/message/19%3Af64%40thread.tacv2/2?groupId=t&parentMessageId=1")
	if err != nil || r.ParentMessageID != "1" {
		t.Fatalf("reply: %+v %v", r, err)
	}
	c, err := Parse("https://teams.microsoft.com/l/chat/19%3Aabc%40thread.v2/2")
	if err != nil || c.Kind != Chat || c.ChatID != "19:abc@thread.v2" {
		t.Fatalf("chat: %+v %v", c, err)
	}
}
func TestParseRejectsForeignURL(t *testing.T) {
	if _, err := Parse("https://example.com/l/message/x/1?groupId=t"); err == nil {
		t.Fatal("expected rejection")
	}
}
