package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type recordedRequest struct {
	Method   string
	Path     string
	RawQuery string
	Body     []byte
}

type recordingServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests []recordedRequest
}

func newRecordingServer(handler http.HandlerFunc) *recordingServer {
	rs := &recordingServer{}
	rs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body.Close()
		r.Body = io.NopCloser(bytes.NewReader(body))

		rs.mu.Lock()
		rs.requests = append(rs.requests, recordedRequest{
			Method:   r.Method,
			Path:     r.URL.Path,
			RawQuery: r.URL.RawQuery,
			Body:     append([]byte(nil), body...),
		})
		rs.mu.Unlock()

		handler(w, r)
	}))
	return rs
}

func (rs *recordingServer) Requests() []recordedRequest {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	requests := make([]recordedRequest, len(rs.requests))
	copy(requests, rs.requests)
	return requests
}

func (rs *recordingServer) LastRequest() (recordedRequest, bool) {
	requests := rs.Requests()
	if len(requests) == 0 {
		return recordedRequest{}, false
	}
	return requests[len(requests)-1], true
}

func requireMethod(w http.ResponseWriter, r *http.Request, methods ...string) bool {
	for _, method := range methods {
		if r.Method == method {
			return true
		}
	}
	w.Header().Set("Allow", strings.Join(methods, ", "))
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	return false
}

func callRegisteredTool(t *testing.T, s *server.MCPServer, name string, params map[string]interface{}) *mcp.CallToolResult {
	t.Helper()
	tool := s.GetTool(name)
	if tool == nil {
		t.Fatalf("tool %q is not registered", name)
	}
	request := makeRequest(params)
	request.Params.Name = name
	result, err := tool.Handler(context.Background(), request)
	if err != nil {
		t.Fatalf("tool %q returned protocol error: %v", name, err)
	}
	if result == nil {
		t.Fatalf("tool %q returned nil result", name)
	}
	return result
}

// Mock server that simulates Audiobookshelf API
func setupMockABSServer() *httptest.Server {
	mux := http.NewServeMux()

	// Server endpoints
	mux.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"success": "true"})
	})

	mux.HandleFunc("/healthcheck", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]bool{"healthy": true})
	})

	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"isInit":   true,
			"language": "en-us",
		})
	})

	// Libraries endpoints
	mux.HandleFunc("/api/libraries", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodGet, http.MethodPost) {
			return
		}
		if r.Method == http.MethodPost {
			// Handle POST - create library
			var payload map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				http.Error(w, "Invalid JSON", http.StatusBadRequest)
				return
			}

			// Validate required fields
			if _, ok := payload["name"]; !ok {
				http.Error(w, "name is required", http.StatusBadRequest)
				return
			}
			if _, ok := payload["folders"]; !ok {
				http.Error(w, "folders is required", http.StatusBadRequest)
				return
			}

			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"id":      "new-lib-123",
				"name":    payload["name"],
				"folders": payload["folders"],
			})
			return
		}

		// Handle GET - list libraries
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"libraries": []map[string]string{
				{"id": "lib1", "name": "Audiobooks"},
				{"id": "lib2", "name": "Podcasts"},
			},
		})
	})

	mux.HandleFunc("/api/libraries/", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/libraries/"), "/")
		if len(parts) == 0 || parts[0] == "" {
			http.Error(w, "Library ID required", http.StatusBadRequest)
			return
		}

		libraryID := parts[0]

		if len(parts) == 1 {
			// Base library info
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"id":   libraryID,
				"name": "Test Library",
			})
			return
		}

		// Sub-resources
		subResource := parts[1]
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"libraryId": libraryID,
			"resource":  subResource,
			"data":      []interface{}{},
		})
	})

	// Items endpoints
	mux.HandleFunc("/api/items/", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		itemID := strings.TrimPrefix(r.URL.Path, "/api/items/")
		parts := strings.Split(itemID, "/")

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":   parts[0],
			"type": "book",
		})
	})

	// Authors endpoints
	mux.HandleFunc("/api/authors/", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/api/authors/")
		parts := strings.Split(path, "/")

		if len(parts) > 1 && parts[1] == "image" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("fake-image-data"))
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":   parts[0],
			"name": "Test Author",
		})
	})

	// Series endpoints
	mux.HandleFunc("/api/series/", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		seriesID := strings.TrimPrefix(r.URL.Path, "/api/series/")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":   seriesID,
			"name": "Test Series",
		})
	})

	// Users endpoints
	mux.HandleFunc("/api/users", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"users": []map[string]string{
				{"id": "user1", "username": "admin"},
			},
		})
	})

	mux.HandleFunc("/api/users/online", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"users": []map[string]string{},
		})
	})

	mux.HandleFunc("/api/users/", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/api/users/")
		parts := strings.Split(path, "/")

		if len(parts) == 1 {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"id":       parts[0],
				"username": "testuser",
			})
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"userId":   parts[0],
			"resource": parts[1],
		})
	})

	// Me endpoints
	mux.HandleFunc("/api/me", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":       "current-user",
			"username": "me",
		})
	})

	mux.HandleFunc("/api/me/listening-sessions", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"sessions": []interface{}{},
		})
	})

	mux.HandleFunc("/api/me/listening-stats", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"totalTime": 0,
		})
	})

	mux.HandleFunc("/api/me/items-in-progress", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"items": []interface{}{},
		})
	})

	mux.HandleFunc("/api/me/progress/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"progress": 0.5,
		})
	})

	// Sessions endpoints
	mux.HandleFunc("/api/sessions", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"sessions": []interface{}{},
		})
	})

	mux.HandleFunc("/api/sessions/", func(w http.ResponseWriter, r *http.Request) {
		sessionID := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id": sessionID,
		})
	})

	// Podcasts endpoints
	mux.HandleFunc("/api/podcasts", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"podcasts": []interface{}{},
		})
	})

	mux.HandleFunc("/api/podcasts/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id": "podcast1",
		})
	})

	// Collections endpoints
	mux.HandleFunc("/api/collections", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"collections": []interface{}{},
		})
	})

	mux.HandleFunc("/api/collections/", func(w http.ResponseWriter, r *http.Request) {
		collectionID := strings.TrimPrefix(r.URL.Path, "/api/collections/")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id": collectionID,
		})
	})

	// Playlists endpoints
	mux.HandleFunc("/api/playlists", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"playlists": []interface{}{},
		})
	})

	mux.HandleFunc("/api/playlists/", func(w http.ResponseWriter, r *http.Request) {
		playlistID := strings.TrimPrefix(r.URL.Path, "/api/playlists/")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id": playlistID,
		})
	})

	// Backups endpoint
	mux.HandleFunc("/api/backups", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"backups": []interface{}{},
		})
	})

	// Filesystem endpoint
	mux.HandleFunc("/api/filesystem", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"directories": []string{"/audiobooks", "/podcasts"},
		})
	})

	// Authorize endpoint
	mux.HandleFunc("/api/authorize", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"user":   map[string]string{"id": "user1"},
			"server": map[string]string{"version": "2.0.0"},
		})
	})

	// Tags endpoint
	mux.HandleFunc("/api/tags", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"tags": []string{"fiction", "non-fiction"},
		})
	})

	// Genres endpoint
	mux.HandleFunc("/api/genres", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"genres": []string{"Fantasy", "Science Fiction", "Mystery"},
		})
	})

	return httptest.NewServer(mux)
}

