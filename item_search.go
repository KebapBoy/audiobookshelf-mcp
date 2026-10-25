package main

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// handleSearchBooks returns the matches a metadata provider finds, with every
// field the provider offers, without changing any library item. The results
// can then be judged and saved with update_item_media and set_item_cover.
func handleSearchBooks(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	baseURL, token, err := getABSConfig(request)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	title := strings.TrimSpace(request.GetString("title", ""))
	itemID := strings.TrimSpace(request.GetString("item_id", ""))
	if title == "" && itemID == "" {
		return mcp.NewToolResultError("pass title, or item_id to search for the item's own title and author"), nil
	}

	values := url.Values{}
	if title != "" {
		values.Set("title", title)
	}
	for param, key := range map[string]string{"author": "author", "provider": "provider", "item_id": "id"} {
		if value := strings.TrimSpace(request.GetString(param, "")); value != "" {
			values.Set(key, value)
		}
	}

	body, err := absGET(ctx, baseURL, token, "/search/books?"+values.Encode())
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(string(body)), nil
}

func handleSetItemCover(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	baseURL, token, err := getABSConfig(request)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	itemID, err := requireNonBlankString(request, "item_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	coverURL, err := requireNonBlankString(request, "url")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if parsed, err := url.Parse(coverURL); err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return mcp.NewToolResultError("url must be an http or https address"), nil
	}

	body, err := absPOST(ctx, baseURL, token, fmt.Sprintf("/items/%s/cover", url.PathEscape(itemID)), map[string]interface{}{"url": coverURL})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(string(body)), nil
}
