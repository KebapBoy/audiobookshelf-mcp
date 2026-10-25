package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

var httpClient = &http.Client{
	Timeout: 10 * time.Second,
}

func getEnvOrParam(paramValue, envKey string) string {
	if paramValue != "" {
		return paramValue
	}
	return os.Getenv(envKey)
}

func normalizeABSBaseURL(rawBaseURL string) (string, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(rawBaseURL), "/")
	baseURL = strings.TrimSuffix(baseURL, "/api")

	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("base_url must be an absolute http(s) URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("base_url must use http or https")
	}

	return baseURL, nil
}

func getABSAuthConfig(request mcp.CallToolRequest) (baseURL, token string, err error) {
	baseURLParam := request.GetString("base_url", "")
	tokenParam := request.GetString("token", "")

	baseURL = getEnvOrParam(baseURLParam, "ABS_BASE_URL")
	token = getEnvOrParam(tokenParam, "ABS_API_KEY")

	if baseURL == "" {
		return "", "", fmt.Errorf("base_url parameter or ABS_BASE_URL environment variable is required")
	}
	if token == "" {
		return "", "", fmt.Errorf("token parameter or ABS_API_KEY environment variable is required")
	}

	baseURL, err = normalizeABSBaseURL(baseURL)
	if err != nil {
		return "", "", err
	}

	return baseURL, token, nil
}

func getABSConfig(request mcp.CallToolRequest) (baseURL, token string, err error) {
	baseURL, token, err = getABSAuthConfig(request)
	if err != nil {
		return "", "", err
	}

	return baseURL + "/api", token, nil
}

// Helper to add base_url and token parameters to a tool
func withABSAuth() []mcp.ToolOption {
	return []mcp.ToolOption{
		mcp.WithString("base_url",
			mcp.Description("Audiobookshelf server URL without /api, e.g. https://abs.example.com or https://abs.example.com/abs (defaults to ABS_BASE_URL env var)"),
		),
		mcp.WithString("token",
			mcp.Description("Bearer token used to authenticate with Audiobookshelf (defaults to ABS_API_KEY env var)"),
		),
	}
}

// Helper to create a simple list/get tool pair
func createSimpleGETHandler(path string) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		baseURL, token, err := getABSConfig(request)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		body, err := absGET(ctx, baseURL, token, path)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		return mcp.NewToolResultText(string(body)), nil
	}
}

// Helper to create a simple POST tool handler without a request body
func createSimplePOSTHandler(path string) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		baseURL, token, err := getABSConfig(request)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		body, err := absPOST(ctx, baseURL, token, path, nil)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		return mcp.NewToolResultText(string(body)), nil
	}
}

// Helper to create a GET handler for root-level endpoints (without /api prefix)
func createRootGETHandler(path string) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		baseURL, token, err := getABSAuthConfig(request)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		body, err := absGET(ctx, baseURL, token, path)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		return mcp.NewToolResultText(string(body)), nil
	}
}

// Helper to create a GET handler with an ID parameter
func createGETByIDHandler(pathTemplate, idParamName string) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		baseURL, token, err := getABSConfig(request)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		id, err := request.RequireString(idParamName)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		body, err := absGET(ctx, baseURL, token, fmt.Sprintf(pathTemplate, id))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		return mcp.NewToolResultText(string(body)), nil
	}
}

// Helper to create a GET handler with an ID parameter that can return MCP image content.
func createGETByIDImageHandler(pathTemplate, idParamName string) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		baseURL, token, err := getABSConfig(request)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		id, err := request.RequireString(idParamName)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		response, err := absGETRaw(ctx, baseURL, token, fmt.Sprintf(pathTemplate, id))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		return newBinaryAwareToolResult(response), nil
	}
}

// Helper to create a GET handler with ID and optional sub-resource
func createGETByIDWithSubResourceHandler(basePath, idParamName string, subResources []string) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return createGETByIDWithBinarySubResourceHandler(basePath, idParamName, subResources, nil)
}

// Helper to create a GET handler with ID and optional sub-resource, with optional binary image sub-resources.
func createGETByIDWithBinarySubResourceHandler(basePath, idParamName string, subResources []string, binarySubResources map[string]bool) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		baseURL, token, err := getABSConfig(request)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		id, err := request.RequireString(idParamName)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		// Build the path - check if any sub-resource is requested
		path := fmt.Sprintf(basePath, id)
		binaryResult := false
		for _, subResource := range subResources {
			if request.GetBool(subResource, false) {
				path = fmt.Sprintf("%s/%s", path, subResource)
				binaryResult = binarySubResources[subResource]
				break // Only one sub-resource at a time
			}
		}

		if binaryResult {
			response, err := absGETRaw(ctx, baseURL, token, path)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return newBinaryAwareToolResult(response), nil
		}

		body, err := absGET(ctx, baseURL, token, path)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		return mcp.NewToolResultText(string(body)), nil
	}
}

func createLibraryHandler() func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		baseURL, token, err := getABSConfig(request)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		libraryID, err := request.RequireString("library_id")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		path := fmt.Sprintf("/libraries/%s", libraryID)
		hasSubResource := false
		for _, subResource := range []string{
			"items",
			"authors",
			"series",
			"collections",
			"playlists",
			"personalized",
			"filterdata",
			"stats",
		} {
			if request.GetBool(subResource, false) {
				path = fmt.Sprintf("%s/%s", path, subResource)
				hasSubResource = true
				break
			}
		}

		if !hasSubResource && request.GetBool("search", false) {
			query := strings.TrimSpace(request.GetString("query", ""))
			if query == "" {
				return mcp.NewToolResultError("query is required when search is true"), nil
			}

			values := url.Values{}
			values.Set("q", query)
			if limit := request.GetFloat("limit", 0); limit > 0 {
				values.Set("limit", strconv.FormatFloat(limit, 'f', -1, 64))
			}
			path = fmt.Sprintf("/libraries/%s/search?%s", libraryID, values.Encode())
			hasSubResource = true
		}

		if !hasSubResource {
			for _, subResource := range []string{
				"episode-downloads",
				"recent-episodes",
			} {
				if request.GetBool(subResource, false) {
					path = fmt.Sprintf("%s/%s", path, subResource)
					break
				}
			}
		}

		body, err := absGET(ctx, baseURL, token, path)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		return mcp.NewToolResultText(string(body)), nil
	}
}

type absResponse struct {
	Body        []byte
	ContentType string
}

func absGET(ctx context.Context, baseURL, token, path string) ([]byte, error) {
	response, err := absGETRaw(ctx, baseURL, token, path)
	if err != nil {
		return nil, err
	}
	return response.Body, nil
}

func absGETRaw(ctx context.Context, baseURL, token, path string) (*absResponse, error) {
	fullURL := strings.TrimSuffix(baseURL, "/") + path

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call ABS API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ABS API returned %s: %s", resp.Status, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	return &absResponse{
		Body:        body,
		ContentType: resp.Header.Get("Content-Type"),
	}, nil
}

