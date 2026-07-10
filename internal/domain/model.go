package domain

import "time"

type Container struct {
	ID, Type, TeamID, ChannelID, ChatID, DisplayName, Description, WebURL string
	Enabled                                                               bool
	CreatedAt, UpdatedAt                                                  time.Time
}
type Message struct {
	ID, ContainerID, ParentMessageID, SenderID, SenderName, SenderType, BodyHTML, BodyText, MessageType, Subject, WebURL, ETag string
	CreatedAt                                                                                                                  time.Time
	ModifiedAt, DeletedAt                                                                                                      *time.Time
	RawJSON                                                                                                                    []byte
	Mentions                                                                                                                   []Mention
	Reactions                                                                                                                  []Reaction
	Attachments                                                                                                                []Attachment
}
type Mention struct {
	ID                 int
	UserID, Name, Type string
}
type Reaction struct {
	Type, UserID, UserName string
	CreatedAt              *time.Time
}
type Attachment struct {
	ID, Type, Name, ContentURL, ContentType, DriveItemID string
	RawJSON                                              []byte
}
type SearchFilter struct {
	Query                                               string
	ContainerIDs, TeamIDs, ChannelIDs, ChatIDs          []string
	Sender, Participant                                 string
	From, To                                            *time.Time
	MentionedMe, RepliesOnly, RootsOnly, IncludeDeleted bool
	Limit                                               int
}
type SearchResult struct {
	Message
	ContainerName, TeamName, Snippet string
	Score                            float64
}
