package main

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func decodeBody(t *testing.T, body []byte) interface{} {
	t.Helper()
	var decoded interface{}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode recorded body %q: %v", body, err)
	}
	return decoded
}

func TestBuildMediaUpdate(t *testing.T) {
	args := map[string]interface{}{
		"title":          "Der Nachtwandler",
		"published_year": "2013",
		"explicit":       false,
		"authors":        []interface{}{"Sebastian Fitzek"},
		"narrators":      []interface{}{"Simon Jäger"},
		"genres":         []interface{}{"Thriller"},
		"tags":           []interface{}{},
		"series": []interface{}{
			map[string]interface{}{"name": "Auris", "sequence": "3"},
			map[string]interface{}{"name": "Standalone"},
		},
		"item_id": "ignored",
		"token":   "ignored",
	}

	got, err := buildMediaUpdate(args)
	if err != nil {
		t.Fatalf("buildMediaUpdate returned error: %v", err)
	}

	want := map[string]interface{}{
		"metadata": map[string]interface{}{
			"title":         "Der Nachtwandler",
			"publishedYear": "2013",
			"explicit":      false,
			"authors":       []map[string]interface{}{{"name": "Sebastian Fitzek"}},
			"narrators":     []string{"Simon Jäger"},
			"genres":        []string{"Thriller"},
			"series": []map[string]interface{}{
				{"name": "Auris", "sequence": "3"},
				{"name": "Standalone"},
			},
		},
		"tags": []string{},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("unexpected payload\n got: %#v\nwant: %#v", got, want)
	}
}

func TestBuildMediaUpdateErrors(t *testing.T) {
	tests := []struct {
		name string
		args map[string]interface{}
	}{
		{"no fields", map[string]interface{}{"item_id": "book-1", "token": "t"}},
		{"title not a string", map[string]interface{}{"title": 5.0}},
		{"explicit not a boolean", map[string]interface{}{"explicit": "yes"}},
		{"authors not an array", map[string]interface{}{"authors": "Sebastian Fitzek"}},
		{"narrators with non-string entry", map[string]interface{}{"narrators": []interface{}{1.0}}},
		{"series not an array", map[string]interface{}{"series": "Auris"}},
		{"series entry not an object", map[string]interface{}{"series": []interface{}{"Auris"}}},
		{"series entry without name", map[string]interface{}{"series": []interface{}{map[string]interface{}{"sequence": "1"}}}},
		{"series entry with blank name", map[string]interface{}{"series": []interface{}{map[string]interface{}{"name": "  "}}}},
		{"series sequence not a string", map[string]interface{}{"series": []interface{}{map[string]interface{}{"name": "Auris", "sequence": 1.0}}}},
		{"tags not an array", map[string]interface{}{"tags": "a"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, err := buildMediaUpdate(tt.args); err == nil {
				t.Errorf("expected error, got payload %#v", got)
			}
		})
	}
}

func TestUpdateItemMediaTool(t *testing.T) {
	s := newMCPServer()
	recorder := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodPatch) {
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"updated": true})
	})
	defer recorder.Close()

	result := callRegisteredTool(t, s, "update_item_media", map[string]interface{}{
		"base_url": recorder.URL,
		"token":    "test-token",
		"item_id":  "book-123",
		"title":    "Das Paket",
		"subtitle": "",
		"authors":  []interface{}{"Sebastian Fitzek"},
	})
	if result.IsError {
		t.Fatalf("result returned error: %v", result)
	}

	got, ok := recorder.LastRequest()
	if !ok {
		t.Fatal("expected recorded request")
	}
	if got.Method != http.MethodPatch || got.Path != "/api/items/book-123/media" {
		t.Errorf("expected PATCH /api/items/book-123/media, got %s %s", got.Method, got.Path)
	}

	want := map[string]interface{}{
		"metadata": map[string]interface{}{
			"title":    "Das Paket",
			"subtitle": "",
			"authors":  []interface{}{map[string]interface{}{"name": "Sebastian Fitzek"}},
		},
	}
	if body := decodeBody(t, got.Body); !reflect.DeepEqual(body, want) {
		t.Errorf("unexpected body\n got: %#v\nwant: %#v", body, want)
	}
}