// Helper to create a basic request with auth
func makeRequest(params map[string]interface{}) mcp.CallToolRequest {
	return mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "",
			Arguments: params,
		},
	}
}

func TestGetEnvOrParam(t *testing.T) {
	tests := []struct {
		name       string
		paramValue string
		envKey     string
		envValue   string
		expected   string
	}{
		{
			name:       "prefer param over env",
			paramValue: "param_value",
			envKey:     "TEST_KEY",
			envValue:   "env_value",
			expected:   "param_value",
		},
		{
			name:       "use env when param empty",
			paramValue: "",
			envKey:     "TEST_KEY",
			envValue:   "env_value",
			expected:   "env_value",
		},
		{
			name:       "return empty when both empty",
			paramValue: "",
			envKey:     "TEST_KEY",
			envValue:   "",
			expected:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				os.Setenv(tt.envKey, tt.envValue)
				defer os.Unsetenv(tt.envKey)
			}

			result := getEnvOrParam(tt.paramValue, tt.envKey)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestNormalizeABSBaseURL(t *testing.T) {
	tests := []struct {
		name        string
		rawBaseURL  string
		expected    string
		expectError bool
	}{
		{
			name:       "plain server URL",
			rawBaseURL: "https://abs.example.com",
			expected:   "https://abs.example.com",
		},
		{
			name:       "trailing slashes",
			rawBaseURL: "https://abs.example.com///",
			expected:   "https://abs.example.com",
		},
		{
			name:       "api suffix",
			rawBaseURL: "https://abs.example.com/api",
			expected:   "https://abs.example.com",
		},
		{
			name:       "api suffix with trailing slash",
			rawBaseURL: "https://abs.example.com/api/",
			expected:   "https://abs.example.com",
		},
		{
			name:       "reverse proxy base path",
			rawBaseURL: "https://abs.example.com/abs",
			expected:   "https://abs.example.com/abs",
		},
		{
			name:       "reverse proxy base path with api suffix",
			rawBaseURL: "https://abs.example.com/abs/api",
			expected:   "https://abs.example.com/abs",
		},
		{
			name:       "trims whitespace",
			rawBaseURL: "  https://abs.example.com/abs/api/  ",
			expected:   "https://abs.example.com/abs",
		},
		{
			name:        "invalid URL",
			rawBaseURL:  "abs.example.com",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual, err := normalizeABSBaseURL(tt.rawBaseURL)

			if tt.expectError {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if actual != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, actual)
			}
		})
	}
}