func newBinaryAwareToolResult(response *absResponse) *mcp.CallToolResult {
	mimeType := imageMIMEType(response.ContentType, response.Body)
	if mimeType == "" {
		return mcp.NewToolResultText(string(response.Body))
	}

	return mcp.NewToolResultImage("Image response", base64.StdEncoding.EncodeToString(response.Body), mimeType)
}

func imageMIMEType(contentType string, body []byte) string {
	mimeType := strings.TrimSpace(contentType)
	if mimeType != "" {
		if parsed, _, err := mime.ParseMediaType(mimeType); err == nil {
			mimeType = parsed
		} else if beforeParams, _, ok := strings.Cut(mimeType, ";"); ok {
			mimeType = strings.TrimSpace(beforeParams)
		}
	}
	if mimeType == "" {
		mimeType = http.DetectContentType(body)
	}
	mimeType = strings.ToLower(mimeType)
	if !strings.HasPrefix(mimeType, "image/") {
		return ""
	}
	return mimeType
}

func absPOST(ctx context.Context, baseURL, token, path string, payload interface{}) ([]byte, error) {
	fullURL := strings.TrimSuffix(baseURL, "/") + path

	var bodyReader io.Reader
	if payload != nil {
		jsonData, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("marshal payload: %w", err)
		}
		bodyReader = bytes.NewBuffer(jsonData)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fullURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call ABS API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ABS API returned %s: %s", resp.Status, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	return body, nil
}

func absPATCH(ctx context.Context, baseURL, token, path string, payload interface{}) ([]byte, error) {
	fullURL := strings.TrimSuffix(baseURL, "/") + path

	var bodyReader io.Reader
	if payload != nil {
		jsonData, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("marshal payload: %w", err)
		}
		bodyReader = bytes.NewBuffer(jsonData)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, fullURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call ABS API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ABS API returned %s: %s", resp.Status, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	return body, nil
}

func absDELETE(ctx context.Context, baseURL, token, path string) ([]byte, error) {
	fullURL := strings.TrimSuffix(baseURL, "/") + path

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call ABS API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ABS API returned %s: %s", resp.Status, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	return body, nil
}

// mediaUpdateStringFields maps tool parameter names of plain string metadata
// fields to the key Audiobookshelf expects in the metadata object.
var mediaUpdateStringFields = map[string]string{
	"title":          "title",
	"subtitle":       "subtitle",
	"description":    "description",
	"publisher":      "publisher",
	"language":       "language",
	"isbn":           "isbn",
	"asin":           "asin",
	"published_year": "publishedYear",
	"published_date": "publishedDate",
}

// mediaUpdateBoolFields maps tool parameter names of boolean metadata fields
// to the key Audiobookshelf expects in the metadata object.
var mediaUpdateBoolFields = map[string]string{
	"explicit": "explicit",
	"abridged": "abridged",
}

func stringSliceArg(args map[string]interface{}, key string) ([]string, error) {
	items, ok := args[key].([]interface{})
	if !ok {
		return nil, fmt.Errorf("%s must be an array of strings", key)
	}
	values := make([]string, 0, len(items))
	for _, item := range items {
		value, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("%s must be an array of strings", key)
		}
		values = append(values, value)
	}
	return values, nil
}

// buildMediaUpdate turns tool arguments into the {metadata, tags} payload of
// PATCH /items/{id}/media. Only fields that are present are sent, so omitted
// fields stay untouched, while an empty string or array clears the field.
func buildMediaUpdate(args map[string]interface{}) (map[string]interface{}, error) {
	metadata := map[string]interface{}{}
	payload := map[string]interface{}{}

	for param, key := range mediaUpdateStringFields {
		value, ok := args[param]
		if !ok {
			continue
		}
		text, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("%s must be a string", param)
		}
		metadata[key] = text
	}

	for param, key := range mediaUpdateBoolFields {
		value, ok := args[param]
		if !ok {
			continue
		}
		flag, ok := value.(bool)
		if !ok {
			return nil, fmt.Errorf("%s must be a boolean", param)
		}
		metadata[key] = flag
	}

	if _, ok := args["authors"]; ok {
		names, err := stringSliceArg(args, "authors")
		if err != nil {
			return nil, err
		}
		authors := make([]map[string]interface{}, 0, len(names))
		for _, name := range names {
			authors = append(authors, map[string]interface{}{"name": name})
		}
		metadata["authors"] = authors
	}

	for _, param := range []string{"narrators", "genres"} {
		if _, ok := args[param]; !ok {
			continue
		}
		values, err := stringSliceArg(args, param)
		if err != nil {
			return nil, err
		}
		metadata[param] = values
	}

	if value, ok := args["series"]; ok {
		items, ok := value.([]interface{})
		if !ok {
			return nil, fmt.Errorf("series must be an array of objects with name and optional sequence")
		}
		series := make([]map[string]interface{}, 0, len(items))
		for _, item := range items {
			entry, ok := item.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("series must be an array of objects with name and optional sequence")
			}
			name, ok := entry["name"].(string)
			if !ok || strings.TrimSpace(name) == "" {
				return nil, fmt.Errorf("each series entry requires a non-blank name")
			}
			seriesEntry := map[string]interface{}{"name": name}
			if sequence, ok := entry["sequence"]; ok {
				text, ok := sequence.(string)
				if !ok {
					return nil, fmt.Errorf("series sequence must be a string")
				}
				seriesEntry["sequence"] = text
			}
			series = append(series, seriesEntry)
		}
		metadata["series"] = series
	}

	if len(metadata) > 0 {
		payload["metadata"] = metadata
	}

	if _, ok := args["tags"]; ok {
		tags, err := stringSliceArg(args, "tags")
		if err != nil {
			return nil, err
		}
		payload["tags"] = tags
	}

	if len(payload) == 0 {
		return nil, fmt.Errorf("at least one metadata field must be provided")
	}

	return payload, nil
}

// mediaUpdateToolOptions describes the metadata fields shared by
// update_item_media. batch_update_items documents the same fields per entry.
func mediaUpdateToolOptions() []mcp.ToolOption {
	return []mcp.ToolOption{
		mcp.WithString("title", mcp.Description("Title")),
		mcp.WithString("subtitle", mcp.Description("Subtitle")),
		mcp.WithString("description", mcp.Description("Description")),
		mcp.WithString("publisher", mcp.Description("Publisher")),
		mcp.WithString("language", mcp.Description("Language")),
		mcp.WithString("isbn", mcp.Description("ISBN")),
		mcp.WithString("asin", mcp.Description("ASIN")),
		mcp.WithString("published_year", mcp.Description("Published year, e.g. \"2013\"")),
		mcp.WithString("published_date", mcp.Description("Published date")),
		mcp.WithBoolean("explicit", mcp.Description("Explicit content flag")),
		mcp.WithBoolean("abridged", mcp.Description("Abridged flag")),
		mcp.WithArray("authors", mcp.Description("Author names. Replaces the existing authors; an empty array clears them"), mcp.WithStringItems()),
		mcp.WithArray("narrators", mcp.Description("Narrator names. Replaces the existing narrators; an empty array clears them"), mcp.WithStringItems()),
		mcp.WithArray("genres", mcp.Description("Genres. Replaces the existing genres; an empty array clears them"), mcp.WithStringItems()),
		mcp.WithArray("tags", mcp.Description("Tags. Replaces the existing tags; an empty array clears them"), mcp.WithStringItems()),
		mcp.WithArray("series", mcp.Description("Series as objects with name and optional sequence. Replaces the existing series; an empty array clears them"),
			mcp.Items(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name":     map[string]interface{}{"type": "string"},
					"sequence": map[string]interface{}{"type": "string"},
				},
				"required": []string{"name"},
			})),
	}
}

