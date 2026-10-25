package main

import (
	"encoding/json"
	"math"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func approxEqual(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// decodeResultText decodes the JSON text of a tool result into v.
func decodeResultText(t *testing.T, result *mcp.CallToolResult, v interface{}) {
	t.Helper()
	if len(result.Content) == 0 {
		t.Fatal("tool result has no content")
	}
	text, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected text content, got %T", result.Content[0])
	}
	if err := json.Unmarshal([]byte(text.Text), v); err != nil {
		t.Fatalf("decode result %q: %v", text.Text, err)
	}
}

func TestRemoveAudibleBranding(t *testing.T) {
	data := &audibleChapterData{
		BrandIntroDurationMs: 2000,
		BrandOutroDurationMs: 5000,
		RuntimeLengthMs:      1807000,
		Chapters: []audibleChapter{
			{Title: "Kapitel 1", StartOffsetMs: 0, LengthMs: 600000},
			{Title: "Kapitel 2", StartOffsetMs: 600000, LengthMs: 600000},
			{Title: "Kapitel 3", StartOffsetMs: 1200000, LengthMs: 602000},
			{Title: "Outro", StartOffsetMs: 1802000, LengthMs: 4000},
		},
	}

	removeAudibleBranding(data)

	var starts []int64
	for _, chapter := range data.Chapters {
		starts = append(starts, chapter.StartOffsetMs)
	}
	if want := []int64{0, 598000, 1198000}; !reflect.DeepEqual(starts, want) {
		t.Errorf("expected starts %v, got %v", want, starts)
	}
	if data.RuntimeLengthMs != 1800000 {
		t.Errorf("expected runtime 1800000, got %d", data.RuntimeLengthMs)
	}
}

func TestRemoveAudibleBrandingKeepsLongLastChapter(t *testing.T) {
	data := &audibleChapterData{
		BrandIntroDurationMs: 2000,
		BrandOutroDurationMs: 5000,
		Chapters: []audibleChapter{
			{Title: "Kapitel 1", StartOffsetMs: 0, LengthMs: 600000},
			{Title: "Epilog", StartOffsetMs: 600000, LengthMs: 90000},
		},
	}

	removeAudibleBranding(data)

	if len(data.Chapters) != 2 {
		t.Errorf("expected both chapters to stay, got %d", len(data.Chapters))
	}
}

func TestBuildFullChaptersSnapsToTrackBoundaries(t *testing.T) {
	audible := []audibleChapter{
		{Title: "A", StartOffsetMs: 0},
		{Title: "B", StartOffsetMs: 598000},
		{Title: "C", StartOffsetMs: 1203000},
		{Title: "D", StartOffsetMs: 1500000},
		{Title: "E", StartOffsetMs: 1900000},
	}
	trackStarts := []float64{0, 600, 1200}

	chapters, warnings := buildFullChapters(audible, 1800, trackStarts, 10)

	type view struct {
		Title      string
		Start, End float64
		Snapped    bool
	}
	var got []view
	for _, chapter := range chapters {
		got = append(got, view{chapter.Title, chapter.Start, chapter.End, chapter.SnappedFrom != nil})
	}
	want := []view{
		{"A", 0, 600, false},
		{"B", 600, 1200, true},
		{"C", 1200, 1497, true},
		{"D", 1497, 1800, false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("unexpected chapters\n got: %+v\nwant: %+v", got, want)
	}
	if chapters[1].SnappedFrom == nil || !approxEqual(*chapters[1].SnappedFrom, 598) {
		t.Errorf("expected chapter B to remember its original start 598")
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], `"E"`) {
		t.Errorf("expected one warning about dropping E, got %v", warnings)
	}
	for i, chapter := range chapters {
		if chapter.Index != i {
			t.Errorf("chapter %d has index %d", i, chapter.Index)
		}
	}
}

func TestBuildFullChaptersWithoutSnapping(t *testing.T) {
	audible := []audibleChapter{
		{Title: "A", StartOffsetMs: 0},
		{Title: "B", StartOffsetMs: 598000},
	}

	chapters, _ := buildFullChapters(audible, 1200, []float64{0, 600}, 0)

	if !approxEqual(chapters[1].Start, 598) || chapters[1].SnappedFrom != nil {
		t.Errorf("expected no snapping with tolerance 0, got %+v", chapters[1])
	}
}

func TestBuildFullChaptersNeverSnapsOntoPreviousStart(t *testing.T) {
	audible := []audibleChapter{
		{Title: "A", StartOffsetMs: 0},
		{Title: "B", StartOffsetMs: 598000},
		{Title: "C", StartOffsetMs: 601000},
	}

	chapters, warnings := buildFullChapters(audible, 1200, []float64{0, 600}, 10)

	if len(chapters) != 3 || len(warnings) != 0 {
		t.Fatalf("expected 3 chapters and no warnings, got %d chapters, warnings %v", len(chapters), warnings)
	}
	if chapters[2].SnappedFrom != nil {
		t.Errorf("a second chapter must not snap onto the same boundary, got %+v", chapters[2])
	}
	if !approxEqual(chapters[1].Start, 600) || !approxEqual(chapters[2].Start, 603) {
		t.Errorf("expected starts 600 and 603 (shifted like its snapped neighbour), got %v and %v", chapters[1].Start, chapters[2].Start)
	}
}

func TestBuildTitleOnlyChapters(t *testing.T) {
	existing := []absChapter{
		{ID: 1, Start: 600, End: 1200, Title: "file 2"},
		{ID: 0, Start: 0, End: 600, Title: "file 1"},
	}
	audible := []audibleChapter{{Title: "Kapitel 1"}, {Title: "Kapitel 2"}}

	chapters, err := buildTitleOnlyChapters(existing, audible, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []plannedChapter{
		{Index: 0, Title: "Kapitel 1", OldTitle: "file 1", Start: 0, End: 600},
		{Index: 1, Title: "Kapitel 2", OldTitle: "file 2", Start: 600, End: 1200},
	}
	if !reflect.DeepEqual(chapters, want) {
		t.Errorf("unexpected chapters\n got: %+v\nwant: %+v", chapters, want)
	}
}

func TestBuildTitleOnlyChaptersCountMismatch(t *testing.T) {
	existing := []absChapter{
		{Start: 0, End: 600, Title: "file 1"},
		{Start: 600, End: 1200, Title: "file 2"},
		{Start: 1200, End: 1800, Title: "file 3"},
	}
	audible := []audibleChapter{{Title: "Kapitel 1"}, {Title: "Kapitel 2"}}

	if _, err := buildTitleOnlyChapters(existing, audible, false); err == nil {
		t.Error("expected an error when the counts differ")
	}

	chapters, err := buildTitleOnlyChapters(existing, audible, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := []string{chapters[0].Title, chapters[1].Title, chapters[2].Title}; !reflect.DeepEqual(got, []string{"Kapitel 1", "Kapitel 2", "file 3"}) {
		t.Errorf("unexpected titles %v", got)
	}

	if _, err := buildTitleOnlyChapters(nil, audible, true); err == nil {
		t.Error("expected an error for an item without chapters")
	}
}

// bookItemJSON builds an ABS item with the given files, each 600s long.
func bookItemJSON(asin string, chapters []map[string]interface{}, filenames ...string) map[string]interface{} {
	files := make([]map[string]interface{}, 0, len(filenames))
	for i, name := range filenames {
		files = append(files, map[string]interface{}{
			"index":    i + 1,
			"ino":      "ino-" + name,
			"duration": 600.0,
			"exclude":  false,
			"metadata": map[string]interface{}{"filename": name},
		})
	}
	return map[string]interface{}{
		"media": map[string]interface{}{
			"metadata":   map[string]interface{}{"title": "Das Paket", "asin": asin},
			"audioFiles": files,
			"chapters":   chapters,
		},
	}
}

func chapterFixtureServer(t *testing.T, item map[string]interface{}, audible map[string]interface{}) *recordingServer {
	t.Helper()
	return newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/items/book-1":
			json.NewEncoder(w).Encode(item)
		case r.Method == http.MethodGet && r.URL.Path == "/api/search/chapters":
			json.NewEncoder(w).Encode(audible)
		case r.Method == http.MethodPost && r.URL.Path == "/api/items/book-1/chapters":
			json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "updated": true})
		default:
			http.NotFound(w, r)
		}
	})
}