func TestGetABSConfig(t *testing.T) {
	tests := []struct {
		name        string
		params      map[string]interface{}
		envBaseURL  string
		envToken    string
		expectError bool
		expectedURL string
	}{
		{
			name: "valid params",
			params: map[string]interface{}{
				"base_url": "https://abs.example.com",
				"token":    "test-token",
			},
			expectedURL: "https://abs.example.com/api",
		},
		{
			name: "valid params with api suffix",
			params: map[string]interface{}{
				"base_url": "https://abs.example.com/api/",
				"token":    "test-token",
			},
			expectedURL: "https://abs.example.com/api",
		},
		{
			name:        "use env vars",
			params:      map[string]interface{}{},
			envBaseURL:  "https://env.example.com",
			envToken:    "env-token",
			expectedURL: "https://env.example.com/api",
		},
		{
			name: "missing base_url",
			params: map[string]interface{}{
				"token": "test-token",
			},
			expectError: true,
		},
		{
			name: "missing token",
			params: map[string]interface{}{
				"base_url": "https://abs.example.com",
			},
			expectError: true,
		},
		{
			name: "invalid base_url",
			params: map[string]interface{}{
				"base_url": "abs.example.com",
				"token":    "test-token",
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("ABS_BASE_URL", tt.envBaseURL)
			t.Setenv("ABS_API_KEY", tt.envToken)

			request := makeRequest(tt.params)
			baseURL, token, err := getABSConfig(request)

			if tt.expectError {
				if err == nil {
					t.Error("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if baseURL != tt.expectedURL {
					t.Errorf("expected URL %q, got %q", tt.expectedURL, baseURL)
				}
				if token == "" {
					t.Error("expected non-empty token")
				}
			}
		})
	}
}

func TestABSGET(t *testing.T) {
	mockServer := setupMockABSServer()
	defer mockServer.Close()

	tests := []struct {
		name        string
		path        string
		expectError bool
		checkBody   func([]byte) error
	}{
		{
			name:        "successful GET request",
			path:        "/ping",
			expectError: false,
			checkBody: func(body []byte) error {
				if !strings.Contains(string(body), "success") {
					return fmt.Errorf("expected 'success' in body, got: %s", string(body))
				}
				return nil
			},
		},
		{
			name:        "libraries endpoint",
			path:        "/api/libraries",
			expectError: false,
			checkBody: func(body []byte) error {
				if !strings.Contains(string(body), "libraries") {
					return fmt.Errorf("expected 'libraries' in body, got: %s", string(body))
				}
				return nil
			},
		},
		{
			name:        "404 endpoint",
			path:        "/api/nonexistent",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := absGET(context.Background(), mockServer.URL, "test-token", tt.path)

			if tt.expectError {
				if err == nil {
					t.Error("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if tt.checkBody != nil {
					if err := tt.checkBody(body); err != nil {
						t.Error(err)
					}
				}
			}
		})
	}
}

func TestAPISuffixRoutesAPIAndRootHandlers(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/api/libraries", "/ping":
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"ok":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	request := makeRequest(map[string]interface{}{
		"base_url": server.URL + "/api/",
		"token":    "test-token",
	})

	apiResult, err := createSimpleGETHandler("/libraries")(context.Background(), request)
	if err != nil {
		t.Fatalf("unexpected API handler error: %v", err)
	}
	if apiResult == nil || apiResult.IsError {
		t.Fatalf("expected API handler success, got %#v", apiResult)
	}

	rootResult, err := createRootGETHandler("/ping")(context.Background(), request)
	if err != nil {
		t.Fatalf("unexpected root handler error: %v", err)
	}
	if rootResult == nil || rootResult.IsError {
		t.Fatalf("expected root handler success, got %#v", rootResult)
	}

	expectedPaths := []string{"/api/libraries", "/ping"}
	if strings.Join(paths, ",") != strings.Join(expectedPaths, ",") {
		t.Fatalf("expected paths %v, got %v", expectedPaths, paths)
	}
}

func assertRegisteredABSAuthSchema(t *testing.T, s *server.MCPServer, toolName string) {
	t.Helper()
	registered := s.GetTool(toolName)
	if registered == nil {
		t.Fatalf("tool %q is not registered", toolName)
	}

	baseURLProperty, ok := registered.Tool.InputSchema.Properties["base_url"]
	if !ok {
		t.Fatalf("tool %q input schema missing base_url", toolName)
	}
	baseURLSchema, ok := baseURLProperty.(map[string]interface{})
	if !ok {
		t.Fatalf("tool %q base_url schema has unexpected type %T", toolName, baseURLProperty)
	}
	if baseURLSchema["type"] != "string" {
		t.Fatalf("tool %q base_url type = %#v, want string", toolName, baseURLSchema["type"])
	}
	description, ok := baseURLSchema["description"].(string)
	if !ok {
		t.Fatalf("tool %q base_url description missing or not a string", toolName)
	}
	for _, expected := range []string{"server URL", "without /api", "https://abs.example.com/abs"} {
		if !strings.Contains(description, expected) {
			t.Fatalf("tool %q base_url description %q missing %q", toolName, description, expected)
		}
	}

	if _, ok := registered.Tool.InputSchema.Properties["token"]; !ok {
		t.Fatalf("tool %q input schema missing token", toolName)
	}
	for _, required := range registered.Tool.InputSchema.Required {
		if required == "base_url" || required == "token" {
			t.Fatalf("tool %q should not require %q because env vars may provide it", toolName, required)
		}
	}
}

func TestRegisteredABSAuthSchemaDocumentsServerURL(t *testing.T) {
	s := newMCPServer()
	assertRegisteredABSAuthSchema(t, s, "libraries")
	assertRegisteredABSAuthSchema(t, s, "ping")
}

func TestRegisteredToolsNormalizeBaseURLRouting(t *testing.T) {
	tests := []struct {
		name         string
		toolName     string
		baseURL      func(*recordingServer) string
		useEnv       bool
		expectedPath string
	}{
		{
			name:         "api tool plain server URL",
			toolName:     "libraries",
			baseURL:      func(rs *recordingServer) string { return rs.URL },
			expectedPath: "/api/libraries",
		},
		{
			name:         "api tool trailing slash",
			toolName:     "libraries",
			baseURL:      func(rs *recordingServer) string { return rs.URL + "///" },
			expectedPath: "/api/libraries",
		},
		{
			name:         "api tool api suffix",
			toolName:     "libraries",
			baseURL:      func(rs *recordingServer) string { return rs.URL + "/api/" },
			expectedPath: "/api/libraries",
		},
		{
			name:         "api tool reverse proxy base path",
			toolName:     "libraries",
			baseURL:      func(rs *recordingServer) string { return rs.URL + "/abs" },
			expectedPath: "/abs/api/libraries",
		},
		{
			name:         "api tool reverse proxy base path api suffix",
			toolName:     "libraries",
			baseURL:      func(rs *recordingServer) string { return rs.URL + "/abs/api/" },
			expectedPath: "/abs/api/libraries",
		},
		{
			name:         "api tool env var api suffix",
			toolName:     "libraries",
			baseURL:      func(rs *recordingServer) string { return rs.URL + "/api/" },
			useEnv:       true,
			expectedPath: "/api/libraries",
		},
		{
			name:         "root tool plain server URL",
			toolName:     "ping",
			baseURL:      func(rs *recordingServer) string { return rs.URL },
			expectedPath: "/ping",
		},
		{
			name:         "root tool trailing slash",
			toolName:     "ping",
			baseURL:      func(rs *recordingServer) string { return rs.URL + "///" },
			expectedPath: "/ping",
		},
		{
			name:         "root tool api suffix",
			toolName:     "ping",
			baseURL:      func(rs *recordingServer) string { return rs.URL + "/api/" },
			expectedPath: "/ping",
		},
		{
			name:         "root tool reverse proxy base path",
			toolName:     "ping",
			baseURL:      func(rs *recordingServer) string { return rs.URL + "/abs" },
			expectedPath: "/abs/ping",
		},
		{
			name:         "root tool reverse proxy base path api suffix",
			toolName:     "ping",
			baseURL:      func(rs *recordingServer) string { return rs.URL + "/abs/api/" },
			expectedPath: "/abs/ping",
		},
		{
			name:         "root tool env var api suffix",
			toolName:     "ping",
			baseURL:      func(rs *recordingServer) string { return rs.URL + "/api/" },
			useEnv:       true,
			expectedPath: "/ping",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newMCPServer()
			recorder := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
				if !requireMethod(w, r, http.MethodGet) {
					return
				}
				if r.URL.Path != tt.expectedPath {
					http.NotFound(w, r)
					return
				}
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]bool{"ok": true})
			})
			defer recorder.Close()

			params := map[string]interface{}{
				"token": "test-token",
			}
			baseURL := tt.baseURL(recorder)
			if tt.useEnv {
				t.Setenv("ABS_BASE_URL", baseURL)
			} else {
				params["base_url"] = baseURL
			}

			result := callRegisteredTool(t, s, tt.toolName, params)
			if result.IsError {
				t.Fatalf("expected tool success, got error result: %#v", result)
			}

			recorded, ok := recorder.LastRequest()
			if !ok {
				t.Fatal("expected recorded request")
			}
			if recorded.Method != http.MethodGet {
				t.Fatalf("expected GET request, got %s", recorded.Method)
			}
			if recorded.Path != tt.expectedPath {
				t.Fatalf("expected path %q, got %q", tt.expectedPath, recorded.Path)
			}
			if recorded.RawQuery != "" {
				t.Fatalf("expected empty query, got %q", recorded.RawQuery)
			}
			if len(recorded.Body) != 0 {
				t.Fatalf("expected empty GET body, got %q", string(recorded.Body))
			}
		})
	}
}