func handleUpdateItemMedia(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	baseURL, token, err := getABSConfig(request)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	itemID, err := requireNonBlankString(request, "item_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	payload, err := buildMediaUpdate(request.GetArguments())
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	body, err := absPATCH(ctx, baseURL, token, fmt.Sprintf("/items/%s/media", itemID), payload)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(compactItemResponse(body)), nil
}

func handleBatchUpdateItems(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	baseURL, token, err := getABSConfig(request)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	entries, ok := request.GetArguments()["updates"].([]interface{})
	if !ok || len(entries) == 0 {
		return mcp.NewToolResultError("updates must be a non-empty array"), nil
	}

	payload := make([]map[string]interface{}, 0, len(entries))
	for i, entry := range entries {
		fields, ok := entry.(map[string]interface{})
		if !ok {
			return mcp.NewToolResultError(fmt.Sprintf("updates[%d] must be an object", i)), nil
		}
		itemID, ok := fields["item_id"].(string)
		if !ok || strings.TrimSpace(itemID) == "" {
			return mcp.NewToolResultError(fmt.Sprintf("updates[%d] requires a non-blank item_id", i)), nil
		}
		mediaPayload, err := buildMediaUpdate(fields)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("updates[%d]: %s", i, err)), nil
		}
		payload = append(payload, map[string]interface{}{
			"id":           itemID,
			"mediaPayload": mediaPayload,
		})
	}

	body, err := absPOST(ctx, baseURL, token, "/items/batch/update", payload)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(string(body)), nil
}

func handleMatchItem(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	baseURL, token, err := getABSConfig(request)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	itemID, err := requireNonBlankString(request, "item_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	payload := map[string]interface{}{}
	for _, key := range []string{"provider", "title", "author", "isbn", "asin"} {
		if value := request.GetString(key, ""); value != "" {
			payload[key] = value
		}
	}
	for param, key := range map[string]string{"override_details": "overrideDetails", "override_cover": "overrideCover"} {
		if _, ok := request.GetArguments()[param]; ok {
			payload[key] = request.GetBool(param, false)
		}
	}

	body, err := absPOST(ctx, baseURL, token, fmt.Sprintf("/items/%s/match", itemID), payload)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(compactItemResponse(body)), nil
}

// compactItemResponse reduces the {updated, libraryItem} response of the item
// update and match endpoints, which contains the whole item with every audio
// file and chapter, to its metadata and a few counts. Long texts are
// shortened. Responses of any other shape are returned unchanged.
func compactItemResponse(body []byte) string {
	var response struct {
		Updated     bool `json:"updated"`
		LibraryItem struct {
			Media struct {
				Metadata   map[string]interface{} `json:"metadata"`
				AudioFiles []json.RawMessage      `json:"audioFiles"`
				Chapters   []json.RawMessage      `json:"chapters"`
			} `json:"media"`
		} `json:"libraryItem"`
	}
	if err := json.Unmarshal(body, &response); err != nil || response.LibraryItem.Media.Metadata == nil {
		return string(body)
	}

	metadata := response.LibraryItem.Media.Metadata
	for key, value := range metadata {
		if text, ok := value.(string); ok {
			if runes := []rune(text); len(runes) > compactTextLimit {
				metadata[key] = string(runes[:compactTextLimit]) + "…"
			}
		}
	}

	compact, err := json.Marshal(map[string]interface{}{
		"updated":          response.Updated,
		"metadata":         metadata,
		"audio_file_count": len(response.LibraryItem.Media.AudioFiles),
		"chapter_count":    len(response.LibraryItem.Media.Chapters),
	})
	if err != nil {
		return string(body)
	}
	return string(compact)
}

// compactTextLimit is the number of characters kept of long metadata texts
// such as the description in compact item responses.
const compactTextLimit = 300

func handleDeleteItem(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	baseURL, token, err := getABSConfig(request)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	itemID, err := requireNonBlankString(request, "item_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	if !request.GetBool("confirm", false) {
		return mcp.NewToolResultError("confirm must be true to delete an item"), nil
	}

	path := fmt.Sprintf("/items/%s", itemID)
	if request.GetBool("hard", false) {
		path += "?hard=1"
	}

	body, err := absDELETE(ctx, baseURL, token, path)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if len(body) == 0 {
		return mcp.NewToolResultText("Item deleted"), nil
	}

	return mcp.NewToolResultText(string(body)), nil
}

func requireNonBlankString(request mcp.CallToolRequest, key string) (string, error) {
	value, err := request.RequireString(key)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("required argument %q must not be blank", key)
	}
	return value, nil
}