func audibleFixture() map[string]interface{} {
	return map[string]interface{}{
		"asin":                 "B01A2B3C4D",
		"brandIntroDurationMs": 2000,
		"brandOutroDurationMs": 5000,
		"runtimeLengthMs":      1807000,
		"chapters": []map[string]interface{}{
			{"title": "Prolog", "startOffsetMs": 0, "lengthMs": 600000},
			{"title": "Kapitel 1", "startOffsetMs": 600000, "lengthMs": 600000},
			{"title": "Kapitel 2", "startOffsetMs": 1200000, "lengthMs": 602000},
			{"title": "Outro", "startOffsetMs": 1802000, "lengthMs": 4000},
		},
	}
}

func TestApplyAudibleChaptersFullDryRunThenApply(t *testing.T) {
	item := bookItemJSON("B01A2B3C4D", []map[string]interface{}{
		{"id": 0, "start": 0.0, "end": 600.0, "title": "file 1"},
		{"id": 1, "start": 600.0, "end": 1200.0, "title": "file 2"},
		{"id": 2, "start": 1200.0, "end": 1800.0, "title": "file 3"},
	}, "1.mp3", "2.mp3", "3.mp3")
	recorder := chapterFixtureServer(t, item, audibleFixture())
	defer recorder.Close()

	params := func(extra map[string]interface{}) map[string]interface{} {
		base := map[string]interface{}{"base_url": recorder.URL, "token": "test-token", "item_id": "book-1", "mode": "full", "region": "de", "remove_branding": true}
		for k, v := range extra {
			base[k] = v
		}
		return base
	}

	result := callRegisteredTool(t, newMCPServer(), "apply_audible_chapters", params(nil))
	if result.IsError {
		t.Fatalf("dry run returned error: %v", result)
	}
	for _, request := range recorder.Requests() {
		if request.Method == http.MethodPost {
			t.Fatalf("dry run must not POST, got %s %s", request.Method, request.Path)
		}
	}

	var preview struct {
		Applied  bool             `json:"applied"`
		DryRun   bool             `json:"dry_run"`
		Warnings []string         `json:"warnings"`
		Chapters []plannedChapter `json:"chapters"`
	}
	decodeResultText(t, result, &preview)
	if preview.Applied || !preview.DryRun {
		t.Errorf("expected an unapplied dry run, got %+v", preview)
	}
	if len(preview.Chapters) != 3 {
		t.Fatalf("expected 3 chapters after dropping the outro, got %+v", preview.Chapters)
	}
	if len(preview.Warnings) != 0 {
		t.Errorf("expected no warnings, got %v", preview.Warnings)
	}
	wantTitles := []string{"Prolog", "Kapitel 1", "Kapitel 2"}
	wantStarts := []float64{0, 600, 1200}
	for i, chapter := range preview.Chapters {
		if chapter.Title != wantTitles[i] || !approxEqual(chapter.Start, wantStarts[i]) {
			t.Errorf("chapter %d: expected %q at %v, got %q at %v", i, wantTitles[i], wantStarts[i], chapter.Title, chapter.Start)
		}
	}

	result = callRegisteredTool(t, newMCPServer(), "apply_audible_chapters", params(map[string]interface{}{"dry_run": false}))
	if result.IsError {
		t.Fatalf("apply returned error: %v", result)
	}
	last, _ := recorder.LastRequest()
	if last.Method != http.MethodPost || last.Path != "/api/items/book-1/chapters" {
		t.Fatalf("expected POST /api/items/book-1/chapters, got %s %s", last.Method, last.Path)
	}
	want := map[string]interface{}{
		"chapters": []interface{}{
			map[string]interface{}{"title": "Prolog", "start": 0.0, "end": 600.0},
			map[string]interface{}{"title": "Kapitel 1", "start": 600.0, "end": 1200.0},
			map[string]interface{}{"title": "Kapitel 2", "start": 1200.0, "end": 1800.0},
		},
	}
	if body := decodeBody(t, last.Body); !reflect.DeepEqual(body, want) {
		t.Errorf("unexpected body\n got: %#v\nwant: %#v", body, want)
	}
}