func toolResultText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	if result == nil || len(result.Content) == 0 {
		t.Fatalf("expected text content, got %#v", result)
	}
	textContent, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected text content, got %T", result.Content[0])
	}
	return textContent.Text
}

func TestRegisteredBaseURLValidationErrors(t *testing.T) {
	tests := []struct {
		name                  string
		toolName              string
		params                map[string]interface{}
		envBaseURL            string
		envToken              string
		expectedErrorContains string
	}{
		{
			name:     "api tool rejects URL without scheme",
			toolName: "libraries",
			params: map[string]interface{}{
				"base_url": "abs.example.com",
				"token":    "test-token",
			},
			expectedErrorContains: "absolute http(s) URL",
		},
		{
			name:     "api tool rejects non-http scheme",
			toolName: "libraries",
			params: map[string]interface{}{
				"base_url": "ftp://abs.example.com",
				"token":    "test-token",
			},
			expectedErrorContains: "http or https",
		},
		{
			name:     "api tool reports missing base URL",
			toolName: "libraries",
			params: map[string]interface{}{
				"token": "test-token",
			},
			expectedErrorContains: "base_url parameter or ABS_BASE_URL",
		},
		{
			name:     "root tool reports missing token",
			toolName: "ping",
			params: map[string]interface{}{
				"base_url": "http://example.invalid",
			},
			expectedErrorContains: "token parameter or ABS_API_KEY",
		},
		{
			name:     "root tool rejects URL without scheme",
			toolName: "ping",
			params: map[string]interface{}{
				"base_url": "abs.example.com",
				"token":    "test-token",
			},
			expectedErrorContains: "absolute http(s) URL",
		},
		{
			name:     "root tool rejects non-http scheme from env",
			toolName: "ping",
			params: map[string]interface{}{
				"token": "test-token",
			},
			envBaseURL:            "ftp://abs.example.com",
			expectedErrorContains: "http or https",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("ABS_BASE_URL", tt.envBaseURL)
			t.Setenv("ABS_API_KEY", tt.envToken)
			result := callRegisteredTool(t, newMCPServer(), tt.toolName, tt.params)
			if !result.IsError {
				t.Fatalf("expected validation error, got success: %#v", result)
			}
			if text := toolResultText(t, result); !strings.Contains(text, tt.expectedErrorContains) {
				t.Fatalf("expected error %q to contain %q", text, tt.expectedErrorContains)
			}
		})
	}
}

func TestRegisteredConfigValidationSkipsHTTPRequest(t *testing.T) {
	recorder := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("server should not be called when token is missing")
	})
	defer recorder.Close()

	result := callRegisteredTool(t, newMCPServer(), "ping", map[string]interface{}{
		"base_url": recorder.URL + "/api/",
	})
	if !result.IsError {
		t.Fatal("expected missing token to return a tool error")
	}
	if requests := recorder.Requests(); len(requests) != 0 {
		t.Fatalf("expected no outbound request, got %d", len(requests))
	}
}

func TestRegisteredABSNon2xxResponsesReturnToolErrors(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		path     string
	}{
		{name: "api tool", toolName: "libraries", path: "/api/libraries"},
		{name: "root tool", toolName: "ping", path: "/ping"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tt.path {
					http.NotFound(w, r)
					return
				}
				http.Error(w, "unavailable", http.StatusServiceUnavailable)
			})
			defer recorder.Close()

			result := callRegisteredTool(t, newMCPServer(), tt.toolName, map[string]interface{}{
				"base_url": recorder.URL,
				"token":    "test-token",
			})
			if !result.IsError {
				t.Fatalf("expected non-2xx response to return tool error, got %#v", result)
			}
			recorded, ok := recorder.LastRequest()
			if !ok {
				t.Fatal("expected recorded request")
			}
			if recorded.Path != tt.path {
				t.Fatalf("expected path %q, got %q", tt.path, recorded.Path)
			}
		})
	}
}

func TestEndpointHandlers(t *testing.T) {
	mockServer := setupMockABSServer()
	defer mockServer.Close()

	// Remove /api from mock server URL since getABSConfig adds it
	baseURL := strings.TrimSuffix(mockServer.URL, "/api")

	tests := []struct {
		name        string
		handler     func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)
		params      map[string]interface{}
		expectError bool
		checkResult func(*mcp.CallToolResult) error
	}{
		{
			name:    "libraries handler",
			handler: createSimpleGETHandler("/libraries"),
			params: map[string]interface{}{
				"base_url": baseURL,
				"token":    "test-token",
			},
			expectError: false,
			checkResult: func(result *mcp.CallToolResult) error {
				if len(result.Content) == 0 {
					return fmt.Errorf("expected content, got empty")
				}
				return nil
			},
		},
		{
			name:    "library by ID handler",
			handler: createGETByIDHandler("/libraries/%s", "library_id"),
			params: map[string]interface{}{
				"base_url":   baseURL,
				"token":      "test-token",
				"library_id": "lib123",
			},
			expectError: false,
			checkResult: func(result *mcp.CallToolResult) error {
				if len(result.Content) == 0 {
					return fmt.Errorf("expected content, got empty")
				}
				return nil
			},
		},
		{
			name:    "author handler",
			handler: createGETByIDHandler("/authors/%s", "author_id"),
			params: map[string]interface{}{
				"base_url":  baseURL,
				"token":     "test-token",
				"author_id": "author123",
			},
			expectError: false,
		},
		{
			name:    "series handler",
			handler: createGETByIDHandler("/series/%s", "series_id"),
			params: map[string]interface{}{
				"base_url":  baseURL,
				"token":     "test-token",
				"series_id": "series123",
			},
			expectError: false,
		},
		{
			name:    "users handler",
			handler: createSimpleGETHandler("/users"),
			params: map[string]interface{}{
				"base_url": baseURL,
				"token":    "test-token",
			},
			expectError: false,
		},
		{
			name:    "tags handler",
			handler: createSimpleGETHandler("/tags"),
			params: map[string]interface{}{
				"base_url": baseURL,
				"token":    "test-token",
			},
			expectError: false,
		},
		{
			name:    "genres handler",
			handler: createSimpleGETHandler("/genres"),
			params: map[string]interface{}{
				"base_url": baseURL,
				"token":    "test-token",
			},
			expectError: false,
		},
		{
			name:    "missing required ID parameter",
			handler: createGETByIDHandler("/libraries/%s", "library_id"),
			params: map[string]interface{}{
				"base_url": baseURL,
				"token":    "test-token",
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := makeRequest(tt.params)
			result, err := tt.handler(context.Background(), request)

			if tt.expectError {
				if err != nil || (result != nil && result.IsError) {
					// Expected error
					return
				}
				t.Error("expected error, got success")
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if result == nil {
					t.Error("expected result, got nil")
				}
				if result.IsError {
					t.Errorf("result returned error: %v", result)
				}
				if tt.checkResult != nil {
					if err := tt.checkResult(result); err != nil {
						t.Error(err)
					}
				}
			}
		})
	}
}