func TestUpdateItemMediaToolErrors(t *testing.T) {
	s := newMCPServer()
	recorder := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
	})
	defer recorder.Close()

	tests := []struct {
		name   string
		params map[string]interface{}
	}{
		{"missing item_id", map[string]interface{}{"title": "x"}},
		{"blank item_id", map[string]interface{}{"item_id": " ", "title": "x"}},
		{"no metadata fields", map[string]interface{}{"item_id": "book-1"}},
		{"invalid field type", map[string]interface{}{"item_id": "book-1", "title": 1.0}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.params["base_url"] = recorder.URL
			tt.params["token"] = "test-token"
			if result := callRegisteredTool(t, s, "update_item_media", tt.params); !result.IsError {
				t.Errorf("expected tool error, got %v", result)
			}
		})
	}
}

func TestBatchUpdateItemsTool(t *testing.T) {
	s := newMCPServer()
	recorder := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "updates": 2})
	})
	defer recorder.Close()

	result := callRegisteredTool(t, s, "batch_update_items", map[string]interface{}{
		"base_url": recorder.URL,
		"token":    "test-token",
		"updates": []interface{}{
			map[string]interface{}{"item_id": "book-1", "title": "Die Therapie"},
			map[string]interface{}{"item_id": "book-2", "narrators": []interface{}{"Simon Jäger"}, "tags": []interface{}{"Thriller"}},
		},
	})
	if result.IsError {
		t.Fatalf("result returned error: %v", result)
	}

	got, ok := recorder.LastRequest()
	if !ok {
		t.Fatal("expected recorded request")
	}
	if got.Method != http.MethodPost || got.Path != "/api/items/batch/update" {
		t.Errorf("expected POST /api/items/batch/update, got %s %s", got.Method, got.Path)
	}

	want := []interface{}{
		map[string]interface{}{
			"id":           "book-1",
			"mediaPayload": map[string]interface{}{"metadata": map[string]interface{}{"title": "Die Therapie"}},
		},
		map[string]interface{}{
			"id": "book-2",
			"mediaPayload": map[string]interface{}{
				"metadata": map[string]interface{}{"narrators": []interface{}{"Simon Jäger"}},
				"tags":     []interface{}{"Thriller"},
			},
		},
	}
	if body := decodeBody(t, got.Body); !reflect.DeepEqual(body, want) {
		t.Errorf("unexpected body\n got: %#v\nwant: %#v", body, want)
	}
}

func TestBatchUpdateItemsToolErrors(t *testing.T) {
	s := newMCPServer()
	recorder := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
	})
	defer recorder.Close()

	tests := []struct {
		name    string
		updates interface{}
	}{
		{"missing updates", nil},
		{"empty updates", []interface{}{}},
		{"entry not an object", []interface{}{"book-1"}},
		{"entry without item_id", []interface{}{map[string]interface{}{"title": "x"}}},
		{"entry without metadata", []interface{}{map[string]interface{}{"item_id": "book-1"}}},
		{"second entry invalid", []interface{}{
			map[string]interface{}{"item_id": "book-1", "title": "x"},
			map[string]interface{}{"item_id": "book-2", "explicit": "no"},
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := map[string]interface{}{"base_url": recorder.URL, "token": "test-token"}
			if tt.updates != nil {
				params["updates"] = tt.updates
			}
			if result := callRegisteredTool(t, s, "batch_update_items", params); !result.IsError {
				t.Errorf("expected tool error, got %v", result)
			}
		})
	}
}

