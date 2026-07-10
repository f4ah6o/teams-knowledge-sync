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
	HasImage                                                                                                                   bool
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

type MailAddress struct{ Address, Name string }
type MailFolder struct {
	ID, ParentID, DisplayName, WellKnownName string
	ChildCount, TotalCount, UnreadCount      int
	Hidden, Enabled                          bool
}
type MailRecipient struct{ Type, Address, Name, NormalizedAddress string }
type MailHeader struct{ Name, Value string }
type MailAttachment struct {
	ID, Name, ContentType, ContentID, Type string
	Size                                   int
	Inline                                 bool
	RawJSON                                []byte
}
type MailMatch struct{ Address, MatchedBy, MatchedValue string }
type MailMessage struct {
	ID, InternetMessageID, ConversationID, ConversationIndex, FolderID string
	Subject, BodyHTML, BodyText, BodyPreview, BodyContentType          string
	SenderAddress, SenderName, FromAddress, FromName                   string
	ReceivedAt, SentAt, CreatedAt, ModifiedAt                          *time.Time
	Importance, FlagStatus, WebURL, ETag                               string
	Read, Draft, HasAttachments                                        bool
	RawJSON                                                            []byte
	Recipients                                                         []MailRecipient
	Headers                                                            []MailHeader
	Attachments                                                        []MailAttachment
	Categories                                                         []string
	Matches                                                            []MailMatch
}
type MailSearchFilter struct {
	Query, Address, Sender, FolderID string
	From, To                         *time.Time
	Limit                            int
}
type MailSearchResult struct {
	MailMessage
	Snippet string
}