func TestSubResourceHandlers(t *testing.T) {
	tests := []struct {
		name           string
		basePath       string
		idParamName    string
		subResources   []string
		params         map[string]interface{}
		expectedPath   string
		expectedInPath string
	}{
		{
			name:         "library with items sub-resource",
			basePath:     "/libraries/%s",
			idParamName:  "library_id",
			subResources: []string{"items", "authors", "series"},
			params: map[string]interface{}{
				"token":      "test-token",
				"library_id": "lib123",
				"items":      true,
			},
			expectedPath:   "/api/libraries/lib123/items",
			expectedInPath: "items",
		},
		{
			name:         "library without sub-resource",
			basePath:     "/libraries/%s",
			idParamName:  "library_id",
			subResources: []string{"items", "authors"},
			params: map[string]interface{}{
				"token":      "test-token",
				"library_id": "lib123",
			},
			expectedPath:   "/api/libraries/lib123",
			expectedInPath: "lib123",
		},
		{
			name:         "user with listening-sessions",
			basePath:     "/users/%s",
			idParamName:  "user_id",
			subResources: []string{"listening-sessions", "listening-stats"},
			params: map[string]interface{}{
				"token":              "test-token",
				"user_id":            "user123",
				"listening-sessions": true,
			},
			expectedPath:   "/api/users/user123/listening-sessions",
			expectedInPath: "listening-sessions",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
				if !requireMethod(w, r, http.MethodGet) {
					return
				}
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"path": r.URL.Path,
				})
			})
			defer recorder.Close()

			params := make(map[string]interface{}, len(tt.params)+1)
			for key, value := range tt.params {
				params[key] = value
			}
			params["base_url"] = recorder.URL

			handler := createGETByIDWithSubResourceHandler(tt.basePath, tt.idParamName, tt.subResources)
			request := makeRequest(params)
			result, err := handler(context.Background(), request)

			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if result == nil {
				t.Error("expected result, got nil")
			}
			if result.IsError {
				t.Errorf("result returned error: %v", result)
			}
			if len(result.Content) == 0 {
				t.Error("expected content, got empty")
			}

			recorded, ok := recorder.LastRequest()
			if !ok {
				t.Fatal("expected recorded request")
			}
			if recorded.Method != http.MethodGet {
				t.Fatalf("expected GET request, got %s", recorded.Method)
			}
			if recorded.Path != tt.expectedPath {
				t.Fatalf("expected path %q, got %q", tt.expectedPath, recorded.Path)
			}
			if !strings.Contains(recorded.Path, tt.expectedInPath) {
				t.Fatalf("expected path %q to contain %q", recorded.Path, tt.expectedInPath)
			}
			if recorded.RawQuery != "" {
				t.Fatalf("expected empty query, got %q", recorded.RawQuery)
			}
		})
	}
}

func TestAuthorizationHeader(t *testing.T) {
	// Create a test server that checks the Authorization header
	called := false
	var receivedAuth string

	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		receivedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer testServer.Close()

	expectedToken := "test-bearer-token"
	_, err := absGET(context.Background(), testServer.URL, expectedToken, "/test")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !called {
		t.Error("test server was not called")
	}

	expectedAuth := "Bearer " + expectedToken
	if receivedAuth != expectedAuth {
		t.Errorf("expected Authorization header %q, got %q", expectedAuth, receivedAuth)
	}
}

func TestContextCancellation(t *testing.T) {
	// Create a server that delays response
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer testServer.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := absGET(ctx, testServer.URL, "token", "/test")
	if err == nil {
		t.Error("expected error from cancelled context, got nil")
	}
}

func TestABSPOST(t *testing.T) {
	mockServer := setupMockABSServer()
	defer mockServer.Close()

	tests := []struct {
		name        string
		path        string
		payload     map[string]interface{}
		expectError bool
		checkBody   func([]byte) error
	}{
		{
			name: "successful POST with payload",
			path: "/api/libraries",
			payload: map[string]interface{}{
				"name": "Test Library",
				"folders": []map[string]interface{}{
					{"fullPath": "/audiobooks"},
				},
			},
			expectError: false,
			checkBody: func(body []byte) error {
				if !strings.Contains(string(body), "new-lib-123") {
					return fmt.Errorf("expected 'new-lib-123' in body, got: %s", string(body))
				}
				if !strings.Contains(string(body), "Test Library") {
					return fmt.Errorf("expected 'Test Library' in body, got: %s", string(body))
				}
				return nil
			},
		},
		{
			name:        "POST with nil payload",
			path:        "/api/libraries",
			payload:     nil,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := absPOST(context.Background(), mockServer.URL, "test-token", tt.path, tt.payload)

			if tt.expectError {
				if err == nil {
					t.Error("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if tt.checkBody != nil {
					if err := tt.checkBody(body); err != nil {
						t.Error(err)
					}
				}
			}
		})
	}
}