func TestMatchItemTool(t *testing.T) {
	tests := []struct {
		name    string
		params  map[string]interface{}
		payload map[string]interface{}
	}{
		{
			name:    "only item_id sends an empty payload",
			params:  map[string]interface{}{"item_id": "book-1"},
			payload: map[string]interface{}{},
		},
		{
			name: "all fields",
			params: map[string]interface{}{
				"item_id":          "book-1",
				"provider":         "audible.de",
				"title":            "Das Paket",
				"author":           "Sebastian Fitzek",
				"isbn":             "9783426199541",
				"asin":             "B01A2B3C4D",
				"override_details": true,
				"override_cover":   true,
			},
			payload: map[string]interface{}{
				"provider":        "audible.de",
				"title":           "Das Paket",
				"author":          "Sebastian Fitzek",
				"isbn":            "9783426199541",
				"asin":            "B01A2B3C4D",
				"overrideDetails": true,
				"overrideCover":   true,
			},
		},
		{
			name:    "explicit false values are sent",
			params:  map[string]interface{}{"item_id": "book-1", "override_details": false, "override_cover": false},
			payload: map[string]interface{}{"overrideDetails": false, "overrideCover": false},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newMCPServer()
			recorder := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
				if !requireMethod(w, r, http.MethodPost) {
					return
				}
				json.NewEncoder(w).Encode(map[string]interface{}{"updated": true})
			})
			defer recorder.Close()

			tt.params["base_url"] = recorder.URL
			tt.params["token"] = "test-token"
			result := callRegisteredTool(t, s, "match_item", tt.params)
			if result.IsError {
				t.Fatalf("result returned error: %v", result)
			}

			got, ok := recorder.LastRequest()
			if !ok {
				t.Fatal("expected recorded request")
			}
			if got.Method != http.MethodPost || got.Path != "/api/items/book-1/match" {
				t.Errorf("expected POST /api/items/book-1/match, got %s %s", got.Method, got.Path)
			}
			if body := decodeBody(t, got.Body); !reflect.DeepEqual(body, tt.payload) {
				t.Errorf("unexpected body\n got: %#v\nwant: %#v", body, tt.payload)
			}
		})
	}
}

func TestMatchItemToolRequiresItemID(t *testing.T) {
	s := newMCPServer()
	recorder := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
	})
	defer recorder.Close()

	result := callRegisteredTool(t, s, "match_item", map[string]interface{}{
		"base_url": recorder.URL,
		"token":    "test-token",
		"item_id":  " ",
	})
	if !result.IsError {
		t.Errorf("expected tool error, got %v", result)
	}
}

func TestDeleteItemTool(t *testing.T) {
	tests := []struct {
		name          string
		params        map[string]interface{}
		expectedQuery string
	}{
		{
			name:   "soft delete",
			params: map[string]interface{}{"item_id": "book-1", "confirm": true},
		},
		{
			name:          "hard delete",
			params:        map[string]interface{}{"item_id": "book-1", "confirm": true, "hard": true},
			expectedQuery: "hard=1",
		},
		{
			name:   "hard false behaves like soft delete",
			params: map[string]interface{}{"item_id": "book-1", "confirm": true, "hard": false},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newMCPServer()
			recorder := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
				if !requireMethod(w, r, http.MethodDelete) {
					return
				}
				w.WriteHeader(http.StatusOK)
			})
			defer recorder.Close()

			tt.params["base_url"] = recorder.URL
			tt.params["token"] = "test-token"
			result := callRegisteredTool(t, s, "delete_item", tt.params)
			if result.IsError {
				t.Fatalf("result returned error: %v", result)
			}

			got, ok := recorder.LastRequest()
			if !ok {
				t.Fatal("expected recorded request")
			}
			if got.Method != http.MethodDelete || got.Path != "/api/items/book-1" {
				t.Errorf("expected DELETE /api/items/book-1, got %s %s", got.Method, got.Path)
			}
			if got.RawQuery != tt.expectedQuery {
				t.Errorf("expected query %q, got %q", tt.expectedQuery, got.RawQuery)
			}
		})
	}
}

func TestDeleteItemToolRefusesWithoutConfirmation(t *testing.T) {
	tests := []struct {
		name   string
		params map[string]interface{}
	}{
		{"confirm omitted", map[string]interface{}{"item_id": "book-1"}},
		{"confirm false", map[string]interface{}{"item_id": "book-1", "confirm": false}},
		{"hard without confirm", map[string]interface{}{"item_id": "book-1", "hard": true}},
		{"blank item_id", map[string]interface{}{"item_id": "", "confirm": true}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newMCPServer()
			recorder := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			})
			defer recorder.Close()

			tt.params["base_url"] = recorder.URL
			tt.params["token"] = "test-token"
			if result := callRegisteredTool(t, s, "delete_item", tt.params); !result.IsError {
				t.Errorf("expected tool error, got %v", result)
			}
		})
	}
}

