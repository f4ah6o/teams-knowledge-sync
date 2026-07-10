package teamsurl

import (
	"fmt"
	"net/url"
	"strings"
)

type Kind string

const (
	Channel Kind = "channel"
	Chat    Kind = "chat"
)

type MessageURL struct {
	Raw                                                   string
	Kind                                                  Kind
	TeamID, ChannelID, ChatID, MessageID, ParentMessageID string
}

func Parse(raw string) (MessageURL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return MessageURL{}, fmt.Errorf("invalid Teams URL: %w", err)
	}
	if u.Scheme != "https" || !strings.EqualFold(u.Host, "teams.microsoft.com") {
		return MessageURL{}, fmt.Errorf("URL must use https://teams.microsoft.com")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 4 || parts[0] != "l" {
		return MessageURL{}, fmt.Errorf("unsupported Teams URL path")
	}
	m := MessageURL{Raw: raw, MessageID: parts[len(parts)-1]}
	if m.MessageID == "" {
		return MessageURL{}, fmt.Errorf("message ID is missing")
	}
	for _, p := range parts {
		if strings.Contains(p, "@thread.tacv2") {
			m.ChannelID, err = url.PathUnescape(p)
			if err != nil {
				return MessageURL{}, err
			}
			break
		}
	}
	q := u.Query()
	m.TeamID = q.Get("groupId")
	m.ParentMessageID = q.Get("parentMessageId")
	if strings.HasPrefix(parts[1], "message") && m.ChannelID != "" {
		if m.TeamID == "" {
			return MessageURL{}, fmt.Errorf("groupId is required")
		}
		m.Kind = Channel
		return m, nil
	}
	if parts[1] == "chat" && len(parts) >= 4 {
		m.Kind = Chat
		m.ChatID, err = url.PathUnescape(parts[2])
		if err != nil {
			return MessageURL{}, err
		}
		if m.ChatID == "" {
			return MessageURL{}, fmt.Errorf("chat ID is missing")
		}
		return m, nil
	}
	return MessageURL{}, fmt.Errorf("unsupported Teams message URL")
}