func TestRegisteredAuthorizeToolUsesPOST(t *testing.T) {
	s := newMCPServer()
	tool := s.GetTool("authorize")
	if tool == nil {
		t.Fatal("authorize tool is not registered")
	}
	if _, ok := tool.Tool.InputSchema.Properties["base_url"]; !ok {
		t.Fatal("authorize schema missing base_url property")
	}
	if _, ok := tool.Tool.InputSchema.Properties["token"]; !ok {
		t.Fatal("authorize schema missing token property")
	}
	if len(tool.Tool.InputSchema.Required) != 0 {
		t.Fatalf("expected authorize to have no required schema parameters, got %v", tool.Tool.InputSchema.Required)
	}

	recorder := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		if r.URL.Path != "/api/authorize" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"user":   map[string]string{"id": "user1"},
			"server": map[string]string{"version": "2.0.0"},
		})
	})
	defer recorder.Close()

	resp, err := http.Get(recorder.URL + "/api/authorize")
	if err != nil {
		t.Fatalf("unexpected GET error: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected GET /api/authorize to return 405, got %d", resp.StatusCode)
	}

	result := callRegisteredTool(t, s, "authorize", map[string]interface{}{
		"base_url": recorder.URL,
		"token":    "test-token",
	})
	if result.IsError {
		t.Fatalf("result returned error: %v", result)
	}
	if len(result.Content) == 0 {
		t.Fatal("expected content, got empty")
	}
	content, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected text content, got %T", result.Content[0])
	}
	if !strings.Contains(content.Text, `"user"`) || !strings.Contains(content.Text, `"server"`) {
		t.Fatalf("expected authorize response, got: %s", content.Text)
	}

	recorded, ok := recorder.LastRequest()
	if !ok {
		t.Fatal("expected recorded request")
	}
	if recorded.Method != http.MethodPost {
		t.Fatalf("expected POST request, got %s", recorded.Method)
	}
	if recorded.Path != "/api/authorize" {
		t.Fatalf("expected /api/authorize path, got %s", recorded.Path)
	}
	if recorded.RawQuery != "" {
		t.Fatalf("expected empty query, got %q", recorded.RawQuery)
	}
	if len(recorded.Body) != 0 {
		t.Fatalf("expected empty request body, got %q", string(recorded.Body))
	}
}

func TestRegisteredAuthorizeToolErrors(t *testing.T) {
	s := newMCPServer()

	t.Run("missing config returns tool error without request", func(t *testing.T) {
		recorder := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		})
		defer recorder.Close()

		result := callRegisteredTool(t, s, "authorize", map[string]interface{}{
			"token": "test-token",
		})
		if !result.IsError {
			t.Fatal("expected missing base_url to return a tool error")
		}
		if requests := recorder.Requests(); len(requests) != 0 {
			t.Fatalf("expected no requests, got %d", len(requests))
		}
	})

	t.Run("ABS non-2xx returns tool error", func(t *testing.T) {
		recorder := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
			if !requireMethod(w, r, http.MethodPost) {
				return
			}
			if r.URL.Path != "/api/authorize" {
				http.NotFound(w, r)
				return
			}
			http.Error(w, "denied", http.StatusUnauthorized)
		})
		defer recorder.Close()

		result := callRegisteredTool(t, s, "authorize", map[string]interface{}{
			"base_url": recorder.URL,
			"token":    "test-token",
		})
		if !result.IsError {
			t.Fatal("expected non-2xx response to return a tool error")
		}

		recorded, ok := recorder.LastRequest()
		if !ok {
			t.Fatal("expected recorded request")
		}
		if recorded.Method != http.MethodPost {
			t.Fatalf("expected POST request, got %s", recorded.Method)
		}
		if recorded.Path != "/api/authorize" {
			t.Fatalf("expected /api/authorize path, got %s", recorded.Path)
		}
	})
}

func TestABSPATCH(t *testing.T) {
	t.Run("successful PATCH with payload", func(t *testing.T) {
		var gotMethod, gotPath string
		var gotBody map[string]interface{}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotMethod = r.Method
			gotPath = r.URL.Path
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
		}))
		defer server.Close()

		_, err := absPATCH(context.Background(), server.URL, "test-token", "/me/progress/item-123", map[string]interface{}{
			"currentTime": 60.0,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if gotMethod != http.MethodPatch {
			t.Errorf("expected PATCH request, got %s", gotMethod)
		}
		if gotPath != "/me/progress/item-123" {
			t.Errorf("expected path with item ID, got %s", gotPath)
		}
		if gotBody["currentTime"] != 60.0 {
			t.Errorf("expected currentTime 60 in body, got %v", gotBody["currentTime"])
		}
	})

	t.Run("marshal error", func(t *testing.T) {
		_, err := absPATCH(context.Background(), "http://example.invalid", "test-token", "/me/progress/item-123", map[string]interface{}{
			"bad": make(chan int),
		})
		if err == nil || !strings.Contains(err.Error(), "marshal payload") {
			t.Fatalf("expected marshal payload error, got %v", err)
		}
	})

	t.Run("build request error", func(t *testing.T) {
		_, err := absPATCH(context.Background(), "%", "test-token", "/me/progress/item-123", map[string]interface{}{
			"currentTime": 60.0,
		})
		if err == nil || !strings.Contains(err.Error(), "build request") {
			t.Fatalf("expected build request error, got %v", err)
		}
	})

	t.Run("transport error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		baseURL := server.URL
		server.Close()

		_, err := absPATCH(context.Background(), baseURL, "test-token", "/me/progress/item-123", map[string]interface{}{
			"currentTime": 60.0,
		})
		if err == nil || !strings.Contains(err.Error(), "call ABS API") {
			t.Fatalf("expected call ABS API error, got %v", err)
		}
	})

	t.Run("non-2xx status", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "bad progress", http.StatusBadRequest)
		}))
		defer server.Close()

		_, err := absPATCH(context.Background(), server.URL, "test-token", "/me/progress/item-123", map[string]interface{}{
			"currentTime": 60.0,
		})
		if err == nil || !strings.Contains(err.Error(), "ABS API returned 400 Bad Request") || !strings.Contains(err.Error(), "bad progress") {
			t.Fatalf("expected non-2xx error with response body, got %v", err)
		}
	})

	t.Run("read error", func(t *testing.T) {
		oldClient := httpClient
		httpClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body:       failingReadCloser{},
				Header:     make(http.Header),
			}, nil
		})}
		defer func() { httpClient = oldClient }()

		_, err := absPATCH(context.Background(), "http://abs.example", "test-token", "/me/progress/item-123", map[string]interface{}{
			"currentTime": 60.0,
		})
		if err == nil || !strings.Contains(err.Error(), "read response") {
			t.Fatalf("expected read response error, got %v", err)
		}
	})
}

