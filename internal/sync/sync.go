package sync

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/obr-grp/teams-knowledge-sync/internal/domain"
	"github.com/obr-grp/teams-knowledge-sync/internal/graph"
	"github.com/obr-grp/teams-knowledge-sync/internal/store"
	"github.com/obr-grp/teams-knowledge-sync/internal/teamsurl"
	"github.com/obr-grp/teams-knowledge-sync/internal/text"
	"log"
	"net/http"
	"strings"
	"time"
)

type Service struct {
	Graph         *graph.Client
	Store         *store.Store
	ExcludedChats map[string]bool
}
type resource struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
	WebURL      string `json:"webUrl"`
	ChatType    string `json:"chatType"`
	Topic       string `json:"topic"`
}

func (s Service) SyncTeam(ctx context.Context, teamID string) error {
	var team resource
	if e := s.Graph.Do(ctx, http.MethodGet, "teams/"+graph.Escape(teamID), nil, &team); e != nil {
		return e
	}
	return s.Graph.Page(ctx, "teams/"+graph.Escape(teamID)+"/channels", func(raw json.RawMessage) error {
		var ch resource
		if e := json.Unmarshal(raw, &ch); e != nil {
			return e
		}
		c := domain.Container{ID: "channel:" + teamID + ":" + ch.ID, Type: "team_channel", TeamID: teamID, ChannelID: ch.ID, DisplayName: team.DisplayName + " / " + ch.DisplayName, Description: ch.Description, WebURL: ch.WebURL, Enabled: true}
		if e := s.Store.UpsertContainer(ctx, c); e != nil {
			return e
		}
		return s.SyncChannel(ctx, c)
	})
}
func (s Service) SyncChats(ctx context.Context) error {
	var failures []error
	return s.Graph.Page(ctx, "me/chats", func(raw json.RawMessage) error {
		var ch resource
		if e := json.Unmarshal(raw, &ch); e != nil {
			return e
		}
		typ := map[string]string{"oneOnOne": "one_on_one_chat", "group": "group_chat", "meeting": "meeting_chat"}[ch.ChatType]
		if typ == "" {
			typ = "group_chat"
		}
		if s.ExcludedChats[ch.ID] {
			return nil
		}
		excluded, err := s.Store.IsChatExcluded(ctx, ch.ID)
		if err != nil {
			return err
		}
		if excluded {
			return nil
		}
		name := ch.Topic
		if name == "" {
			name = ch.ID
		}
		c := domain.Container{ID: "chat:" + ch.ID, Type: typ, ChatID: ch.ID, DisplayName: name, WebURL: ch.WebURL, Enabled: true}
		if e := s.Store.UpsertContainer(ctx, c); e != nil {
			return e
		}
		if e := s.SyncChat(ctx, c); e != nil {
			if strings.Contains(e.Error(), "InsufficientPrivileges") {
				if markErr := s.Store.ExcludeChat(ctx, ch.ID, "InsufficientPrivileges"); markErr != nil {
					return markErr
				}
				log.Printf("chat automatically excluded: %s (%s)", ch.ID, typ)
				return nil
			}
			failures = append(failures, fmt.Errorf("%s (%s): %w", ch.ID, typ, e))
			log.Printf("chat sync failed: %v", failures[len(failures)-1])
		}
		return nil
	})
}
func (s Service) SyncChannel(ctx context.Context, c domain.Container) error {
	return s.Graph.Page(ctx, "teams/"+graph.Escape(c.TeamID)+"/channels/"+graph.Escape(c.ChannelID)+"/messages?$expand=replies", func(raw json.RawMessage) error {
		m, e := message(raw, c.ID, "")
		if e != nil {
			return e
		}
		if e = s.Store.UpsertMessage(ctx, m); e != nil {
			return e
		}
		var v struct {
			Replies []json.RawMessage `json:"replies"`
		}
		_ = json.Unmarshal(raw, &v)
		for _, reply := range v.Replies {
			r, e := message(reply, c.ID, m.ID)
			if e != nil {
				return e
			}
			if e = s.Store.UpsertMessage(ctx, r); e != nil {
				return e
			}
		}
		return nil
	})
}
func (s Service) SyncChat(ctx context.Context, c domain.Container) error {
	return s.Graph.Page(ctx, "me/chats/"+graph.Escape(c.ChatID)+"/messages", func(raw json.RawMessage) error {
		m, e := message(raw, c.ID, "")
		if e != nil {
			return e
		}
		return s.Store.UpsertMessage(ctx, m)
	})
}
func (s Service) FetchURL(ctx context.Context, rawURL string) (domain.Message, error) {
	u, err := teamsurl.Parse(rawURL)
	if err != nil {
		return domain.Message{}, err
	}
	var c domain.Container
	if u.Kind == teamsurl.Channel {
		c, err = s.Store.FindChannel(ctx, u.TeamID, u.ChannelID)
		if err == sql.ErrNoRows {
			c, err = s.ensureChannel(ctx, u.TeamID, u.ChannelID)
		}
	} else {
		c, err = s.Store.FindChat(ctx, u.ChatID)
		if err == sql.ErrNoRows {
			c, err = s.ensureChat(ctx, u.ChatID)
		}
	}
	if err != nil {
		return domain.Message{}, err
	}
	path := ""
	if u.Kind == teamsurl.Channel {
		if u.ParentMessageID != "" && u.ParentMessageID != u.MessageID {
			path = "teams/" + graph.Escape(u.TeamID) + "/channels/" + graph.Escape(u.ChannelID) + "/messages/" + graph.Escape(u.ParentMessageID) + "/replies/" + graph.Escape(u.MessageID)
		} else {
			path = "teams/" + graph.Escape(u.TeamID) + "/channels/" + graph.Escape(u.ChannelID) + "/messages/" + graph.Escape(u.MessageID)
		}
	} else {
		path = "me/chats/" + graph.Escape(u.ChatID) + "/messages/" + graph.Escape(u.MessageID)
	}
	var b json.RawMessage
	if err = s.Graph.Do(ctx, http.MethodGet, path, nil, &b); err != nil {
		return domain.Message{}, fmt.Errorf("fetch %s: %w", u.Kind, err)
	}
	m, err := Message(b, c.ID, u.ParentMessageID)
	if err != nil {
		return domain.Message{}, err
	}
	if m.WebURL == "" {
		m.WebURL = rawURL
	}
	if err = s.Store.UpsertMessage(ctx, m); err != nil {
		return domain.Message{}, err
	}
	if u.Kind == teamsurl.Channel && (u.ParentMessageID == "" || u.ParentMessageID == u.MessageID) {
		repliesPath := "teams/" + graph.Escape(u.TeamID) + "/channels/" + graph.Escape(u.ChannelID) + "/messages/" + graph.Escape(u.MessageID) + "/replies"
		if err = s.Graph.Page(ctx, repliesPath, func(replyRaw json.RawMessage) error {
			reply, parseErr := Message(replyRaw, c.ID, m.ID)
			if parseErr != nil {
				return parseErr
			}
			return s.Store.UpsertMessage(ctx, reply)
		}); err != nil {
			return domain.Message{}, fmt.Errorf("fetch channel thread replies: %w", err)
		}
	}
	return m, nil
}
func (s Service) ensureChannel(ctx context.Context, teamID, channelID string) (domain.Container, error) {
	var team, ch resource
	if e := s.Graph.Do(ctx, http.MethodGet, "teams/"+graph.Escape(teamID), nil, &team); e != nil {
		return domain.Container{}, e
	}
	if e := s.Graph.Do(ctx, http.MethodGet, "teams/"+graph.Escape(teamID)+"/channels/"+graph.Escape(channelID), nil, &ch); e != nil {
		return domain.Container{}, e
	}
	c := domain.Container{ID: "channel:" + teamID + ":" + channelID, Type: "team_channel", TeamID: teamID, ChannelID: channelID, DisplayName: team.DisplayName + " / " + ch.DisplayName, Description: ch.Description, WebURL: ch.WebURL, Enabled: true}
	return c, s.Store.UpsertContainer(ctx, c)
}
func (s Service) ensureChat(ctx context.Context, chatID string) (domain.Container, error) {
	var ch resource
	if e := s.Graph.Do(ctx, http.MethodGet, "me/chats/"+graph.Escape(chatID), nil, &ch); e != nil {
		return domain.Container{}, e
	}
	typ := map[string]string{"oneOnOne": "one_on_one_chat", "group": "group_chat", "meeting": "meeting_chat"}[ch.ChatType]
	if typ == "" {
		typ = "group_chat"
	}
	name := ch.Topic
	if name == "" {
		name = chatID
	}
	c := domain.Container{ID: "chat:" + chatID, Type: typ, ChatID: chatID, DisplayName: name, WebURL: ch.WebURL, Enabled: true}
	return c, s.Store.UpsertContainer(ctx, c)
}
func Message(raw json.RawMessage, containerID, parent string) (domain.Message, error) {
	var v struct {
		ID          string `json:"id"`
		Created     string `json:"createdDateTime"`
		Modified    string `json:"lastModifiedDateTime"`
		Deleted     string `json:"deletedDateTime"`
		ETag        string `json:"etag"`
		WebURL      string `json:"webUrl"`
		Subject     string `json:"subject"`
		MessageType string `json:"messageType"`
		Body        struct {
			Content string `json:"content"`
		} `json:"body"`
		From struct {
			User struct {
				ID          string `json:"id"`
				DisplayName string `json:"displayName"`
			} `json:"user"`
		} `json:"from"`
	}
	if e := json.Unmarshal(raw, &v); e != nil {
		return domain.Message{}, e
	}
	created, e := time.Parse(time.RFC3339, v.Created)
	if e != nil {
		return domain.Message{}, fmt.Errorf("message %s created: %w", v.ID, e)
	}
	m := domain.Message{ID: v.ID, ContainerID: containerID, ParentMessageID: parent, SenderID: v.From.User.ID, SenderName: v.From.User.DisplayName, BodyHTML: v.Body.Content, BodyText: text.PlainHTML(v.Body.Content), Subject: v.Subject, MessageType: v.MessageType, WebURL: v.WebURL, CreatedAt: created, ETag: v.ETag, RawJSON: raw}
	if v.Modified != "" {
		t, _ := time.Parse(time.RFC3339, v.Modified)
		m.ModifiedAt = &t
	}
	if v.Deleted != "" {
		t, _ := time.Parse(time.RFC3339, v.Deleted)
		m.DeletedAt = &t
		m.BodyHTML = ""
		m.BodyText = ""
	}
	return m, nil
}
func message(raw json.RawMessage, containerID, parent string) (domain.Message, error) {
	return Message(raw, containerID, parent)
}