func TestApplyAudibleChaptersTitlesMode(t *testing.T) {
	item := bookItemJSON("B01A2B3C4D", []map[string]interface{}{
		{"id": 0, "start": 0.0, "end": 600.0, "title": "file 1"},
		{"id": 1, "start": 600.0, "end": 1200.0, "title": "file 2"},
		{"id": 2, "start": 1200.0, "end": 1800.0, "title": "file 3"},
	}, "1.mp3", "2.mp3", "3.mp3")
	recorder := chapterFixtureServer(t, item, audibleFixture())
	defer recorder.Close()

	result := callRegisteredTool(t, newMCPServer(), "apply_audible_chapters", map[string]interface{}{
		"base_url": recorder.URL, "token": "test-token", "item_id": "book-1", "dry_run": false, "remove_branding": true,
	})
	if result.IsError {
		t.Fatalf("returned error: %v", result)
	}

	last, _ := recorder.LastRequest()
	want := map[string]interface{}{
		"chapters": []interface{}{
			map[string]interface{}{"title": "Prolog", "start": 0.0, "end": 600.0},
			map[string]interface{}{"title": "Kapitel 1", "start": 600.0, "end": 1200.0},
			map[string]interface{}{"title": "Kapitel 2", "start": 1200.0, "end": 1800.0},
		},
	}
	if body := decodeBody(t, last.Body); !reflect.DeepEqual(body, want) {
		t.Errorf("unexpected body\n got: %#v\nwant: %#v", body, want)
	}
}