// TestUpdateProgressHandler exercises the registered update_progress MCP handler.
func TestUpdateProgressHandler(t *testing.T) {
	s := newMCPServer()

	tests := []struct {
		name             string
		args             map[string]interface{}
		expectedPath     string
		expectedProgress float64
		expectedFinished *bool
	}{
		{
			name: "book progress omits isFinished when omitted",
			args: map[string]interface{}{
				"token":    "test-token",
				"item_id":  "book-123",
				"progress": 60.0,
				"duration": 9596.754,
			},
			expectedPath:     "/api/me/progress/book-123",
			expectedProgress: 60.0,
		},
		{
			name: "episode progress uses episode path",
			args: map[string]interface{}{
				"token":      "test-token",
				"item_id":    "podcast-123",
				"episode_id": "episode-456",
				"progress":   30.0,
			},
			expectedPath:     "/api/me/progress/podcast-123/episode-456",
			expectedProgress: 30.0,
		},
		{
			name: "sends isFinished true",
			args: map[string]interface{}{
				"token":       "test-token",
				"item_id":     "book-true",
				"progress":    42.0,
				"is_finished": true,
			},
			expectedPath:     "/api/me/progress/book-true",
			expectedProgress: 42.0,
			expectedFinished: boolPtr(true),
		},
		{
			name: "sends explicit isFinished false",
			args: map[string]interface{}{
				"token":       "test-token",
				"item_id":     "book-false",
				"progress":    24.0,
				"is_finished": false,
			},
			expectedPath:     "/api/me/progress/book-false",
			expectedProgress: 24.0,
			expectedFinished: boolPtr(false),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
				if !requireMethod(w, r, http.MethodPatch) {
					return
				}
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
			})
			defer recorder.Close()

			params := make(map[string]interface{}, len(tt.args)+1)
			for key, value := range tt.args {
				params[key] = value
			}
			params["base_url"] = recorder.URL

			result := callRegisteredTool(t, s, "update_progress", params)
			if result.IsError {
				t.Fatalf("result returned error: %v", result)
			}

			got, ok := recorder.LastRequest()
			if !ok {
				t.Fatal("expected recorded request")
			}
			if got.Method != http.MethodPatch {
				t.Errorf("expected PATCH request, got %s", got.Method)
			}
			if got.Path != tt.expectedPath {
				t.Errorf("expected path %q, got %q", tt.expectedPath, got.Path)
			}
			if got.RawQuery != "" {
				t.Errorf("expected empty query, got %q", got.RawQuery)
			}

			var body map[string]interface{}
			if err := json.Unmarshal(got.Body, &body); err != nil {
				t.Fatalf("decode recorded body: %v", err)
			}
			if body["currentTime"] != tt.expectedProgress {
				t.Errorf("expected currentTime %v in body, got %v", tt.expectedProgress, body["currentTime"])
			}
			if _, ok := body["libraryItemId"]; ok {
				t.Errorf("libraryItemId should not be sent in body, item ID belongs in the URL path")
			}
			if _, ok := body["episodeId"]; ok {
				t.Errorf("episodeId should not be sent in body, episode ID belongs in the URL path")
			}

			gotFinished, hasFinished := body["isFinished"]
			if tt.expectedFinished == nil {
				if hasFinished {
					t.Errorf("isFinished should be omitted when is_finished is not supplied, got %v", gotFinished)
				}
				return
			}
			if !hasFinished {
				t.Fatalf("expected isFinished %v in body, got no field", *tt.expectedFinished)
			}
			if gotFinished != *tt.expectedFinished {
				t.Errorf("expected isFinished %v in body, got %v", *tt.expectedFinished, gotFinished)
			}
		})
	}
}

func TestUpdateProgressSchema(t *testing.T) {
	tool := newMCPServer().GetTool("update_progress")
	if tool == nil {
		t.Fatal("update_progress tool is not registered")
	}

	for _, name := range []string{"base_url", "token", "item_id", "progress", "duration", "is_finished", "episode_id"} {
		if _, ok := tool.Tool.InputSchema.Properties[name]; !ok {
			t.Fatalf("expected update_progress schema property %q", name)
		}
	}
	for _, name := range []string{"item_id", "progress"} {
		if !containsString(tool.Tool.InputSchema.Required, name) {
			t.Fatalf("expected update_progress schema required parameter %q in %v", name, tool.Tool.InputSchema.Required)
		}
	}
}

func TestUpdateProgressHandlerErrors(t *testing.T) {
	t.Setenv("ABS_BASE_URL", "")
	t.Setenv("ABS_API_KEY", "")
	s := newMCPServer()

	t.Run("missing base_url fails before request", func(t *testing.T) {
		result := callRegisteredTool(t, s, "update_progress", map[string]interface{}{
			"token":    "test-token",
			"item_id":  "book-123",
			"progress": 60.0,
		})
		if !result.IsError {
			t.Fatalf("expected error result, got %v", result)
		}
	})

	t.Run("missing token fails before request", func(t *testing.T) {
		recorder := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
			t.Fatalf("validation should fail before sending a request")
		})
		defer recorder.Close()

		result := callRegisteredTool(t, s, "update_progress", map[string]interface{}{
			"base_url": recorder.URL,
			"item_id":  "book-123",
			"progress": 60.0,
		})
		if !result.IsError {
			t.Fatalf("expected error result, got %v", result)
		}
		if got := len(recorder.Requests()); got != 0 {
			t.Fatalf("expected no requests, got %d", got)
		}
	})

	validationTests := []struct {
		name string
		args map[string]interface{}
	}{
		{
			name: "missing item_id",
			args: map[string]interface{}{
				"token":    "test-token",
				"progress": 60.0,
			},
		},
		{
			name: "missing progress",
			args: map[string]interface{}{
				"token":   "test-token",
				"item_id": "book-123",
			},
		},
	}
	for _, tt := range validationTests {
		t.Run(tt.name+" fails before request", func(t *testing.T) {
			recorder := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
				t.Fatalf("validation should fail before sending a request")
			})
			defer recorder.Close()

			params := make(map[string]interface{}, len(tt.args)+1)
			for key, value := range tt.args {
				params[key] = value
			}
			params["base_url"] = recorder.URL

			result := callRegisteredTool(t, s, "update_progress", params)
			if !result.IsError {
				t.Fatalf("expected error result, got %v", result)
			}
			if got := len(recorder.Requests()); got != 0 {
				t.Fatalf("expected no requests, got %d", got)
			}
		})
	}

	t.Run("non-2xx ABS response returns tool error", func(t *testing.T) {
		recorder := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
			if !requireMethod(w, r, http.MethodPatch) {
				return
			}
			http.Error(w, "bad progress", http.StatusBadRequest)
		})
		defer recorder.Close()

		result := callRegisteredTool(t, s, "update_progress", map[string]interface{}{
			"base_url": recorder.URL,
			"token":    "test-token",
			"item_id":  "book-123",
			"progress": 60.0,
		})
		if !result.IsError {
			t.Fatalf("expected error result, got %v", result)
		}
		if got := len(recorder.Requests()); got != 1 {
			t.Fatalf("expected one request, got %d", got)
		}
	})

	t.Run("transport error returns tool error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		baseURL := server.URL
		server.Close()

		result := callRegisteredTool(t, s, "update_progress", map[string]interface{}{
			"base_url": baseURL,
			"token":    "test-token",
			"item_id":  "book-123",
			"progress": 60.0,
		})
		if !result.IsError {
			t.Fatalf("expected error result, got %v", result)
		}
	})
}

