package main

import (
	"bytes"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func pngBytes(t *testing.T, width, height int) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, image.NewRGBA(image.Rect(0, 0, width, height))); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buffer.Bytes()
}

// useOpenCoverClient lets the cover download reach the loopback test servers.
func useOpenCoverClient(t *testing.T) {
	t.Helper()
	previous := coverFetchClient
	coverFetchClient = &http.Client{}
	t.Cleanup(func() { coverFetchClient = previous })
}

func coverImageServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/big.png":
			w.Write(pngBytes(t, 400, 400))
		case "/bigger.png":
			w.Write(pngBytes(t, 800, 800))
		case "/small.png":
			w.Write(pngBytes(t, 50, 50))
		case "/almost.png":
			w.Write(pngBytes(t, 104, 100))
		case "/notanimage":
			w.Write([]byte("hello"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func compareCoversWith(t *testing.T, absHandler http.HandlerFunc, urls ...interface{}) (*mcp.CallToolResult, *recordingServer) {
	t.Helper()
	recorder := newRecordingServer(absHandler)
	t.Cleanup(recorder.Close)
	result := callRegisteredTool(t, newMCPServer(), "compare_covers", map[string]interface{}{
		"base_url": recorder.URL, "token": "test-token", "item_id": "book-1", "urls": urls,
	})
	return result, recorder
}

func currentCoverHandler(t *testing.T, width, height int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/items/book-1/cover" || r.URL.Query().Get("raw") != "1" {
			t.Errorf("expected the raw cover of book-1, got %s?%s", r.URL.Path, r.URL.RawQuery)
			http.NotFound(w, r)
			return
		}
		w.Write(pngBytes(t, width, height))
	}
}

func TestCompareCovers(t *testing.T) {
	useOpenCoverClient(t)
	images := coverImageServer(t)

	result, _ := compareCoversWith(t, currentCoverHandler(t, 100, 100),
		images.URL+"/big.png", images.URL+"/bigger.png", images.URL+"/small.png", images.URL+"/almost.png",
		images.URL+"/notanimage", images.URL+"/missing.png", "ftp://example.com/c.png")
	if result.IsError {
		t.Fatalf("returned error: %v", result)
	}

	var got struct {
		Current    *coverInfo  `json:"current"`
		Candidates []coverInfo `json:"candidates"`
		Best       string      `json:"best"`
	}
	decodeResultText(t, result, &got)

	if got.Current == nil || got.Current.Width != 100 || got.Current.Height != 100 || got.Current.Format != "png" {
		t.Errorf("unexpected current cover %+v", got.Current)
	}
	if len(got.Candidates) != 7 {
		t.Fatalf("expected 7 candidates, got %d", len(got.Candidates))
	}

	want := []struct {
		better bool
		width  int
		failed bool
	}{
		{true, 400, false},  // big
		{true, 800, false},  // bigger
		{false, 50, false},  // small
		{false, 104, false}, // almost: only 8% more pixels
		{false, 0, true},    // not an image
		{false, 0, true},    // 404
		{false, 0, true},    // wrong scheme
	}
	for i, w := range want {
		c := got.Candidates[i]
		if c.Better != w.better || c.Width != w.width || (c.Error != "") != w.failed {
			t.Errorf("candidate %d: expected better=%v width=%d failed=%v, got %+v", i, w.better, w.width, w.failed, c)
		}
	}
	if got.Best != images.URL+"/bigger.png" {
		t.Errorf("expected the largest better candidate as best, got %q", got.Best)
	}
	if got.Candidates[1].Ratio != 64 {
		t.Errorf("expected pixel ratio 64, got %v", got.Candidates[1].Ratio)
	}
}

func TestCompareCoversNothingBetter(t *testing.T) {
	useOpenCoverClient(t)
	images := coverImageServer(t)

	result, _ := compareCoversWith(t, currentCoverHandler(t, 1000, 1000), images.URL+"/big.png")
	var got struct {
		Best string `json:"best"`
	}
	decodeResultText(t, result, &got)
	if result.IsError || got.Best != "" {
		t.Errorf("expected no better candidate, got %q (%v)", got.Best, result)
	}
}

func TestCompareCoversWithoutCurrentCover(t *testing.T) {
	useOpenCoverClient(t)
	images := coverImageServer(t)

	result, _ := compareCoversWith(t, http.NotFound, images.URL+"/small.png")
	if result.IsError {
		t.Fatalf("returned error: %v", result)
	}
	var got struct {
		Current     *coverInfo  `json:"current"`
		CurrentNote string      `json:"current_note"`
		Candidates  []coverInfo `json:"candidates"`
		Best        string      `json:"best"`
	}
	decodeResultText(t, result, &got)
	if got.Current != nil || got.CurrentNote == "" {
		t.Errorf("expected a note instead of a current cover, got %+v / %q", got.Current, got.CurrentNote)
	}
	if !got.Candidates[0].Better || got.Best != images.URL+"/small.png" {
		t.Errorf("without a current cover any readable candidate is better, got %+v best %q", got.Candidates[0], got.Best)
	}
}

func TestCompareCoversValidatesInput(t *testing.T) {
	recorder := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
	})
	defer recorder.Close()

	tooMany := make([]interface{}, maxCoverCandidates+1)
	for i := range tooMany {
		tooMany[i] = "https://example.com/c.jpg"
	}
	tests := []struct {
		name   string
		params map[string]interface{}
	}{
		{"missing item_id", map[string]interface{}{"urls": []interface{}{"https://example.com/c.jpg"}}},
		{"missing urls", map[string]interface{}{"item_id": "book-1"}},
		{"empty urls", map[string]interface{}{"item_id": "book-1", "urls": []interface{}{}}},
		{"too many urls", map[string]interface{}{"item_id": "book-1", "urls": tooMany}},
		{"blank url", map[string]interface{}{"item_id": "book-1", "urls": []interface{}{" "}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.params["base_url"] = recorder.URL
			tt.params["token"] = "test-token"
			if result := callRegisteredTool(t, newMCPServer(), "compare_covers", tt.params); !result.IsError {
				t.Errorf("expected tool error, got %v", result)
			}
		})
	}
}

func TestCoverDownloadRefusesNonPublicAddresses(t *testing.T) {
	images := coverImageServer(t) // listens on 127.0.0.1

	_, err := fetchCoverCandidate(t.Context(), images.URL+"/big.png")
	if err == nil || !strings.Contains(err.Error(), "non-public") {
		t.Errorf("expected the loopback address to be refused, got %v", err)
	}

	for _, address := range []string{"http://localhost/c.png", "http://10.0.0.5/c.png", "http://192.168.1.10/c.png", "http://169.254.169.254/latest", "http://[::1]/c.png"} {
		if _, err := fetchCoverCandidate(t.Context(), address); err == nil {
			t.Errorf("expected %s to be refused", address)
		}
	}
}

func TestDescribeImage(t *testing.T) {
	info := describeImage(pngBytes(t, 30, 20))
	if info.Width != 30 || info.Height != 20 || info.Pixels != 600 || info.Format != "png" || info.Error != "" {
		t.Errorf("unexpected info %+v", info)
	}

	info = describeImage([]byte("RIFF....WEBPVP8 "))
	if info.Error == "" || info.Bytes != 16 || info.Pixels != 0 {
		t.Errorf("expected an unreadable-format note with the byte size, got %+v", info)
	}
}