func TestApplyAudibleChaptersWarnsOnRuntimeMismatch(t *testing.T) {
	item := bookItemJSON("B01A2B3C4D", nil, "1.mp3")
	recorder := chapterFixtureServer(t, item, audibleFixture())
	defer recorder.Close()

	result := callRegisteredTool(t, newMCPServer(), "apply_audible_chapters", map[string]interface{}{
		"base_url": recorder.URL, "token": "test-token", "item_id": "book-1", "mode": "full",
	})
	if result.IsError {
		t.Fatalf("returned error: %v", result)
	}

	var preview struct {
		Warnings []string `json:"warnings"`
	}
	decodeResultText(t, result, &preview)
	joined := strings.Join(preview.Warnings, "\n")
	if !strings.Contains(joined, "different edition") || !strings.Contains(joined, "dropped") {
		t.Errorf("expected runtime and dropped-chapter warnings, got %v", preview.Warnings)
	}
}

func TestApplyAudibleChaptersErrors(t *testing.T) {
	tests := []struct {
		name    string
		item    map[string]interface{}
		audible map[string]interface{}
		params  map[string]interface{}
	}{
		{"invalid mode", bookItemJSON("B01A2B3C4D", nil, "1.mp3"), audibleFixture(), map[string]interface{}{"mode": "all"}},
		{"negative tolerance", bookItemJSON("B01A2B3C4D", nil, "1.mp3"), audibleFixture(), map[string]interface{}{"mode": "full", "snap_tolerance_sec": -1.0}},
		{"item without asin", bookItemJSON("", nil, "1.mp3"), audibleFixture(), map[string]interface{}{"mode": "full"}},
		{"audible lookup error", bookItemJSON("B01A2B3C4D", nil, "1.mp3"), map[string]interface{}{"error": "Chapters not found"}, map[string]interface{}{"mode": "full"}},
		{"audible without chapters", bookItemJSON("B01A2B3C4D", nil, "1.mp3"), map[string]interface{}{"chapters": []interface{}{}}, map[string]interface{}{"mode": "full"}},
		{"item without audio", bookItemJSON("B01A2B3C4D", nil), audibleFixture(), map[string]interface{}{"mode": "full"}},
		{"titles mode without chapters", bookItemJSON("B01A2B3C4D", nil, "1.mp3"), audibleFixture(), map[string]interface{}{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := chapterFixtureServer(t, tt.item, tt.audible)
			defer recorder.Close()

			params := map[string]interface{}{"base_url": recorder.URL, "token": "test-token", "item_id": "book-1", "dry_run": false}
			for k, v := range tt.params {
				params[k] = v
			}
			if result := callRegisteredTool(t, newMCPServer(), "apply_audible_chapters", params); !result.IsError {
				t.Errorf("expected tool error, got %v", result)
			}
			for _, request := range recorder.Requests() {
				if request.Method == http.MethodPost {
					t.Errorf("unexpected POST %s", request.Path)
				}
			}
		})
	}
}

func TestSearchChaptersTool(t *testing.T) {
	recorder := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"asin": "B01A2B3C4D"})
	})
	defer recorder.Close()

	result := callRegisteredTool(t, newMCPServer(), "search_chapters", map[string]interface{}{
		"base_url": recorder.URL, "token": "test-token", "asin": "B01A2B3C4D", "region": "de",
	})
	if result.IsError {
		t.Fatalf("returned error: %v", result)
	}
	got, _ := recorder.LastRequest()
	if got.Path != "/api/search/chapters" || got.RawQuery != "asin=B01A2B3C4D&region=de" {
		t.Errorf("unexpected request %s?%s", got.Path, got.RawQuery)
	}

	result = callRegisteredTool(t, newMCPServer(), "search_chapters", map[string]interface{}{
		"base_url": recorder.URL, "token": "test-token", "asin": "B01A2B3C4D",
	})
	got, _ = recorder.LastRequest()
	if result.IsError || got.RawQuery != "asin=B01A2B3C4D" {
		t.Errorf("expected no region parameter, got query %q", got.RawQuery)
	}

	if result := callRegisteredTool(t, newMCPServer(), "search_chapters", map[string]interface{}{
		"base_url": recorder.URL, "token": "test-token", "asin": " ",
	}); !result.IsError {
		t.Error("expected an error for a blank asin")
	}
}

func TestUpdateItemChaptersTool(t *testing.T) {
	recorder := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "updated": true})
	})
	defer recorder.Close()

	chapters := []interface{}{
		map[string]interface{}{"title": "Kapitel 1", "start": 0.0, "end": 60.5},
		map[string]interface{}{"title": "Kapitel 2", "start": 60.5, "end": 120.0},
	}
	result := callRegisteredTool(t, newMCPServer(), "update_item_chapters", map[string]interface{}{
		"base_url": recorder.URL, "token": "test-token", "item_id": "book-1", "chapters": chapters,
	})
	if result.IsError {
		t.Fatalf("returned error: %v", result)
	}
	got, _ := recorder.LastRequest()
	if got.Method != http.MethodPost || got.Path != "/api/items/book-1/chapters" {
		t.Errorf("expected POST /api/items/book-1/chapters, got %s %s", got.Method, got.Path)
	}
	if body := decodeBody(t, got.Body); !reflect.DeepEqual(body, map[string]interface{}{"chapters": chapters}) {
		t.Errorf("unexpected body %#v", body)
	}
}

