package main

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
)

func TestUpdateAuthorTool(t *testing.T) {
	recorder := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{"updated": true})
	})
	defer recorder.Close()

	result := callRegisteredTool(t, newMCPServer(), "update_author", map[string]interface{}{
		"base_url": recorder.URL, "token": "t", "author_id": "a1", "description": "Bio", "asin": "",
	})
	if result.IsError {
		t.Fatalf("returned error: %v", result)
	}
	got, _ := recorder.LastRequest()
	if got.Method != http.MethodPatch || got.Path != "/api/authors/a1" {
		t.Errorf("expected PATCH /api/authors/a1, got %s %s", got.Method, got.Path)
	}
	want := map[string]interface{}{"description": "Bio", "asin": ""}
	if body := decodeBody(t, got.Body); !reflect.DeepEqual(body, want) {
		t.Errorf("unexpected body %#v", body)
	}
}

func TestSetAuthorImageTool(t *testing.T) {
	recorder := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{"author": map[string]interface{}{"id": "a1"}})
	})
	defer recorder.Close()

	result := callRegisteredTool(t, newMCPServer(), "set_author_image", map[string]interface{}{
		"base_url": recorder.URL, "token": "t", "author_id": "a1", "url": "https://example.com/p.jpg",
	})
	if result.IsError {
		t.Fatalf("returned error: %v", result)
	}
	got, _ := recorder.LastRequest()
	if got.Method != http.MethodPost || got.Path != "/api/authors/a1/image" {
		t.Errorf("expected POST /api/authors/a1/image, got %s %s", got.Method, got.Path)
	}
}

func TestAuthorToolErrors(t *testing.T) {
	recorder := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
	})
	defer recorder.Close()

	tests := []struct {
		tool   string
		params map[string]interface{}
	}{
		{"update_author", map[string]interface{}{"author_id": "a1"}},
		{"update_author", map[string]interface{}{"author_id": "a1", "name": " "}},
		{"update_author", map[string]interface{}{"name": "X"}},
		{"set_author_image", map[string]interface{}{"author_id": "a1", "url": "file:///etc/passwd"}},
		{"set_author_image", map[string]interface{}{"url": "https://example.com/p.jpg"}},
	}
	for _, tt := range tests {
		tt.params["base_url"] = recorder.URL
		tt.params["token"] = "t"
		if result := callRegisteredTool(t, newMCPServer(), tt.tool, tt.params); !result.IsError {
			t.Errorf("%s %v: expected tool error", tt.tool, tt.params)
		}
	}
}
