package mcpserver

import (
	"context"
	"fmt"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/obr-grp/teams-knowledge-sync/internal/domain"
	"github.com/obr-grp/teams-knowledge-sync/internal/store"
	syncservice "github.com/obr-grp/teams-knowledge-sync/internal/sync"
	"time"
)

type SearchInput struct {
	Query string `json:"query"`
	Team  string `json:"team,omitempty"`
	From  string `json:"from,omitempty"`
	To    string `json:"to,omitempty"`
	Limit int    `json:"limit,omitempty"`
}
type SearchOutput struct {
	Results []domain.SearchResult `json:"results"`
}

type URLInput struct {
	URL string `json:"url"`
}
type OpenInput struct {
	MessageID   string `json:"message_id"`
	ContainerID string `json:"container_id"`
}

func Run(ctx context.Context, s *store.Store, syncer *syncservice.Service) error {
	server := mcp.NewServer(&mcp.Implementation{Name: "teams-knowledge", Version: "0.1.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "search_messages", Description: "Search locally synchronized Microsoft Teams messages."}, func(ctx context.Context, _ *mcp.CallToolRequest, in SearchInput) (*mcp.CallToolResult, SearchOutput, error) {
		f := domain.SearchFilter{Query: in.Query, Limit: in.Limit}
		if in.Team != "" {
			f.TeamIDs = []string{in.Team}
		}
		if in.From != "" {
			t, e := time.Parse(time.RFC3339, in.From)
			if e != nil {
				return nil, SearchOutput{}, fmt.Errorf("from: %w", e)
			}
			f.From = &t
		}
		if in.To != "" {
			t, e := time.Parse(time.RFC3339, in.To)
			if e != nil {
				return nil, SearchOutput{}, fmt.Errorf("to: %w", e)
			}
			f.To = &t
		}
		r, e := s.Search(ctx, f)
		return nil, SearchOutput{Results: r}, e
	})
	mcp.AddTool(server, &mcp.Tool{Name: "fetch_message", Description: "Fetch a Teams message URL through Microsoft Graph and save it locally."}, func(ctx context.Context, _ *mcp.CallToolRequest, in URLInput) (*mcp.CallToolResult, SearchOutput, error) {
		if in.URL == "" {
			return nil, SearchOutput{}, fmt.Errorf("url is required")
		}
		m, e := syncer.FetchURL(ctx, in.URL)
		if e != nil {
			return nil, SearchOutput{}, e
		}
		return nil, SearchOutput{Results: []domain.SearchResult{{Message: m}}}, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "open_in_teams", Description: "Return the saved Teams URL for a message."}, func(ctx context.Context, _ *mcp.CallToolRequest, in OpenInput) (*mcp.CallToolResult, map[string]string, error) {
		if in.MessageID == "" || in.ContainerID == "" {
			return nil, nil, fmt.Errorf("message_id and container_id are required")
		}
		m, e := s.GetMessage(ctx, in.ContainerID, in.MessageID)
		if e != nil {
			return nil, nil, e
		}
		return nil, map[string]string{"url": m.WebURL}, nil
	})
	return server.Run(ctx, &mcp.StdioTransport{})
}