func TestUpdateItemChaptersToolErrors(t *testing.T) {
	recorder := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
	})
	defer recorder.Close()

	chapter := func(title interface{}, start, end interface{}) interface{} {
		return map[string]interface{}{"title": title, "start": start, "end": end}
	}
	tests := []struct {
		name   string
		params map[string]interface{}
	}{
		{"missing chapters", map[string]interface{}{"item_id": "book-1"}},
		{"empty chapters", map[string]interface{}{"item_id": "book-1", "chapters": []interface{}{}}},
		{"missing item_id", map[string]interface{}{"chapters": []interface{}{chapter("a", 0.0, 1.0)}}},
		{"entry not an object", map[string]interface{}{"item_id": "book-1", "chapters": []interface{}{"a"}}},
		{"blank title", map[string]interface{}{"item_id": "book-1", "chapters": []interface{}{chapter(" ", 0.0, 1.0)}}},
		{"start as string", map[string]interface{}{"item_id": "book-1", "chapters": []interface{}{chapter("a", "0", 1.0)}}},
		{"negative start", map[string]interface{}{"item_id": "book-1", "chapters": []interface{}{chapter("a", -1.0, 1.0)}}},
		{"end before start", map[string]interface{}{"item_id": "book-1", "chapters": []interface{}{chapter("a", 5.0, 5.0)}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.params["base_url"] = recorder.URL
			tt.params["token"] = "test-token"
			if result := callRegisteredTool(t, newMCPServer(), "update_item_chapters", tt.params); !result.IsError {
				t.Errorf("expected tool error, got %v", result)
			}
		})
	}
}

func TestShouldRemoveBranding(t *testing.T) {
	trackStarts := []float64{0, 600, 1200, 1800}
	chapters := func(offsetMs int64) audibleChapterData {
		return audibleChapterData{
			BrandIntroDurationMs: 4180,
			BrandOutroDurationMs: 2694,
			Chapters: []audibleChapter{
				{Title: "A", StartOffsetMs: 0, LengthMs: 600000 + offsetMs},
				{Title: "B", StartOffsetMs: 600000 + offsetMs, LengthMs: 600000},
				{Title: "C", StartOffsetMs: 1200000 + offsetMs, LengthMs: 600000},
				{Title: "D", StartOffsetMs: 1800000 + offsetMs, LengthMs: 600000},
			},
		}
	}

	tests := []struct {
		name        string
		data        audibleChapterData
		trackStarts []float64
		want        bool
	}{
		{"audio contains the intro", chapters(0), trackStarts, false},
		{"audio was cut without the intro", chapters(4180), trackStarts, true},
		{"single track gives no signal", chapters(4180), []float64{0}, false},
		{"no tracks gives no signal", chapters(4180), nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := append([]audibleChapter(nil), tt.data.Chapters...)
			if got := shouldRemoveBranding(tt.data, tt.trackStarts); got != tt.want {
				t.Errorf("shouldRemoveBranding = %v, want %v", got, tt.want)
			}
			if !reflect.DeepEqual(tt.data.Chapters, original) {
				t.Error("shouldRemoveBranding must not modify the chapters it is given")
			}
		})
	}
}

func TestApplyAudibleChaptersDetectsBranding(t *testing.T) {
	audibleWithOffset := func(offsetMs int) map[string]interface{} {
		return map[string]interface{}{
			"asin":                 "B01A2B3C4D",
			"brandIntroDurationMs": 4180,
			"brandOutroDurationMs": 2694,
			"runtimeLengthMs":      1800000 + offsetMs,
			"chapters": []map[string]interface{}{
				{"title": "A", "startOffsetMs": 0, "lengthMs": 600000 + offsetMs},
				{"title": "B", "startOffsetMs": 600000 + offsetMs, "lengthMs": 600000},
				{"title": "C", "startOffsetMs": 1200000 + offsetMs, "lengthMs": 600000},
			},
		}
	}

	tests := []struct {
		name        string
		audible     map[string]interface{}
		params      map[string]interface{}
		wantRemoved bool
		wantDetect  bool
	}{
		{"audio contains the intro", audibleWithOffset(0), map[string]interface{}{}, false, true},
		{"audio was cut without the intro", audibleWithOffset(4180), map[string]interface{}{}, true, true},
		{"explicit false wins", audibleWithOffset(4180), map[string]interface{}{"remove_branding": false}, false, false},
		{"explicit true wins", audibleWithOffset(0), map[string]interface{}{"remove_branding": true}, true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item := bookItemJSON("B01A2B3C4D", nil, "1.mp3", "2.mp3", "3.mp3")
			recorder := chapterFixtureServer(t, item, tt.audible)
			defer recorder.Close()

			params := map[string]interface{}{"base_url": recorder.URL, "token": "test-token", "item_id": "book-1", "mode": "full"}
			for k, v := range tt.params {
				params[k] = v
			}
			result := callRegisteredTool(t, newMCPServer(), "apply_audible_chapters", params)
			if result.IsError {
				t.Fatalf("returned error: %v", result)
			}

			var preview struct {
				Removed  bool             `json:"branding_removed"`
				Detected bool             `json:"branding_detected"`
				Chapters []plannedChapter `json:"chapters"`
			}
			decodeResultText(t, result, &preview)
			if preview.Removed != tt.wantRemoved || preview.Detected != tt.wantDetect {
				t.Errorf("expected removed=%v detected=%v, got removed=%v detected=%v", tt.wantRemoved, tt.wantDetect, preview.Removed, preview.Detected)
			}
			if len(preview.Chapters) != 3 {
				t.Fatalf("expected 3 chapters, got %+v", preview.Chapters)
			}
			if tt.wantDetect {
				for i, want := range []float64{0, 600, 1200} {
					if !approxEqual(preview.Chapters[i].Start, want) || preview.Chapters[i].SnappedFrom != nil {
						t.Errorf("chapter %d should start exactly at %v without snapping, got %+v", i, want, preview.Chapters[i])
					}
				}
			}
		})
	}
}

