package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

const (
	// A candidate must have this many times the pixels of the current cover
	// to count as better, so near-identical images do not trigger a swap.
	coverMinGain = 1.1

	maxCoverBytes      = 25 << 20
	maxCoverCandidates = 10
)

// coverFetchClient downloads candidate covers. It refuses to connect to
// loopback, private and link-local addresses, so the tool cannot be pointed
// at services inside the network the server runs in.
var coverFetchClient = &http.Client{
	Timeout:   30 * time.Second,
	Transport: &http.Transport{DialContext: publicOnlyDialContext},
}

func publicOnlyDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("no address found for %s", host)
	}
	for _, address := range addresses {
		ip := address.IP
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
			return nil, fmt.Errorf("%s resolves to a non-public address", host)
		}
	}
	dialer := net.Dialer{Timeout: 10 * time.Second}
	return dialer.DialContext(ctx, network, net.JoinHostPort(addresses[0].IP.String(), port))
}

type coverInfo struct {
	URL    string  `json:"url,omitempty"`
	Width  int     `json:"width,omitempty"`
	Height int     `json:"height,omitempty"`
	Pixels int     `json:"pixels,omitempty"`
	Bytes  int     `json:"bytes,omitempty"`
	Format string  `json:"format,omitempty"`
	Ratio  float64 `json:"pixel_ratio,omitempty"`
	Better bool    `json:"better,omitempty"`
	Error  string  `json:"error,omitempty"`
}

// describeImage reads the size and format of an image without decoding it.
// WebP and other formats the standard library cannot read only report their
// byte size.
func describeImage(data []byte) coverInfo {
	info := coverInfo{Bytes: len(data)}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		info.Error = "cannot read the image size, the format is not JPEG, PNG or GIF"
		return info
	}
	info.Width, info.Height, info.Format = config.Width, config.Height, format
	info.Pixels = config.Width * config.Height
	return info
}

func fetchCoverCandidate(ctx context.Context, rawURL string) ([]byte, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, fmt.Errorf("not an http or https address")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	resp, err := coverFetchClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("download returned %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxCoverBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read download: %w", err)
	}
	if len(data) > maxCoverBytes {
		return nil, fmt.Errorf("image is larger than %d MB", maxCoverBytes>>20)
	}
	return data, nil
}

// handleCompareCovers compares the current cover of an item, in its original
// size, with candidate images so the better one can be chosen.
func handleCompareCovers(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	baseURL, token, err := getABSConfig(request)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	itemID, err := requireNonBlankString(request, "item_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	urls := request.GetStringSlice("urls", nil)
	if len(urls) == 0 || len(urls) > maxCoverCandidates {
		return mcp.NewToolResultError(fmt.Sprintf("urls must list between 1 and %d image addresses", maxCoverCandidates)), nil
	}
	for _, candidate := range urls {
		if strings.TrimSpace(candidate) == "" {
			return mcp.NewToolResultError("urls must not contain blank entries"), nil
		}
	}

	var current *coverInfo
	var currentNote string
	response, err := absGETRaw(ctx, baseURL, token, fmt.Sprintf("/items/%s/cover?raw=1", url.PathEscape(itemID)))
	switch {
	case err != nil:
		currentNote = fmt.Sprintf("no current cover could be read: %s", err)
	case len(response.Body) == 0:
		currentNote = "the server returned no image data for the current cover"
	default:
		info := describeImage(response.Body)
		current = &info
	}

	candidates := make([]coverInfo, 0, len(urls))
	bestIndex := -1
	for i, candidateURL := range urls {
		info := coverInfo{URL: candidateURL}
		data, err := fetchCoverCandidate(ctx, candidateURL)
		if err != nil {
			info.Error = err.Error()
			candidates = append(candidates, info)
			continue
		}
		described := describeImage(data)
		described.URL = candidateURL
		info = described

		if info.Pixels > 0 {
			switch {
			case current == nil || current.Pixels == 0:
				info.Better = true
			default:
				info.Ratio = float64(info.Pixels) / float64(current.Pixels)
				info.Better = info.Ratio >= coverMinGain
			}
		}
		candidates = append(candidates, info)

		if info.Better && (bestIndex < 0 || info.Pixels > candidates[bestIndex].Pixels) {
			bestIndex = i
		}
	}

	best := ""
	if bestIndex >= 0 {
		best = candidates[bestIndex].URL
	}
	result := map[string]interface{}{
		"item_id":    itemID,
		"current":    current,
		"candidates": candidates,
		"best":       best,
	}
	if currentNote != "" {
		result["current_note"] = currentNote
	}

	output, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("encode result: %s", err)), nil
	}
	return mcp.NewToolResultText(string(output)), nil
}