func TestItemMediaToolsReturnABSErrors(t *testing.T) {
	recorder := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusForbidden)
	})
	defer recorder.Close()

	tests := []struct {
		tool   string
		params map[string]interface{}
	}{
		{"update_item_media", map[string]interface{}{"item_id": "book-1", "title": "x"}},
		{"batch_update_items", map[string]interface{}{"updates": []interface{}{map[string]interface{}{"item_id": "book-1", "title": "x"}}}},
		{"match_item", map[string]interface{}{"item_id": "book-1"}},
		{"delete_item", map[string]interface{}{"item_id": "book-1", "confirm": true}},
	}

	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			tt.params["base_url"] = recorder.URL
			tt.params["token"] = "test-token"
			if result := callRegisteredTool(t, newMCPServer(), tt.tool, tt.params); !result.IsError {
				t.Errorf("expected tool error, got %v", result)
			}
		})
	}
}

func TestCompactItemResponse(t *testing.T) {
	long := strings.Repeat("ä", compactTextLimit+50)
	body, _ := json.Marshal(map[string]interface{}{
		"updated": true,
		"libraryItem": map[string]interface{}{
			"media": map[string]interface{}{
				"metadata":   map[string]interface{}{"title": "Das Paket", "description": long, "publishedYear": "2016"},
				"audioFiles": []interface{}{map[string]interface{}{"ino": "1"}, map[string]interface{}{"ino": "2"}},
				"chapters":   []interface{}{map[string]interface{}{"id": 0}},
			},
		},
	})

	var got struct {
		Updated        bool                   `json:"updated"`
		Metadata       map[string]interface{} `json:"metadata"`
		AudioFileCount int                    `json:"audio_file_count"`
		ChapterCount   int                    `json:"chapter_count"`
	}
	if err := json.Unmarshal([]byte(compactItemResponse(body)), &got); err != nil {
		t.Fatalf("decode compact response: %v", err)
	}

	if !got.Updated || got.AudioFileCount != 2 || got.ChapterCount != 1 {
		t.Errorf("unexpected summary %+v", got)
	}
	if got.Metadata["title"] != "Das Paket" || got.Metadata["publishedYear"] != "2016" {
		t.Errorf("short fields must stay untouched, got %v", got.Metadata)
	}
	description, _ := got.Metadata["description"].(string)
	if want := strings.Repeat("ä", compactTextLimit) + "…"; description != want {
		t.Errorf("expected the description cut to %d characters, got %d", compactTextLimit, len([]rune(description)))
	}
}

func TestCompactItemResponseKeepsOtherShapes(t *testing.T) {
	for _, body := range []string{`{"updated":true}`, `{"success":true,"updates":2}`, `not json`, ``} {
		if got := compactItemResponse([]byte(body)); got != body {
			t.Errorf("expected %q unchanged, got %q", body, got)
		}
	}
}

func TestMatchItemAndUpdateItemMediaReturnCompactResponses(t *testing.T) {
	recorder := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"updated": true,
			"libraryItem": map[string]interface{}{
				"media": map[string]interface{}{
					"metadata":   map[string]interface{}{"title": "Das Paket"},
					"audioFiles": []interface{}{map[string]interface{}{"ino": "1", "metaTags": strings.Repeat("x", 5000)}},
					"chapters":   []interface{}{},
				},
			},
		})
	})
	defer recorder.Close()

	tools := map[string]map[string]interface{}{
		"match_item":        {"item_id": "book-1"},
		"update_item_media": {"item_id": "book-1", "title": "Das Paket"},
	}
	for name, params := range tools {
		t.Run(name, func(t *testing.T) {
			params["base_url"] = recorder.URL
			params["token"] = "test-token"
			result := callRegisteredTool(t, newMCPServer(), name, params)
			if result.IsError {
				t.Fatalf("returned error: %v", result)
			}
			var got struct {
				Updated        bool `json:"updated"`
				AudioFileCount int  `json:"audio_file_count"`
			}
			text := result.Content[0].(mcp.TextContent).Text
			if strings.Contains(text, "metaTags") {
				t.Errorf("response was not compacted: %.200s", text)
			}
			if err := json.Unmarshal([]byte(text), &got); err != nil || !got.Updated || got.AudioFileCount != 1 {
				t.Errorf("unexpected response %q (%v)", text, err)
			}
		})
	}
}