func TestApplyAudibleChaptersRejectsNonBooleanBranding(t *testing.T) {
	item := bookItemJSON("B01A2B3C4D", nil, "1.mp3")
	recorder := chapterFixtureServer(t, item, audibleFixture())
	defer recorder.Close()

	result := callRegisteredTool(t, newMCPServer(), "apply_audible_chapters", map[string]interface{}{
		"base_url": recorder.URL, "token": "test-token", "item_id": "book-1", "remove_branding": "yes",
	})
	if !result.IsError {
		t.Errorf("expected tool error, got %v", result)
	}
}

func TestBuildFullChaptersFollowsConstantOffset(t *testing.T) {
	// The recording has a 12s longer lead-in than Audible, so every Audible
	// time is 12s too early, including the chapters inside the files.
	audible := []audibleChapter{
		{Title: "A", StartOffsetMs: 0},
		{Title: "B", StartOffsetMs: 288000},
		{Title: "C", StartOffsetMs: 588000},
		{Title: "D", StartOffsetMs: 888000},
		{Title: "E", StartOffsetMs: 1188000},
	}

	chapters, warnings := buildFullChapters(audible, 1800, []float64{0, 600, 1200}, 15)

	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings %v", warnings)
	}
	wantStarts := []float64{0, 300, 600, 900, 1200}
	for i, want := range wantStarts {
		if !approxEqual(chapters[i].Start, want) {
			t.Errorf("chapter %d should start at %v, got %v", i, want, chapters[i].Start)
		}
	}
	if chapters[1].SnappedFrom != nil || chapters[1].ShiftedFrom == nil || !approxEqual(*chapters[1].ShiftedFrom, 288) {
		t.Errorf("chapter B should be marked as shifted from 288, got %+v", chapters[1])
	}
	if chapters[2].SnappedFrom == nil || chapters[2].ShiftedFrom != nil {
		t.Errorf("chapter C should be marked as snapped only, got %+v", chapters[2])
	}
}

func TestApplyNeighbourShiftKeepsChapterOrder(t *testing.T) {
	from := func(v float64) *float64 { return &v }
	chapters := []plannedChapter{
		{Title: "A", Start: 0},
		{Title: "B", Start: 100, SnappedFrom: from(90)}, // shifted by +10
		{Title: "C", Start: 105},                        // +10 would reach 115, past D
		{Title: "D", Start: 110},
	}

	applyNeighbourShift(chapters)

	if !approxEqual(chapters[2].Start, 105) || chapters[2].ShiftedFrom != nil {
		t.Errorf("a shift that would break the order must be skipped, got %+v", chapters[2])
	}
	if !approxEqual(chapters[3].Start, 120) || chapters[3].ShiftedFrom == nil {
		t.Errorf("D still follows B, got %+v", chapters[3])
	}

	none := []plannedChapter{{Title: "A", Start: 0}, {Title: "B", Start: 50}}
	applyNeighbourShift(none)
	if !approxEqual(none[1].Start, 50) {
		t.Errorf("without snapped chapters nothing moves, got %+v", none[1])
	}
}

// stepTracks returns n tracks of 100s each as boundaries, and their total.
func stepTracks(n int) ([]float64, float64) {
	starts := make([]float64, n)
	for i := range starts {
		starts[i] = float64(i) * 100
	}
	return starts, float64(n) * 100
}