func handlePodcasts(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	baseURL, token, err := getABSConfig(request)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	if request.GetBool("feed", false) {
		rssFeed, err := requireNonBlankString(request, "rss_feed")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		body, err := absPOST(ctx, baseURL, token, "/podcasts/feed", map[string]interface{}{"rssFeed": rssFeed})
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		return mcp.NewToolResultText(string(body)), nil
	}

	if request.GetBool("opml", false) {
		opmlText, err := requireNonBlankString(request, "opml_text")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		body, err := absPOST(ctx, baseURL, token, "/podcasts/opml/parse", map[string]interface{}{"opmlText": opmlText})
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		return mcp.NewToolResultText(string(body)), nil
	}

	libraryID, err := requireNonBlankString(request, "library_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	body, err := absGET(ctx, baseURL, token, fmt.Sprintf("/libraries/%s/items", url.PathEscape(libraryID)))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(string(body)), nil
}

func handlePodcast(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	baseURL, token, err := getABSConfig(request)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	podcastID, err := requireNonBlankString(request, "podcast_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	escapedPodcastID := url.PathEscape(podcastID)
	path := fmt.Sprintf("/items/%s", escapedPodcastID)

	if request.GetBool("downloads", false) {
		path = fmt.Sprintf("/podcasts/%s/downloads", escapedPodcastID)
	} else if request.GetBool("search-episode", false) {
		title := strings.TrimSpace(request.GetString("title", ""))
		if title == "" {
			return mcp.NewToolResultError("title is required when search-episode is true"), nil
		}

		values := url.Values{}
		values.Set("title", title)
		path = fmt.Sprintf("/podcasts/%s/search-episode?%s", escapedPodcastID, values.Encode())
	} else if episodeID := request.GetString("episode_id", ""); episodeID != "" {
		path = fmt.Sprintf("/podcasts/%s/episode/%s", escapedPodcastID, url.PathEscape(episodeID))
	}

	body, err := absGET(ctx, baseURL, token, path)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(string(body)), nil
}

func handleCheckPodcastEpisodes(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	baseURL, token, err := getABSConfig(request)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	podcastID, err := requireNonBlankString(request, "podcast_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	path := fmt.Sprintf("/podcasts/%s/checknew", url.PathEscape(podcastID))
	if limit := request.GetFloat("limit", 0); limit > 0 {
		query := url.Values{}
		query.Set("limit", strconv.Itoa(int(limit)))
		path += "?" + query.Encode()
	}

	body, err := absGET(ctx, baseURL, token, path)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(string(body)), nil
}

func newMCPServer() *server.MCPServer {
	// Create a new MCP server
	s := server.NewMCPServer(
		"Audiobookshelf MCP Server",
		"1.0.0",
		server.WithToolCapabilities(true),
		server.WithRecovery(),
	)

	// Add ABS tools
	// Libraries tools
	librariesOpts := append(withABSAuth(), mcp.WithDescription("List Audiobookshelf libraries"))
	librariesTool := mcp.NewTool("libraries", librariesOpts...)

	libraryOpts := append(withABSAuth(),
		mcp.WithDescription("Retrieve a single Audiobookshelf library by ID, optionally with sub-resources"),
		mcp.WithString("library_id", mcp.Required(), mcp.Description("Library identifier to fetch")),
		mcp.WithBoolean("items", mcp.Description("Include all items in the library")),
		mcp.WithBoolean("authors", mcp.Description("Include all authors in the library")),
		mcp.WithBoolean("series", mcp.Description("Include all series in the library")),
		mcp.WithBoolean("collections", mcp.Description("Include all collections in the library")),
		mcp.WithBoolean("playlists", mcp.Description("Include all playlists in the library")),
		mcp.WithBoolean("personalized", mcp.Description("Include personalized view for the library")),
		mcp.WithBoolean("filterdata", mcp.Description("Include filter data for the library")),
		mcp.WithBoolean("stats", mcp.Description("Include library statistics")),
		mcp.WithBoolean("search", mcp.Description("Search the library items")),
		mcp.WithString("query", mcp.Description("Search query to use when search is true")),
		mcp.WithNumber("limit", mcp.Description("Maximum number of search results to return")),
		mcp.WithBoolean("episode-downloads", mcp.Description("Include episode downloads for the library")),
		mcp.WithBoolean("recent-episodes", mcp.Description("Include recent episodes for the library")),
	)
	libraryTool := mcp.NewTool("library", libraryOpts...)

	// Create library tool
	createLibraryOpts := append(withABSAuth(),
		mcp.WithDescription("Create a new Audiobookshelf library"),
		mcp.WithString("name", mcp.Required(), mcp.Description("Library name")),
		mcp.WithString("folders", mcp.Required(), mcp.Description("Comma-separated list of folder paths for the library")),
		mcp.WithString("media_type", mcp.Required(), mcp.Description("Media type: book or podcast")),
		mcp.WithString("icon", mcp.Description("Library icon (default: database)")),
		mcp.WithString("provider", mcp.Description("Metadata provider (default: google)")),
	)
	createLibraryTool := mcp.NewTool("create_library", createLibraryOpts...)

	// Items tools
	itemOpts := append(withABSAuth(),
		mcp.WithDescription("Retrieve a single Audiobookshelf item (audiobook or podcast) by ID, optionally with sub-resources"),
		mcp.WithString("item_id", mcp.Required(), mcp.Description("Item identifier to fetch")),
		mcp.WithBoolean("cover", mcp.Description("Return the cover image for the item as MCP image content")),
		mcp.WithBoolean("tone-object", mcp.Description("Include tone object for the item")),
	)
	itemTool := mcp.NewTool("item", itemOpts...)

	// Authors tools
	authorOpts := append(withABSAuth(),
		mcp.WithDescription("Retrieve a single Audiobookshelf author by ID"),
		mcp.WithString("author_id", mcp.Required(), mcp.Description("Author identifier to fetch")),
	)
	authorTool := mcp.NewTool("author", authorOpts...)

	// User tools
	meOpts := append(withABSAuth(),
		mcp.WithDescription("Get authenticated user information, or fetch specific user sub-resources"),
		mcp.WithBoolean("listening-sessions", mcp.Description("Get listening sessions for the user")),
		mcp.WithBoolean("listening-stats", mcp.Description("Get listening statistics for the user")),
		mcp.WithBoolean("items-in-progress", mcp.Description("Get items currently in progress for the user")),
		mcp.WithString("progress_item_id", mcp.Description("Get progress for a specific library item ID")),
		mcp.WithString("progress_episode_id", mcp.Description("Get progress for a specific episode ID (requires progress_item_id)")),
	)
	meTool := mcp.NewTool("me", meOpts...)

	// Sessions tools
	sessionsOpts := append(withABSAuth(), mcp.WithDescription("List all playback sessions"))
	sessionsTool := mcp.NewTool("sessions", sessionsOpts...)

	sessionOpts := append(withABSAuth(),
		mcp.WithDescription("Retrieve a single playback session by ID"),
		mcp.WithString("session_id", mcp.Required(), mcp.Description("Session identifier to fetch")),
	)
	sessionTool := mcp.NewTool("session", sessionOpts...)

	// Podcasts tools
	podcastsOpts := append(withABSAuth(),
		mcp.WithDescription("List podcast library items, fetch RSS feed metadata, or parse OPML text"),
		mcp.WithString("library_id", mcp.Description("Podcast library ID to list items from (required unless feed or opml is true)")),
		mcp.WithBoolean("feed", mcp.Description("Fetch podcast feed metadata using POST /podcasts/feed")),
		mcp.WithString("rss_feed", mcp.Description("RSS feed URL (required when feed is true)")),
		mcp.WithBoolean("opml", mcp.Description("Parse OPML text using POST /podcasts/opml/parse")),
		mcp.WithString("opml_text", mcp.Description("OPML XML text (required when opml is true)")),
	)
	podcastsTool := mcp.NewTool("podcasts", podcastsOpts...)

	podcastOpts := append(withABSAuth(),
		mcp.WithDescription("Retrieve a podcast library item by ID, or fetch podcast sub-resources"),
		mcp.WithString("podcast_id", mcp.Required(), mcp.Description("Podcast library item identifier to fetch")),
		mcp.WithBoolean("downloads", mcp.Description("Get downloads for the podcast")),
		mcp.WithBoolean("search-episode", mcp.Description("Search for episodes in the podcast")),
		mcp.WithString("title", mcp.Description("Episode title to search for when search-episode is true")),
		mcp.WithString("episode_id", mcp.Description("Get a specific episode by ID")),
	)
	podcastTool := mcp.NewTool("podcast", podcastOpts...)

	// Collections tools
	collectionsOpts := append(withABSAuth(), mcp.WithDescription("List all Audiobookshelf collections"))
	collectionsTool := mcp.NewTool("collections", collectionsOpts...)

	collectionOpts := append(withABSAuth(),
		mcp.WithDescription("Retrieve a single Audiobookshelf collection by ID"),
		mcp.WithString("collection_id", mcp.Required(), mcp.Description("Collection identifier to fetch")),
	)
	collectionTool := mcp.NewTool("collection", collectionOpts...)

	createCollectionOpts := append(withABSAuth(),
		mcp.WithDescription("Create a new collection"),
		mcp.WithString("library_id", mcp.Required(), mcp.Description("Library ID")),
		mcp.WithString("name", mcp.Required(), mcp.Description("Collection name")),
		mcp.WithString("description", mcp.Description("Collection description")),
	)
	createCollectionTool := mcp.NewTool("create_collection", createCollectionOpts...)

	addToCollectionOpts := append(withABSAuth(),
		mcp.WithDescription("Add a book to an existing collection"),
		mcp.WithString("collection_id", mcp.Required(), mcp.Description("Collection ID")),
		mcp.WithString("book_id", mcp.Required(), mcp.Description("Book ID to add")),
	)
	addToCollectionTool := mcp.NewTool("add_to_collection", addToCollectionOpts...)

	// Playlists tools
	playlistsOpts := append(withABSAuth(), mcp.WithDescription("List all Audiobookshelf playlists"))
	playlistsTool := mcp.NewTool("playlists", playlistsOpts...)

	playlistOpts := append(withABSAuth(),
		mcp.WithDescription("Retrieve a single Audiobookshelf playlist by ID"),
		mcp.WithString("playlist_id", mcp.Required(), mcp.Description("Playlist identifier to fetch")),
	)
	playlistTool := mcp.NewTool("playlist", playlistOpts...)

	createPlaylistOpts := append(withABSAuth(),
		mcp.WithDescription("Create a new playlist"),
		mcp.WithString("library_id", mcp.Required(), mcp.Description("Library ID")),
		mcp.WithString("name", mcp.Required(), mcp.Description("Playlist name")),
		mcp.WithString("description", mcp.Description("Playlist description")),
	)
	createPlaylistTool := mcp.NewTool("create_playlist", createPlaylistOpts...)

	addToPlaylistOpts := append(withABSAuth(),
		mcp.WithDescription("Add an item to an existing playlist"),
		mcp.WithString("playlist_id", mcp.Required(), mcp.Description("Playlist ID")),
		mcp.WithString("item_id", mcp.Required(), mcp.Description("Library item ID to add")),
		mcp.WithString("episode_id", mcp.Description("Episode ID (for podcast episodes)")),
	)
	addToPlaylistTool := mcp.NewTool("add_to_playlist", addToPlaylistOpts...)

	// Podcast check new episodes
	checkPodcastEpisodesOpts := append(withABSAuth(),
		mcp.WithDescription("Check for new episodes for a podcast"),
		mcp.WithString("podcast_id", mcp.Required(), mcp.Description("Podcast library item ID to check")),
		mcp.WithNumber("limit", mcp.Description("Maximum number of new episodes to download (Audiobookshelf defaults to 3)")),
	)
	checkPodcastEpisodesTool := mcp.NewTool("check_podcast_episodes", checkPodcastEpisodesOpts...)

	// Backup creation
	createBackupOpts := append(withABSAuth(),
		mcp.WithDescription("Create a server backup"),
	)
	createBackupTool := mcp.NewTool("create_backup", createBackupOpts...)

	// Progress tracking
	updateProgressOpts := append(withABSAuth(),
		mcp.WithDescription("Update listening progress for a media item"),
		mcp.WithString("item_id", mcp.Required(), mcp.Description("Library item ID")),
		mcp.WithNumber("progress", mcp.Required(), mcp.Description("Progress in seconds")),
		mcp.WithNumber("duration", mcp.Description("Total duration in seconds")),
		mcp.WithBoolean("is_finished", mcp.Description("Mark as finished")),
		mcp.WithString("episode_id", mcp.Description("Episode ID (for podcasts)")),
	)
	updateProgressTool := mcp.NewTool("update_progress", updateProgressOpts...)

	// Item metadata tools
	updateItemMediaOpts := append(withABSAuth(),
		mcp.WithDescription("Update the metadata of a library item. Only the supplied fields are changed"),
		mcp.WithString("item_id", mcp.Required(), mcp.Description("Library item ID")),
	)
	updateItemMediaOpts = append(updateItemMediaOpts, mediaUpdateToolOptions()...)
	updateItemMediaTool := mcp.NewTool("update_item_media", updateItemMediaOpts...)

	batchUpdateItemsOpts := append(withABSAuth(),
		mcp.WithDescription("Update the metadata of several library items in one request"),
		mcp.WithArray("updates", mcp.Required(),
			mcp.Description("One object per item with item_id plus the same metadata fields as update_item_media (title, subtitle, description, publisher, language, isbn, asin, published_year, published_date, explicit, abridged, authors, narrators, genres, tags, series)"),
			mcp.Items(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"item_id": map[string]interface{}{"type": "string"},
				},
				"required":             []string{"item_id"},
				"additionalProperties": true,
			})),
	)
	batchUpdateItemsTool := mcp.NewTool("batch_update_items", batchUpdateItemsOpts...)

	matchItemOpts := append(withABSAuth(),
		mcp.WithDescription("Match a library item against a metadata provider and apply the result. Without a provider Audiobookshelf falls back to Google Books, so pass the library's provider such as audible.de"),
		mcp.WithString("item_id", mcp.Required(), mcp.Description("Library item ID")),
		mcp.WithString("provider", mcp.Description("Metadata provider, e.g. audible.de, audible, google, openlibrary")),
		mcp.WithString("title", mcp.Description("Title to search for (defaults to the item's title)")),
		mcp.WithString("author", mcp.Description("Author to search for (defaults to the item's author)")),
		mcp.WithString("isbn", mcp.Description("ISBN to search for")),
		mcp.WithString("asin", mcp.Description("ASIN to look up directly")),
		mcp.WithBoolean("override_details", mcp.Description("Overwrite metadata fields that already have a value instead of only filling empty ones")),
		mcp.WithBoolean("override_cover", mcp.Description("Replace the existing cover with the one from the provider instead of only setting a missing cover")),
	)
	matchItemTool := mcp.NewTool("match_item", matchItemOpts...)

	searchBooksOpts := append(withABSAuth(),
		mcp.WithDescription("Search a metadata provider and return every match with all fields it offers (title, subtitle, author, narrator, series, genres, description, publisher, year, language, ASIN, ISBN, cover URL and more) without changing anything. Judge the results, then save the chosen values with update_item_media. Without a provider Audiobookshelf uses Google Books, so pass the library's provider such as audible.de"),
		mcp.WithString("title", mcp.Description("Title to search for. Required unless item_id is given")),
		mcp.WithString("author", mcp.Description("Author to search for")),
		mcp.WithString("provider", mcp.Description("Metadata provider, e.g. audible.de, audible, google, openlibrary")),
		mcp.WithString("item_id", mcp.Description("Library item ID; searches with the item's own title and author unless title or author are given")),
	)
	searchBooksTool := mcp.NewTool("search_books", searchBooksOpts...)

	setItemCoverOpts := append(withABSAuth(),
		mcp.WithDescription("Set the cover of a library item from an image URL, for example the cover URL of a search_books result. Needs a user with upload permission"),
		mcp.WithString("item_id", mcp.Required(), mcp.Description("Library item ID")),
		mcp.WithString("url", mcp.Required(), mcp.Description("http or https URL of the cover image")),
	)
	setItemCoverTool := mcp.NewTool("set_item_cover", setItemCoverOpts...)

	updateAuthorOpts := append(withABSAuth(),
		mcp.WithDescription("Update an author's name, description or Audible ASIN. Only the fields that are passed change"),
		mcp.WithString("author_id", mcp.Required(), mcp.Description("Author ID")),
		mcp.WithString("name", mcp.Description("Author name")),
		mcp.WithString("description", mcp.Description("Author description; an empty string clears it")),
		mcp.WithString("asin", mcp.Description("Audible author ASIN; an empty string clears it")),
	)
	updateAuthorTool := mcp.NewTool("update_author", updateAuthorOpts...)

	setAuthorImageOpts := append(withABSAuth(),
		mcp.WithDescription("Set the portrait of an author from an image URL. Needs a user with upload permission"),
		mcp.WithString("author_id", mcp.Required(), mcp.Description("Author ID")),
		mcp.WithString("url", mcp.Required(), mcp.Description("http or https URL of the image")),
	)
	setAuthorImageTool := mcp.NewTool("set_author_image", setAuthorImageOpts...)

	compareCoversOpts := append(withABSAuth(),
		mcp.WithDescription("Compare the current cover of a library item, in its original size, with candidate images by resolution and file size, to decide whether a provider's cover is better. Candidates must be public http or https addresses. Reads JPEG, PNG and GIF dimensions"),
		mcp.WithString("item_id", mcp.Required(), mcp.Description("Library item ID")),
		mcp.WithArray("urls", mcp.Required(), mcp.Description("One to ten candidate image URLs, for example the cover URLs of search_books results"), mcp.WithStringItems()),
	)
	compareCoversTool := mcp.NewTool("compare_covers", compareCoversOpts...)

	deleteItemOpts := append(withABSAuth(),
		mcp.WithDescription("Delete a library item. By default only the database entry is removed; a rescan can add it again while its files remain"),
		mcp.WithString("item_id", mcp.Required(), mcp.Description("Library item ID")),
		mcp.WithBoolean("confirm", mcp.Required(), mcp.Description("Must be true to delete the item")),
		mcp.WithBoolean("hard", mcp.Description("Also delete the item's files from disk. This cannot be undone")),
	)
	deleteItemTool := mcp.NewTool("delete_item", deleteItemOpts...)

	// Chapter and track tools
	searchChaptersOpts := append(withABSAuth(),
		mcp.WithDescription("Look up the chapters of an Audible audiobook by ASIN"),
		mcp.WithString("asin", mcp.Required(), mcp.Description("Audible ASIN")),
		mcp.WithString("region", mcp.Description("Audible region such as de, us, uk, fr, it, es, ca, au, jp, in (Audiobookshelf defaults to us)")),
	)
	searchChaptersTool := mcp.NewTool("search_chapters", searchChaptersOpts...)

	updateItemChaptersOpts := append(withABSAuth(),
		mcp.WithDescription("Replace the chapters of a library item"),
		mcp.WithString("item_id", mcp.Required(), mcp.Description("Library item ID")),
		mcp.WithArray("chapters", mcp.Required(),
			mcp.Description("All chapters in order, with start and end in seconds"),
			mcp.Items(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"title": map[string]interface{}{"type": "string"},
					"start": map[string]interface{}{"type": "number"},
					"end":   map[string]interface{}{"type": "number"},
				},
				"required": []string{"title", "start", "end"},
			})),
	)
	updateItemChaptersTool := mcp.NewTool("update_item_chapters", updateItemChaptersOpts...)

	applyAudibleChaptersOpts := append(withABSAuth(),
		mcp.WithDescription("Apply Audible chapters to a library item. Runs as a preview unless dry_run is false"),
		mcp.WithString("item_id", mcp.Required(), mcp.Description("Library item ID")),
		mcp.WithString("mode", mcp.Description(`"titles" keeps the existing chapter times and only takes the titles by position (default); "full" replaces the chapters with the Audible chapter times, corrected for an offset between the recording and Audible when most chapters start on file boundaries; "tracks" makes one chapter per audio file and names it after the Audible chapter at the same position, which needs as many files as chapters`)),
		mcp.WithString("asin", mcp.Description("Audible ASIN (defaults to the item's ASIN)")),
		mcp.WithString("region", mcp.Description("Audible region such as de or us (Audiobookshelf defaults to us)")),
		mcp.WithBoolean("remove_branding", mcp.Description("Remove the Audible intro and outro from the chapter times. When omitted, both variants are compared against the audio file boundaries and the one that lines up better is used; the result reports the choice")),
		mcp.WithNumber("snap_tolerance_sec", mcp.Description("In full mode, move chapter starts that lie within this many seconds of an audio file boundary onto that boundary, so a chapter does not begin with the end of the previous file (default 15, 0 disables). Chapters in the middle of a file follow the shift of the nearest chapter that snapped, which covers a constant offset or a slow drift between the recording and Audible")),
		mcp.WithBoolean("stretch_to_audio", mcp.Description(`In full mode, scale the Audible times so the Audible runtime matches the length of the audio. Use it when the recording is slightly faster or slower than Audible and no chapters line up with file boundaries to measure the offset. The result is an estimate`)),
		mcp.WithBoolean("allow_count_mismatch", mcp.Description("In titles mode, rename by position even if the chapter counts differ")),
		mcp.WithBoolean("dry_run", mcp.Description("Only show what would change (default true)")),
	)
	applyAudibleChaptersTool := mcp.NewTool("apply_audible_chapters", applyAudibleChaptersOpts...)

	reorderTracksOpts := append(withABSAuth(),
		mcp.WithDescription("Sort or reorder the audio tracks of a library item. Runs as a preview unless dry_run is false. Chapters are not changed"),
		mcp.WithString("item_id", mcp.Required(), mcp.Description("Library item ID")),
		mcp.WithString("sort_by", mcp.Description(`Sort all tracks: "filename" (natural order, 2 before 10), "track_tag" (disc and track number from the audio tags) or "filename_number" (disc and track number found in the file name)`)),
		mcp.WithBoolean("reverse", mcp.Description("Reverse the order after sorting")),
		mcp.WithArray("order", mcp.Description("File names or inodes to put first, in this order; the remaining tracks follow in their current or sorted order"), mcp.WithStringItems()),
		mcp.WithArray("exclude", mcp.Description("File names or inodes to exclude from playback"), mcp.WithStringItems()),
		mcp.WithArray("include", mcp.Description("File names or inodes of excluded tracks to include again"), mcp.WithStringItems()),
		mcp.WithBoolean("dry_run", mcp.Description("Only show what would change (default true)")),
	)
	reorderTracksTool := mcp.NewTool("reorder_tracks", reorderTracksOpts...)

	// Server status/health tools
	pingOpts := append(withABSAuth(), mcp.WithDescription("Simple health check endpoint"))
	pingTool := mcp.NewTool("ping", pingOpts...)

	healthcheckOpts := append(withABSAuth(), mcp.WithDescription("Server health verification endpoint"))
	healthcheckTool := mcp.NewTool("healthcheck", healthcheckOpts...)

	statusOpts := append(withABSAuth(), mcp.WithDescription("Get server initialization status and configuration"))
	statusTool := mcp.NewTool("status", statusOpts...)

	// Users tools
	usersOpts := append(withABSAuth(), mcp.WithDescription("List all Audiobookshelf users"))
	usersTool := mcp.NewTool("users", usersOpts...)

	usersOnlineOpts := append(withABSAuth(), mcp.WithDescription("Get currently online users"))
	usersOnlineTool := mcp.NewTool("users_online", usersOnlineOpts...)

	userOpts := append(withABSAuth(),
		mcp.WithDescription("Retrieve a single user by ID, optionally with sub-resources"),
		mcp.WithString("user_id", mcp.Required(), mcp.Description("User identifier to fetch")),
		mcp.WithBoolean("listening-sessions", mcp.Description("Get listening sessions for the user")),
		mcp.WithBoolean("listening-stats", mcp.Description("Get listening statistics for the user")),
	)
	userTool := mcp.NewTool("user", userOpts...)

	// Series tools
	seriesOpts := append(withABSAuth(),
		mcp.WithDescription("Retrieve a single series by ID"),
		mcp.WithString("series_id", mcp.Required(), mcp.Description("Series identifier to fetch")),
	)
	seriesTool := mcp.NewTool("series", seriesOpts...)

	// Author image tool
	authorImageOpts := append(withABSAuth(),
		mcp.WithDescription("Retrieve an author image by ID as MCP image content"),
		mcp.WithString("author_id", mcp.Required(), mcp.Description("Author identifier")),
	)
	authorImageTool := mcp.NewTool("author_image", authorImageOpts...)

	// Backups tools
	backupsOpts := append(withABSAuth(), mcp.WithDescription("List all server backups"))
	backupsTool := mcp.NewTool("backups", backupsOpts...)

	// Filesystem tools
	filesystemOpts := append(withABSAuth(), mcp.WithDescription("List available filesystem paths"))
	filesystemTool := mcp.NewTool("filesystem", filesystemOpts...)

	// Authorize tools
	authorizeOpts := append(withABSAuth(), mcp.WithDescription("Get authorized user and server information"))
	authorizeTool := mcp.NewTool("authorize", authorizeOpts...)

	// Tags and Genres tools
	tagsOpts := append(withABSAuth(), mcp.WithDescription("Get all library tags"))
	tagsTool := mcp.NewTool("tags", tagsOpts...)

	genresOpts := append(withABSAuth(), mcp.WithDescription("Get all available genres"))
	genresTool := mcp.NewTool("genres", genresOpts...)

	// Add ABS Libraries handlers
	s.AddTool(librariesTool, createSimpleGETHandler("/libraries"))
	s.AddTool(libraryTool, createLibraryHandler())
	s.AddTool(createLibraryTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		baseURL, token, err := getABSConfig(request)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		name, err := request.RequireString("name")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		foldersStr, err := request.RequireString("folders")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		mediaType, err := request.RequireString("media_type")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		// Parse folders from comma-separated string
		folderPaths := strings.Split(foldersStr, ",")
		folders := make([]map[string]interface{}, len(folderPaths))
		for i, path := range folderPaths {
			folders[i] = map[string]interface{}{
				"fullPath": strings.TrimSpace(path),
			}
		}

		// Build the payload with required fields
		payload := map[string]interface{}{
			"name":      name,
			"folders":   folders,
			"mediaType": mediaType,
		}

		// Add optional fields if provided
		if icon := request.GetString("icon", ""); icon != "" {
			payload["icon"] = icon
		}
		if provider := request.GetString("provider", ""); provider != "" {
			payload["provider"] = provider
		}

		body, err := absPOST(ctx, baseURL, token, "/libraries", payload)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		return mcp.NewToolResultText(string(body)), nil
	})

	// Add ABS Items handlers
	s.AddTool(itemTool, createGETByIDWithBinarySubResourceHandler("/items/%s", "item_id", []string{
		"cover",
		"tone-object",
	}, map[string]bool{"cover": true}))

	// Add ABS Authors handlers
	s.AddTool(authorTool, createGETByIDHandler("/authors/%s", "author_id"))

	// Add ABS Me handler
	s.AddTool(meTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		baseURL, token, err := getABSConfig(request)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		path := "/me"

		// Check for simple boolean sub-resources first
		if request.GetBool("listening-sessions", false) {
			path = "/me/listening-sessions"
		} else if request.GetBool("listening-stats", false) {
			path = "/me/listening-stats"
		} else if request.GetBool("items-in-progress", false) {
			path = "/me/items-in-progress"
		} else if progressItemID := request.GetString("progress_item_id", ""); progressItemID != "" {
			// Handle progress endpoints with IDs
			if progressEpisodeID := request.GetString("progress_episode_id", ""); progressEpisodeID != "" {
				path = fmt.Sprintf("/me/progress/%s/%s", progressItemID, progressEpisodeID)
			} else {
				path = fmt.Sprintf("/me/progress/%s", progressItemID)
			}
		}

		body, err := absGET(ctx, baseURL, token, path)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		return mcp.NewToolResultText(string(body)), nil
	})

	// Add ABS Sessions handlers
	s.AddTool(sessionsTool, createSimpleGETHandler("/sessions"))
	s.AddTool(sessionTool, createGETByIDHandler("/sessions/%s", "session_id"))

	// Add ABS Podcasts handlers
	s.AddTool(podcastsTool, handlePodcasts)
	s.AddTool(podcastTool, handlePodcast)

	// Add ABS Collections handlers
	s.AddTool(collectionsTool, createSimpleGETHandler("/collections"))
	s.AddTool(collectionTool, createGETByIDHandler("/collections/%s", "collection_id"))
	s.AddTool(createCollectionTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		baseURL, token, err := getABSConfig(request)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		libraryID, err := request.RequireString("library_id")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		name, err := request.RequireString("name")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		payload := map[string]interface{}{
			"libraryId": libraryID,
			"name":      name,
		}

		if description := request.GetString("description", ""); description != "" {
			payload["description"] = description
		}

		body, err := absPOST(ctx, baseURL, token, "/collections", payload)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		return mcp.NewToolResultText(string(body)), nil
	})
	s.AddTool(addToCollectionTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		baseURL, token, err := getABSConfig(request)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		collectionID, err := request.RequireString("collection_id")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		bookID, err := request.RequireString("book_id")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		payload := map[string]interface{}{
			"id": bookID,
		}

		body, err := absPOST(ctx, baseURL, token, fmt.Sprintf("/collections/%s/book", collectionID), payload)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		return mcp.NewToolResultText(string(body)), nil
	})

	// Add ABS Playlists handlers
	s.AddTool(playlistsTool, createSimpleGETHandler("/playlists"))
	s.AddTool(playlistTool, createGETByIDHandler("/playlists/%s", "playlist_id"))
	s.AddTool(createPlaylistTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		baseURL, token, err := getABSConfig(request)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		libraryID, err := request.RequireString("library_id")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		name, err := request.RequireString("name")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		payload := map[string]interface{}{
			"libraryId": libraryID,
			"name":      name,
		}

		if description := request.GetString("description", ""); description != "" {
			payload["description"] = description
		}

		body, err := absPOST(ctx, baseURL, token, "/playlists", payload)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		return mcp.NewToolResultText(string(body)), nil
	})
	s.AddTool(addToPlaylistTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		baseURL, token, err := getABSConfig(request)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		playlistID, err := request.RequireString("playlist_id")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		itemID, err := request.RequireString("item_id")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		payload := map[string]interface{}{
			"libraryItemId": itemID,
		}

		if episodeID := request.GetString("episode_id", ""); episodeID != "" {
			payload["episodeId"] = episodeID
		}

		body, err := absPOST(ctx, baseURL, token, fmt.Sprintf("/playlists/%s/item", playlistID), payload)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		return mcp.NewToolResultText(string(body)), nil
	})

	// Add Podcast check episodes handler
	s.AddTool(checkPodcastEpisodesTool, handleCheckPodcastEpisodes)

	// Add create backup handler
	s.AddTool(createBackupTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		baseURL, token, err := getABSConfig(request)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		body, err := absPOST(ctx, baseURL, token, "/backups", nil)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		return mcp.NewToolResultText(string(body)), nil
	})

	// Add progress tracking handler
	s.AddTool(updateProgressTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		baseURL, token, err := getABSConfig(request)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		itemID, err := request.RequireString("item_id")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		progress, err := request.RequireFloat("progress")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		payload := map[string]interface{}{
			"currentTime": progress,
		}

		if duration := request.GetFloat("duration", 0); duration > 0 {
			payload["duration"] = duration
		}

		if _, ok := request.GetArguments()["is_finished"]; ok {
			payload["isFinished"] = request.GetBool("is_finished", false)
		}

		path := fmt.Sprintf("/me/progress/%s", itemID)
		if episodeID := request.GetString("episode_id", ""); episodeID != "" {
			path = fmt.Sprintf("/me/progress/%s/%s", itemID, episodeID)
		}

		body, err := absPATCH(ctx, baseURL, token, path, payload)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		return mcp.NewToolResultText(string(body)), nil
	})

	// Add item metadata handlers
	s.AddTool(updateItemMediaTool, handleUpdateItemMedia)
	s.AddTool(batchUpdateItemsTool, handleBatchUpdateItems)
	s.AddTool(matchItemTool, handleMatchItem)
	s.AddTool(searchBooksTool, handleSearchBooks)
	s.AddTool(setItemCoverTool, handleSetItemCover)
	s.AddTool(updateAuthorTool, handleUpdateAuthor)
	s.AddTool(setAuthorImageTool, handleSetAuthorImage)
	s.AddTool(compareCoversTool, handleCompareCovers)
	s.AddTool(deleteItemTool, handleDeleteItem)

	// Add chapter and track handlers
	s.AddTool(searchChaptersTool, handleSearchChapters)
	s.AddTool(updateItemChaptersTool, handleUpdateItemChapters)
	s.AddTool(applyAudibleChaptersTool, handleApplyAudibleChapters)
	s.AddTool(reorderTracksTool, handleReorderTracks)

	// Add Server status/health handlers (these are at root level, not /api)
	s.AddTool(pingTool, createRootGETHandler("/ping"))
	s.AddTool(healthcheckTool, createRootGETHandler("/healthcheck"))
	s.AddTool(statusTool, createRootGETHandler("/status"))

	// Add Users handlers
	s.AddTool(usersTool, createSimpleGETHandler("/users"))
	s.AddTool(usersOnlineTool, createSimpleGETHandler("/users/online"))
	s.AddTool(userTool, createGETByIDWithSubResourceHandler("/users/%s", "user_id", []string{
		"listening-sessions",
		"listening-stats",
	}))

	// Add Series handler
	s.AddTool(seriesTool, createGETByIDHandler("/series/%s", "series_id"))

	// Add Author image handler
	s.AddTool(authorImageTool, createGETByIDImageHandler("/authors/%s/image", "author_id"))

	// Add Backups handler
	s.AddTool(backupsTool, createSimpleGETHandler("/backups"))

	// Add Filesystem handler
	s.AddTool(filesystemTool, createSimpleGETHandler("/filesystem"))

	// Add Authorize handler
	s.AddTool(authorizeTool, createSimplePOSTHandler("/authorize"))

	// Add Tags and Genres handlers
	s.AddTool(tagsTool, createSimpleGETHandler("/tags"))
	s.AddTool(genresTool, createSimpleGETHandler("/genres"))

	return s
}

func main() {
	s := newMCPServer()

	switch transport := strings.ToLower(os.Getenv("MCP_TRANSPORT")); transport {
	case "", "stdio":
		if err := server.ServeStdio(s); err != nil {
			fmt.Printf("Server error: %v\n", err)
		}
	case "http":
		addr := os.Getenv("MCP_ADDR")
		if addr == "" {
			addr = ":8080"
		}
		endpoint := os.Getenv("MCP_ENDPOINT")
		if endpoint == "" {
			endpoint = "/mcp"
		}
		httpServer := server.NewStreamableHTTPServer(s, server.WithEndpointPath(endpoint))
		fmt.Fprintf(os.Stderr, "Listening on %s%s\n", addr, endpoint)
		if err := httpServer.Start(addr); err != nil {
			fmt.Printf("Server error: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "Unknown MCP_TRANSPORT %q (use stdio or http)\n", transport)
		os.Exit(1)
	}
}