func boolPtr(v bool) *bool {
	return &v
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type failingReadCloser struct{}

func (failingReadCloser) Read([]byte) (int, error) {
	return 0, fmt.Errorf("read failed")
}

func (failingReadCloser) Close() error {
	return nil
}

func TestCreateLibraryHandler(t *testing.T) {
	s := newMCPServer()

	t.Run("create library successfully", func(t *testing.T) {
		recorder := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
			if !requireMethod(w, r, http.MethodPost) {
				return
			}
			if r.URL.Path != "/api/libraries" {
				http.NotFound(w, r)
				return
			}

			var payload map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				http.Error(w, "invalid json", http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"id":      "new-lib-123",
				"name":    payload["name"],
				"folders": payload["folders"],
			})
		})
		defer recorder.Close()

		result := callRegisteredTool(t, s, "create_library", map[string]interface{}{
			"base_url":   recorder.URL,
			"token":      "test-token",
			"name":       "My Audiobooks",
			"folders":    "/audiobooks, /more-audiobooks",
			"media_type": "book",
			"icon":       "audiobooks",
			"provider":   "audible",
		})
		if result.IsError {
			t.Fatalf("result returned error: %v", result)
		}
		if len(result.Content) == 0 {
			t.Fatal("expected content, got empty")
		}
		if textContent, ok := result.Content[0].(mcp.TextContent); ok {
			if !strings.Contains(textContent.Text, "My Audiobooks") {
				t.Fatalf("expected response to contain library name, got: %s", textContent.Text)
			}
		} else {
			t.Fatalf("expected text content, got %T", result.Content[0])
		}

		recorded, ok := recorder.LastRequest()
		if !ok {
			t.Fatal("expected recorded request")
		}
		if recorded.Method != http.MethodPost {
			t.Fatalf("expected POST request, got %s", recorded.Method)
		}
		if recorded.Path != "/api/libraries" {
			t.Fatalf("expected /api/libraries path, got %s", recorded.Path)
		}
		if recorded.RawQuery != "" {
			t.Fatalf("expected empty query, got %q", recorded.RawQuery)
		}

		var payload map[string]interface{}
		if err := json.Unmarshal(recorded.Body, &payload); err != nil {
			t.Fatalf("decode recorded payload: %v", err)
		}
		if payload["name"] != "My Audiobooks" {
			t.Fatalf("expected name payload, got %#v", payload["name"])
		}
		if payload["mediaType"] != "book" {
			t.Fatalf("expected mediaType book, got %#v", payload["mediaType"])
		}
		if payload["icon"] != "audiobooks" {
			t.Fatalf("expected icon audiobooks, got %#v", payload["icon"])
		}
		if payload["provider"] != "audible" {
			t.Fatalf("expected provider audible, got %#v", payload["provider"])
		}
		folders, ok := payload["folders"].([]interface{})
		if !ok || len(folders) != 2 {
			t.Fatalf("expected two folders, got %#v", payload["folders"])
		}
		for i, expected := range []string{"/audiobooks", "/more-audiobooks"} {
			folder, ok := folders[i].(map[string]interface{})
			if !ok {
				t.Fatalf("folder %d has unexpected type %T", i, folders[i])
			}
			if folder["fullPath"] != expected {
				t.Fatalf("expected folder %d fullPath %q, got %#v", i, expected, folder["fullPath"])
			}
		}
	})

	t.Run("missing required media_type parameter", func(t *testing.T) {
		recorder := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
			t.Fatalf("server should not be called when media_type is missing")
		})
		defer recorder.Close()

		result := callRegisteredTool(t, s, "create_library", map[string]interface{}{
			"base_url": recorder.URL,
			"token":    "test-token",
			"name":     "My Library",
			"folders":  "/audiobooks",
		})
		if !result.IsError {
			t.Fatal("expected missing media_type to return a tool error")
		}
		if requests := recorder.Requests(); len(requests) != 0 {
			t.Fatalf("expected no outbound request, got %d", len(requests))
		}
	})

	t.Run("missing required name parameter", func(t *testing.T) {
		result := callRegisteredTool(t, s, "create_library", map[string]interface{}{
			"base_url":   "http://example.invalid",
			"token":      "test-token",
			"folders":    "/audiobooks",
			"media_type": "book",
		})
		if !result.IsError {
			t.Fatal("expected missing name to return a tool error")
		}
	})

	t.Run("missing required folders parameter", func(t *testing.T) {
		result := callRegisteredTool(t, s, "create_library", map[string]interface{}{
			"base_url":   "http://example.invalid",
			"token":      "test-token",
			"name":       "My Library",
			"media_type": "book",
		})
		if !result.IsError {
			t.Fatal("expected missing folders to return a tool error")
		}
	})
}

func TestRegisteredToolNames(t *testing.T) {
	s := newMCPServer()
	tools := s.ListTools()

	expected := []string{
		"libraries",
		"library",
		"create_library",
		"item",
		"author",
		"me",
		"sessions",
		"session",
		"podcasts",
		"podcast",
		"collections",
		"collection",
		"create_collection",
		"add_to_collection",
		"playlists",
		"playlist",
		"create_playlist",
		"add_to_playlist",
		"check_podcast_episodes",
		"create_backup",
		"update_progress",
		"update_item_media",
		"batch_update_items",
		"match_item",
		"search_books",
		"set_item_cover",
		"compare_covers",
		"delete_item",
		"search_chapters",
		"update_item_chapters",
		"apply_audible_chapters",
		"reorder_tracks",
		"ping",
		"healthcheck",
		"status",
		"users",
		"users_online",
		"user",
		"series",
		"author_image",
		"backups",
		"filesystem",
		"authorize",
		"tags",
		"genres",
	}

	if len(tools) != len(expected) {
		t.Fatalf("expected %d registered tools, got %d", len(expected), len(tools))
	}
	for _, name := range expected {
		tool, ok := tools[name]
		if !ok {
			t.Fatalf("expected tool %q to be registered", name)
		}
		if tool.Tool.Name != name {
			t.Fatalf("expected registered tool %q to report name %q, got %q", name, name, tool.Tool.Name)
		}
		if tool.Handler == nil {
			t.Fatalf("expected tool %q to have a handler", name)
		}
	}
}