func TestEstimateChapterOffsetsFollowsSteps(t *testing.T) {
	trackStarts, _ := stepTracks(30)
	// Chapters 0-9 sit on boundaries 0..900 with no offset; from chapter 10 on
	// the recording has 28s more than Audible before each boundary, from 20 on
	// 40s more. Offsets must stay below half the track length (50s here). Chapter 15 starts 40s into a file.
	var starts []float64
	for i := 0; i < 30; i++ {
		offset := 0.0
		switch {
		case i >= 20:
			offset = 40
		case i >= 10:
			offset = 28
		}
		starts = append(starts, trackStarts[i]-offset)
	}
	starts[15] = trackStarts[15] + 40 - 28

	offsets := estimateChapterOffsets(starts, trackStarts)
	if offsets == nil {
		t.Fatal("expected offsets to be estimated")
	}
	for i, want := range map[int]float64{2: 0, 8: 0, 12: 28, 15: 28, 18: 28, 24: 40, 29: 40} {
		if !approxEqual(offsets[i], want) {
			t.Errorf("chapter %d: expected offset %v, got %v", i, want, offsets[i])
		}
	}
}

func TestEstimateChapterOffsetsRejectsRandomDistances(t *testing.T) {
	trackStarts, _ := stepTracks(30)
	// Starts that have no relation to the boundaries
	var starts []float64
	for i := 0; i < 30; i++ {
		starts = append(starts, float64(i)*100+float64((i*37)%90)+5)
	}
	if offsets := estimateChapterOffsets(starts, trackStarts); offsets != nil {
		t.Errorf("expected no estimate for unrelated chapters, got %v", offsets)
	}
	if offsets := estimateChapterOffsets(starts[:5], trackStarts); offsets != nil {
		t.Error("expected no estimate for too few chapters")
	}
	if offsets := estimateChapterOffsets(starts, trackStarts[:2]); offsets != nil {
		t.Error("expected no estimate for too few tracks")
	}
}

func TestBuildFullChaptersCorrectsSteppedOffsets(t *testing.T) {
	trackStarts, total := stepTracks(30)
	var audible []audibleChapter
	for i := 0; i < 30; i++ {
		offset := 0.0
		if i >= 15 {
			offset = 28
		}
		audible = append(audible, audibleChapter{Title: "K" + string(rune('A'+i%26)), StartOffsetMs: int64((trackStarts[i] - offset) * 1000)})
	}
	// A chapter in the middle of the second plateau, 40s into the file
	audible[20].StartOffsetMs = int64((trackStarts[20] + 40 - 28) * 1000)

	chapters, warnings := buildFullChapters(audible, total, trackStarts, 15)

	if len(chapters) != 30 {
		t.Fatalf("expected 30 chapters, got %d (%v)", len(chapters), warnings)
	}
	for i, chapter := range chapters {
		want := trackStarts[i]
		if i == 20 {
			want += 40
		}
		if !approxEqual(chapter.Start, want) {
			t.Errorf("chapter %d should start at %v, got %v", i, want, chapter.Start)
		}
	}
	if chapters[10].AudibleStart != nil || chapters[16].AudibleStart == nil || !approxEqual(*chapters[16].AudibleStart, trackStarts[16]-28) {
		t.Errorf("only moved chapters report audible_start, got %+v and %+v", chapters[10], chapters[16])
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "corrected by up to 28s") {
		t.Errorf("expected one note about the correction, got %v", warnings)
	}
}

func trackFiles(durations ...float64) []absAudioFile {
	files := make([]absAudioFile, len(durations))
	for i, duration := range durations {
		files[i] = absAudioFile{Index: i + 1, Ino: string(rune('a' + i)), Duration: duration}
		files[i].Metadata.Filename = string(rune('a'+i)) + ".mp3"
	}
	return files
}

func TestBuildTrackChapters(t *testing.T) {
	audible := []audibleChapter{
		{Title: "Kapitel 1", StartOffsetMs: 0},
		{Title: "Kapitel 2", StartOffsetMs: 100000},
		{Title: "Kapitel 3", StartOffsetMs: 300000},
	}

	chapters, warnings, err := buildTrackChapters(audible, trackFiles(100, 200, 50))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []plannedChapter{
		{Index: 0, Title: "Kapitel 1", Start: 0, End: 100},
		{Index: 1, Title: "Kapitel 2", Start: 100, End: 300},
		{Index: 2, Title: "Kapitel 3", Start: 300, End: 350},
	}
	if !reflect.DeepEqual(chapters, want) || len(warnings) != 0 {
		t.Errorf("unexpected result\n got: %+v %v\nwant: %+v", chapters, warnings, want)
	}
}

