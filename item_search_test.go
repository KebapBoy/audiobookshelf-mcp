package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"testing"
)

func TestSearchBooksTool(t *testing.T) {
	tests := []struct {
		name          string
		params        map[string]interface{}
		expectedQuery url.Values
	}{
		{
			name:          "title only",
			params:        map[string]interface{}{"title": "Das Paket"},
			expectedQuery: url.Values{"title": {"Das Paket"}},
		},
		{
			name: "all fields",
			params: map[string]interface{}{
				"title":    "Das Paket",
				"author":   "Sebastian Fitzek",
				"provider": "audible.de",
				"item_id":  "book-1",
			},
			expectedQuery: url.Values{"title": {"Das Paket"}, "author": {"Sebastian Fitzek"}, "provider": {"audible.de"}, "id": {"book-1"}},
		},
		{
			name:          "item_id alone searches with the item's own data",
			params:        map[string]interface{}{"item_id": "book-1", "provider": "audible.de"},
			expectedQuery: url.Values{"id": {"book-1"}, "provider": {"audible.de"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
				if !requireMethod(w, r, http.MethodGet) {
					return
				}
				json.NewEncoder(w).Encode([]map[string]interface{}{{"title": "Das Paket", "asin": "B01LOEALCC"}})
			})
			defer recorder.Close()

			tt.params["base_url"] = recorder.URL
			tt.params["token"] = "test-token"
			result := callRegisteredTool(t, newMCPServer(), "search_books", tt.params)
			if result.IsError {
				t.Fatalf("returned error: %v", result)
			}

			got, ok := recorder.LastRequest()
			if !ok {
				t.Fatal("expected recorded request")
			}
			if got.Path != "/api/search/books" {
				t.Errorf("expected /api/search/books, got %s", got.Path)
			}
			query, err := url.ParseQuery(got.RawQuery)
			if err != nil {
				t.Fatalf("parse query %q: %v", got.RawQuery, err)
			}
			if !reflect.DeepEqual(query, tt.expectedQuery) {
				t.Errorf("expected query %v, got %v", tt.expectedQuery, query)
			}

			var results []map[string]interface{}
			decodeResultText(t, result, &results)
			if len(results) != 1 || results[0]["asin"] != "B01LOEALCC" {
				t.Errorf("expected the provider results to be passed through, got %v", results)
			}
		})
	}
}

func TestSearchBooksToolRequiresTitleOrItem(t *testing.T) {
	recorder := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
	})
	defer recorder.Close()

	for _, params := range []map[string]interface{}{
		{},
		{"author": "Sebastian Fitzek"},
		{"title": "  ", "item_id": " "},
	} {
		params["base_url"] = recorder.URL
		params["token"] = "test-token"
		if result := callRegisteredTool(t, newMCPServer(), "search_books", params); !result.IsError {
			t.Errorf("expected tool error for %v, got %v", params, result)
		}
	}
}

func TestSetItemCoverTool(t *testing.T) {
	recorder := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "cover": "/metadata/items/book-1/cover.jpg"})
	})
	defer recorder.Close()

	result := callRegisteredTool(t, newMCPServer(), "set_item_cover", map[string]interface{}{
		"base_url": recorder.URL, "token": "test-token", "item_id": "book-1", "url": "https://m.media-amazon.com/images/I/cover.jpg",
	})
	if result.IsError {
		t.Fatalf("returned error: %v", result)
	}

	got, _ := recorder.LastRequest()
	if got.Method != http.MethodPost || got.Path != "/api/items/book-1/cover" {
		t.Errorf("expected POST /api/items/book-1/cover, got %s %s", got.Method, got.Path)
	}
	want := map[string]interface{}{"url": "https://m.media-amazon.com/images/I/cover.jpg"}
	if body := decodeBody(t, got.Body); !reflect.DeepEqual(body, want) {
		t.Errorf("unexpected body %#v", body)
	}
}

func TestSetItemCoverToolErrors(t *testing.T) {
	recorder := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
	})
	defer recorder.Close()

	tests := []struct {
		name   string
		params map[string]interface{}
	}{
		{"missing item_id", map[string]interface{}{"url": "https://example.com/c.jpg"}},
		{"blank item_id", map[string]interface{}{"item_id": " ", "url": "https://example.com/c.jpg"}},
		{"missing url", map[string]interface{}{"item_id": "book-1"}},
		{"not a url", map[string]interface{}{"item_id": "book-1", "url": "cover.jpg"}},
		{"unsupported scheme", map[string]interface{}{"item_id": "book-1", "url": "file:///etc/passwd"}},
		{"ftp scheme", map[string]interface{}{"item_id": "book-1", "url": "ftp://example.com/c.jpg"}},
		{"no host", map[string]interface{}{"item_id": "book-1", "url": "https:///c.jpg"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.params["base_url"] = recorder.URL
			tt.params["token"] = "test-token"
			if result := callRegisteredTool(t, newMCPServer(), "set_item_cover", tt.params); !result.IsError {
				t.Errorf("expected tool error, got %v", result)
			}
		})
	}
}

func TestSetItemCoverToolReturnsABSErrors(t *testing.T) {
	recorder := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Forbidden", http.StatusForbidden)
	})
	defer recorder.Close()

	result := callRegisteredTool(t, newMCPServer(), "set_item_cover", map[string]interface{}{
		"base_url": recorder.URL, "token": "test-token", "item_id": "book-1", "url": "https://example.com/c.jpg",
	})
	if !result.IsError {
		t.Errorf("expected tool error, got %v", result)
	}
}
