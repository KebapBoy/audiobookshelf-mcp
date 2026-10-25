package main

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

func handleUpdateAuthor(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	baseURL, token, err := getABSConfig(request)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	authorID, err := requireNonBlankString(request, "author_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	args := request.GetArguments()
	payload := map[string]interface{}{}
	for _, key := range []string{"name", "description", "asin"} {
		raw, ok := args[key]
		if !ok {
			continue
		}
		value, ok := raw.(string)
		if !ok {
			return mcp.NewToolResultError(fmt.Sprintf("%s must be a string", key)), nil
		}
		if key == "name" && strings.TrimSpace(value) == "" {
			return mcp.NewToolResultError("name must not be blank"), nil
		}
		payload[key] = value
	}
	if len(payload) == 0 {
		return mcp.NewToolResultError("pass at least one of name, description or asin"), nil
	}

	body, err := absPATCH(ctx, baseURL, token, fmt.Sprintf("/authors/%s", url.PathEscape(authorID)), payload)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(string(body)), nil
}

func handleSetAuthorImage(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	baseURL, token, err := getABSConfig(request)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	authorID, err := requireNonBlankString(request, "author_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	imageURL, err := requireNonBlankString(request, "url")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if parsed, err := url.Parse(imageURL); err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return mcp.NewToolResultError("url must be an http or https address"), nil
	}

	body, err := absPOST(ctx, baseURL, token, fmt.Sprintf("/authors/%s/image", url.PathEscape(authorID)), map[string]interface{}{"url": imageURL})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(string(body)), nil
}