func TestBuildTrackChaptersUsesPlaybackOrderAndSkipsExcluded(t *testing.T) {
	files := trackFiles(10, 20, 30)
	files[0].Index, files[1].Index, files[2].Index = 3, 1, 2
	files[2].Exclude = true
	files[2].Index = -1
	audible := []audibleChapter{{Title: "A", StartOffsetMs: 0}, {Title: "B", StartOffsetMs: 20000}}

	chapters, _, err := buildTrackChapters(audible, files)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chapters) != 2 || chapters[0].End != 20 || chapters[1].End != 30 {
		t.Errorf("expected the 20s track first, then the 10s track, got %+v", chapters)
	}
}

func TestBuildTrackChaptersErrorsAndWarnings(t *testing.T) {
	audible := []audibleChapter{{Title: "A", StartOffsetMs: 0}, {Title: "B", StartOffsetMs: 100000}}
	if _, _, err := buildTrackChapters(audible, trackFiles(100, 100, 100)); err == nil || !strings.Contains(err.Error(), "3 tracks but Audible lists 2") {
		t.Errorf("expected a count error, got %v", err)
	}

	// The first track is 60s longer than the first Audible chapter
	_, warnings, err := buildTrackChapters(audible, trackFiles(160, 100))
	if err != nil || len(warnings) != 1 || !strings.Contains(warnings[0], "60s longer") {
		t.Errorf("expected a warning about the longer track, got %v (%v)", warnings, err)
	}
}

func TestApplyAudibleChaptersTracksMode(t *testing.T) {
	item := bookItemJSON("B01A2B3C4D", []map[string]interface{}{
		{"id": 0, "start": 0.0, "end": 1800.0, "title": "old"},
	}, "1.mp3", "2.mp3", "3.mp3")
	recorder := chapterFixtureServer(t, item, map[string]interface{}{
		"asin": "B01A2B3C4D", "runtimeLengthMs": 1800000,
		"chapters": []map[string]interface{}{
			{"title": "Eins", "startOffsetMs": 0, "lengthMs": 600000},
			{"title": "Zwei", "startOffsetMs": 600000, "lengthMs": 600000},
			{"title": "Drei", "startOffsetMs": 1200000, "lengthMs": 600000},
		},
	})
	defer recorder.Close()

	result := callRegisteredTool(t, newMCPServer(), "apply_audible_chapters", map[string]interface{}{
		"base_url": recorder.URL, "token": "test-token", "item_id": "book-1", "mode": "tracks", "dry_run": false,
	})
	if result.IsError {
		t.Fatalf("returned error: %v", result)
	}
	last, _ := recorder.LastRequest()
	want := map[string]interface{}{"chapters": []interface{}{
		map[string]interface{}{"title": "Eins", "start": 0.0, "end": 600.0},
		map[string]interface{}{"title": "Zwei", "start": 600.0, "end": 1200.0},
		map[string]interface{}{"title": "Drei", "start": 1200.0, "end": 1800.0},
	}}
	if body := decodeBody(t, last.Body); !reflect.DeepEqual(body, want) {
		t.Errorf("unexpected body\n got: %#v\nwant: %#v", body, want)
	}
}

func TestApplyAudibleChaptersStretchToAudio(t *testing.T) {
	// Audible runs 1% longer than the recording and the chapters do not line up
	item := bookItemJSON("B01A2B3C4D", nil, "1.mp3", "2.mp3", "3.mp3") // 1800s
	recorder := chapterFixtureServer(t, item, map[string]interface{}{
		"asin": "B01A2B3C4D", "runtimeLengthMs": 1818000,
		"chapters": []map[string]interface{}{
			{"title": "A", "startOffsetMs": 0, "lengthMs": 606000},
			{"title": "B", "startOffsetMs": 606000, "lengthMs": 606000},
			{"title": "C", "startOffsetMs": 1212000, "lengthMs": 606000},
		},
	})
	defer recorder.Close()

	params := map[string]interface{}{"base_url": recorder.URL, "token": "test-token", "item_id": "book-1", "mode": "full", "remove_branding": false, "snap_tolerance_sec": 0.0}
	params["stretch_to_audio"] = true
	result := callRegisteredTool(t, newMCPServer(), "apply_audible_chapters", params)
	if result.IsError {
		t.Fatalf("returned error: %v", result)
	}
	var preview struct {
		Factor   float64          `json:"stretch_factor"`
		Chapters []plannedChapter `json:"chapters"`
	}
	decodeResultText(t, result, &preview)
	if math.Abs(preview.Factor-1800.0/1818.0) > 1e-9 {
		t.Errorf("unexpected stretch factor %v", preview.Factor)
	}
	for i, want := range []float64{0, 600, 1200} {
		if math.Abs(preview.Chapters[i].Start-want) > 0.01 {
			t.Errorf("chapter %d should start at %v, got %v", i, want, preview.Chapters[i].Start)
		}
	}

	params["mode"] = "titles"
	if result := callRegisteredTool(t, newMCPServer(), "apply_audible_chapters", params); !result.IsError {
		t.Error("stretch_to_audio must be refused outside of full mode")
	}
}
