package main

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func intPtr(v int) *int { return &v }

func trackFile(index int, name string) absAudioFile {
	file := absAudioFile{Index: index, Ino: "ino-" + name, Duration: 60}
	file.Metadata.Filename = name
	return file
}

func trackNames(planned []plannedTrack) []string {
	names := make([]string, 0, len(planned))
	for _, track := range planned {
		name := track.Filename
		if track.Excluded {
			name += "!"
		}
		names = append(names, name)
	}
	return names
}

func TestNaturalCompare(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"Track 2.mp3", "Track 10.mp3", -1},
		{"Track 10.mp3", "Track 2.mp3", 1},
		{"track 02.mp3", "Track 2.mp3", 0},
		{"Kapitel 001.mp3", "Kapitel 002.mp3", -1},
		{"a.mp3", "b.mp3", -1},
		{"A.mp3", "a.mp3", 0},
		{"Track 1", "Track 1 b", -1},
		{"Teil 1 - 9.mp3", "Teil 1 - 10.mp3", -1},
		{"Teil 2 - 1.mp3", "Teil 10 - 1.mp3", -1},
		{"", "a", -1},
		{"", "", 0},
	}

	for _, tt := range tests {
		if got := naturalCompare(tt.a, tt.b); got != tt.want {
			t.Errorf("naturalCompare(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestPlanTrackOrderSortByFilename(t *testing.T) {
	files := []absAudioFile{trackFile(1, "10.mp3"), trackFile(2, "2.mp3"), trackFile(3, "1.mp3")}

	planned, err := planTrackOrder(files, trackOrderOptions{SortBy: "filename"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got, want := trackNames(planned), []string{"1.mp3", "2.mp3", "10.mp3"}; !reflect.DeepEqual(got, want) {
		t.Errorf("expected %v, got %v", want, got)
	}
	if planned[0].Position != 1 || planned[0].OldPosition != 3 {
		t.Errorf("expected 1.mp3 to move from 3 to 1, got %+v", planned[0])
	}
}

func TestPlanTrackOrderSortByTags(t *testing.T) {
	a, b, c, d := trackFile(1, "a.mp3"), trackFile(2, "b.mp3"), trackFile(3, "c.mp3"), trackFile(4, "d.mp3")
	a.DiscNumFromMeta, a.TrackNumFromMeta = intPtr(2), intPtr(1)
	b.DiscNumFromMeta, b.TrackNumFromMeta = intPtr(1), intPtr(2)
	c.DiscNumFromMeta, c.TrackNumFromMeta = intPtr(1), intPtr(1)
	// d has no tags and must sort last

	planned, err := planTrackOrder([]absAudioFile{a, b, c, d}, trackOrderOptions{SortBy: "track_tag"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := trackNames(planned), []string{"c.mp3", "b.mp3", "a.mp3", "d.mp3"}; !reflect.DeepEqual(got, want) {
		t.Errorf("expected %v, got %v", want, got)
	}
}

func TestPlanTrackOrderSortByFilenameNumber(t *testing.T) {
	a, b := trackFile(1, "a.mp3"), trackFile(2, "b.mp3")
	a.TrackNumFromFilename = intPtr(2)
	b.TrackNumFromFilename = intPtr(1)

	planned, err := planTrackOrder([]absAudioFile{a, b}, trackOrderOptions{SortBy: "filename_number"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := trackNames(planned), []string{"b.mp3", "a.mp3"}; !reflect.DeepEqual(got, want) {
		t.Errorf("expected %v, got %v", want, got)
	}
}

func TestPlanTrackOrderExplicitOrderAndReverse(t *testing.T) {
	files := []absAudioFile{trackFile(1, "a.mp3"), trackFile(2, "b.mp3"), trackFile(3, "c.mp3"), trackFile(4, "d.mp3")}

	planned, err := planTrackOrder(files, trackOrderOptions{Order: []string{"d.mp3", "ino-b.mp3"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := trackNames(planned), []string{"d.mp3", "b.mp3", "a.mp3", "c.mp3"}; !reflect.DeepEqual(got, want) {
		t.Errorf("order: expected %v, got %v", want, got)
	}

	planned, err = planTrackOrder(files, trackOrderOptions{Reverse: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := trackNames(planned), []string{"d.mp3", "c.mp3", "b.mp3", "a.mp3"}; !reflect.DeepEqual(got, want) {
		t.Errorf("reverse: expected %v, got %v", want, got)
	}
}

func TestPlanTrackOrderExcludeAndInclude(t *testing.T) {
	cover := trackFile(-1, "bonus.mp3")
	cover.Exclude = true
	files := []absAudioFile{trackFile(1, "a.mp3"), trackFile(2, "b.mp3"), cover}

	planned, err := planTrackOrder(files, trackOrderOptions{Exclude: []string{"a.mp3"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := trackNames(planned), []string{"b.mp3", "a.mp3!", "bonus.mp3!"}; !reflect.DeepEqual(got, want) {
		t.Errorf("exclude: expected %v, got %v", want, got)
	}
	if planned[2].OldPosition != 0 || planned[2].Position != 0 {
		t.Errorf("excluded tracks have position 0, got %+v", planned[2])
	}

	planned, err = planTrackOrder(files, trackOrderOptions{Include: []string{"bonus.mp3"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := trackNames(planned), []string{"a.mp3", "b.mp3", "bonus.mp3"}; !reflect.DeepEqual(got, want) {
		t.Errorf("include: expected %v, got %v", want, got)
	}
}

func TestPlanTrackOrderErrors(t *testing.T) {
	cover := trackFile(-1, "bonus.mp3")
	cover.Exclude = true
	files := []absAudioFile{trackFile(1, "a.mp3"), trackFile(2, "b.mp3"), cover}

	tests := []struct {
		name string
		opts trackOrderOptions
	}{
		{"unknown sort_by", trackOrderOptions{SortBy: "size"}},
		{"unknown order entry", trackOrderOptions{Order: []string{"x.mp3"}}},
		{"order lists an excluded file", trackOrderOptions{Order: []string{"bonus.mp3"}}},
		{"order lists a file twice", trackOrderOptions{Order: []string{"a.mp3", "ino-a.mp3"}}},
		{"unknown exclude entry", trackOrderOptions{Exclude: []string{"x.mp3"}}},
		{"unknown include entry", trackOrderOptions{Include: []string{"x.mp3"}}},
		{"order lists a file that is also excluded", trackOrderOptions{Order: []string{"a.mp3"}, Exclude: []string{"a.mp3"}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if planned, err := planTrackOrder(files, tt.opts); err == nil {
				t.Errorf("expected an error, got %v", trackNames(planned))
			}
		})
	}

	duplicates := []absAudioFile{trackFile(1, "a.mp3"), trackFile(2, "a.mp3")}
	duplicates[1].Ino = "other"
	if _, err := planTrackOrder(duplicates, trackOrderOptions{Order: []string{"a.mp3"}}); err == nil || !strings.Contains(err.Error(), "more than one") {
		t.Errorf("expected an ambiguity error, got %v", err)
	}
}

func trackItemJSON(files ...map[string]interface{}) map[string]interface{} {
	items := make([]map[string]interface{}, 0, len(files))
	items = append(items, files...)
	return map[string]interface{}{
		"media": map[string]interface{}{
			"metadata":   map[string]interface{}{"title": "Das Paket"},
			"audioFiles": items,
			"chapters":   []interface{}{},
		},
	}
}

func trackJSON(index int, name string, exclude bool) map[string]interface{} {
	return map[string]interface{}{
		"index": index, "ino": "ino-" + name, "duration": 60.0, "exclude": exclude,
		"metadata": map[string]interface{}{"filename": name},
	}
}

func trackFixtureServer(t *testing.T, item map[string]interface{}) *recordingServer {
	t.Helper()
	return newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/items/book-1":
			json.NewEncoder(w).Encode(item)
		case r.Method == http.MethodPatch && r.URL.Path == "/api/items/book-1/tracks":
			json.NewEncoder(w).Encode(map[string]interface{}{"id": "book-1"})
		default:
			http.NotFound(w, r)
		}
	})
}

func patchRequests(recorder *recordingServer) []recordedRequest {
	var patches []recordedRequest
	for _, request := range recorder.Requests() {
		if request.Method == http.MethodPatch {
			patches = append(patches, request)
		}
	}
	return patches
}

func TestReorderTracksTool(t *testing.T) {
	item := trackItemJSON(trackJSON(1, "10.mp3", false), trackJSON(2, "2.mp3", false), trackJSON(-1, "bonus.mp3", true))
	recorder := trackFixtureServer(t, item)
	defer recorder.Close()

	params := func(extra map[string]interface{}) map[string]interface{} {
		base := map[string]interface{}{"base_url": recorder.URL, "token": "test-token", "item_id": "book-1", "sort_by": "filename"}
		for k, v := range extra {
			base[k] = v
		}
		return base
	}

	result := callRegisteredTool(t, newMCPServer(), "reorder_tracks", params(nil))
	if result.IsError {
		t.Fatalf("dry run returned error: %v", result)
	}
	if len(patchRequests(recorder)) != 0 {
		t.Fatal("dry run must not PATCH")
	}
	var preview struct {
		Changed bool           `json:"changed"`
		Applied bool           `json:"applied"`
		DryRun  bool           `json:"dry_run"`
		Tracks  []plannedTrack `json:"tracks"`
	}
	decodeResultText(t, result, &preview)
	if !preview.Changed || preview.Applied || !preview.DryRun {
		t.Errorf("expected a changed, unapplied dry run, got %+v", preview)
	}
	if got, want := trackNames(preview.Tracks), []string{"2.mp3", "10.mp3", "bonus.mp3!"}; !reflect.DeepEqual(got, want) {
		t.Errorf("expected %v, got %v", want, got)
	}

	result = callRegisteredTool(t, newMCPServer(), "reorder_tracks", params(map[string]interface{}{"dry_run": false}))
	if result.IsError {
		t.Fatalf("apply returned error: %v", result)
	}
	patches := patchRequests(recorder)
	if len(patches) != 1 {
		t.Fatalf("expected one PATCH, got %d", len(patches))
	}
	want := map[string]interface{}{
		"orderedFileData": []interface{}{
			map[string]interface{}{"ino": "ino-2.mp3", "exclude": false},
			map[string]interface{}{"ino": "ino-10.mp3", "exclude": false},
			map[string]interface{}{"ino": "ino-bonus.mp3", "exclude": true},
		},
	}
	if body := decodeBody(t, patches[0].Body); !reflect.DeepEqual(body, want) {
		t.Errorf("unexpected body\n got: %#v\nwant: %#v", body, want)
	}
}

func TestReorderTracksToolSkipsPatchWithoutChanges(t *testing.T) {
	item := trackItemJSON(trackJSON(1, "1.mp3", false), trackJSON(2, "2.mp3", false))
	recorder := trackFixtureServer(t, item)
	defer recorder.Close()

	result := callRegisteredTool(t, newMCPServer(), "reorder_tracks", map[string]interface{}{
		"base_url": recorder.URL, "token": "test-token", "item_id": "book-1", "sort_by": "filename", "dry_run": false,
	})
	if result.IsError {
		t.Fatalf("returned error: %v", result)
	}
	if len(patchRequests(recorder)) != 0 {
		t.Error("an unchanged order must not be sent")
	}
	var outcome struct {
		Changed bool `json:"changed"`
		Applied bool `json:"applied"`
	}
	decodeResultText(t, result, &outcome)
	if outcome.Changed || outcome.Applied {
		t.Errorf("expected unchanged and unapplied, got %+v", outcome)
	}
}

func TestReorderTracksToolErrors(t *testing.T) {
	item := trackItemJSON(trackJSON(1, "1.mp3", false))
	empty := trackItemJSON()

	tests := []struct {
		name   string
		item   map[string]interface{}
		params map[string]interface{}
	}{
		{"no options", item, map[string]interface{}{}},
		{"invalid sort_by", item, map[string]interface{}{"sort_by": "size"}},
		{"unknown track", item, map[string]interface{}{"order": []interface{}{"x.mp3"}}},
		{"item without audio", empty, map[string]interface{}{"sort_by": "filename"}},
		{"missing item_id", item, map[string]interface{}{"item_id": "", "sort_by": "filename"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := trackFixtureServer(t, tt.item)
			defer recorder.Close()

			params := map[string]interface{}{"base_url": recorder.URL, "token": "test-token", "item_id": "book-1", "dry_run": false}
			for k, v := range tt.params {
				params[k] = v
			}
			if result := callRegisteredTool(t, newMCPServer(), "reorder_tracks", params); !result.IsError {
				t.Errorf("expected tool error, got %v", result)
			}
			if len(patchRequests(recorder)) != 0 {
				t.Error("unexpected PATCH")
			}
		})
	}
}
