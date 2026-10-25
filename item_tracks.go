package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"unicode"

	"github.com/mark3labs/mcp-go/mcp"
)

type plannedTrack struct {
	Position    int     `json:"position"`
	OldPosition int     `json:"old_position"`
	Filename    string  `json:"filename"`
	Ino         string  `json:"ino"`
	DurationSec float64 `json:"duration_sec"`
	Excluded    bool    `json:"excluded"`
}

// naturalCompare orders strings case-insensitively and compares runs of digits
// by value, so "Track 2" sorts before "Track 10".
func naturalCompare(a, b string) int {
	ar, br := []rune(strings.ToLower(a)), []rune(strings.ToLower(b))
	i, j := 0, 0
	for i < len(ar) && j < len(br) {
		if unicode.IsDigit(ar[i]) && unicode.IsDigit(br[j]) {
			si := i
			for i < len(ar) && unicode.IsDigit(ar[i]) {
				i++
			}
			sj := j
			for j < len(br) && unicode.IsDigit(br[j]) {
				j++
			}
			an := strings.TrimLeft(string(ar[si:i]), "0")
			bn := strings.TrimLeft(string(br[sj:j]), "0")
			if len(an) != len(bn) {
				if len(an) < len(bn) {
					return -1
				}
				return 1
			}
			if an != bn {
				if an < bn {
					return -1
				}
				return 1
			}
			continue
		}
		if ar[i] != br[j] {
			if ar[i] < br[j] {
				return -1
			}
			return 1
		}
		i++
		j++
	}
	switch {
	case len(ar)-i < len(br)-j:
		return -1
	case len(ar)-i > len(br)-j:
		return 1
	}
	return 0
}

func compareOptionalInt(a, b *int) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return 1
	case b == nil:
		return -1
	case *a < *b:
		return -1
	case *a > *b:
		return 1
	}
	return 0
}

// trackSortCompare returns the comparison function for a sort_by value.
// Files without the requested numbers sort last, ordered by file name.
func trackSortCompare(sortBy string) (func(a, b absAudioFile) int, error) {
	byName := func(a, b absAudioFile) int { return naturalCompare(a.Metadata.Filename, b.Metadata.Filename) }
	switch sortBy {
	case "filename":
		return byName, nil
	case "track_tag":
		return func(a, b absAudioFile) int {
			if c := compareOptionalInt(a.DiscNumFromMeta, b.DiscNumFromMeta); c != 0 {
				return c
			}
			if c := compareOptionalInt(a.TrackNumFromMeta, b.TrackNumFromMeta); c != 0 {
				return c
			}
			return byName(a, b)
		}, nil
	case "filename_number":
		return func(a, b absAudioFile) int {
			if c := compareOptionalInt(a.DiscNumFromFilename, b.DiscNumFromFilename); c != 0 {
				return c
			}
			if c := compareOptionalInt(a.TrackNumFromFilename, b.TrackNumFromFilename); c != 0 {
				return c
			}
			return byName(a, b)
		}, nil
	}
	return nil, fmt.Errorf(`sort_by must be "filename", "track_tag" or "filename_number"`)
}

type trackOrderOptions struct {
	SortBy  string
	Reverse bool
	Order   []string
	Exclude []string
	Include []string
}

// resolveTrack finds a file by inode or exact file name.
func resolveTrack(files []absAudioFile, ref string) (int, error) {
	found := -1
	for i, file := range files {
		if file.Ino == ref || file.Metadata.Filename == ref {
			if found >= 0 {
				return 0, fmt.Errorf("%q matches more than one track", ref)
			}
			found = i
		}
	}
	if found < 0 {
		return 0, fmt.Errorf("no track matches %q", ref)
	}
	return found, nil
}

// planTrackOrder computes the new track order. It starts from the current
// playback order, optionally sorts it, then moves the files named in Order to
// the front. Previously excluded files stay excluded unless listed in Include.
func planTrackOrder(files []absAudioFile, opts trackOrderOptions) ([]plannedTrack, error) {
	current := append([]absAudioFile(nil), files...)
	sort.SliceStable(current, func(i, j int) bool {
		if current[i].Exclude != current[j].Exclude {
			return !current[i].Exclude
		}
		return current[i].Index < current[j].Index
	})

	oldPosition := make(map[string]int, len(current))
	position := 0
	for _, file := range current {
		if !file.Exclude {
			position++
			oldPosition[file.Ino] = position
		} else {
			oldPosition[file.Ino] = 0
		}
	}

	excluded := make(map[string]bool)
	for _, file := range current {
		if file.Exclude {
			excluded[file.Ino] = true
		}
	}
	for _, ref := range opts.Include {
		i, err := resolveTrack(current, ref)
		if err != nil {
			return nil, fmt.Errorf("include: %w", err)
		}
		delete(excluded, current[i].Ino)
	}
	for _, ref := range opts.Exclude {
		i, err := resolveTrack(current, ref)
		if err != nil {
			return nil, fmt.Errorf("exclude: %w", err)
		}
		excluded[current[i].Ino] = true
	}

	var active, skipped []absAudioFile
	for _, file := range current {
		if excluded[file.Ino] {
			skipped = append(skipped, file)
		} else {
			active = append(active, file)
		}
	}

	if opts.SortBy != "" {
		compare, err := trackSortCompare(opts.SortBy)
		if err != nil {
			return nil, err
		}
		sort.SliceStable(active, func(i, j int) bool { return compare(active[i], active[j]) < 0 })
	}
	if opts.Reverse {
		for i, j := 0, len(active)-1; i < j; i, j = i+1, j-1 {
			active[i], active[j] = active[j], active[i]
		}
	}

	if len(opts.Order) > 0 {
		front := make([]absAudioFile, 0, len(opts.Order))
		listed := make(map[string]bool, len(opts.Order))
		for _, ref := range opts.Order {
			i, err := resolveTrack(current, ref)
			if err != nil {
				return nil, fmt.Errorf("order: %w", err)
			}
			file := current[i]
			if excluded[file.Ino] {
				return nil, fmt.Errorf("order: %q is excluded", ref)
			}
			if listed[file.Ino] {
				return nil, fmt.Errorf("order: %q is listed twice", ref)
			}
			listed[file.Ino] = true
			front = append(front, file)
		}
		for _, file := range active {
			if !listed[file.Ino] {
				front = append(front, file)
			}
		}
		active = front
	}

	planned := make([]plannedTrack, 0, len(current))
	for i, file := range active {
		planned = append(planned, plannedTrack{
			Position:    i + 1,
			OldPosition: oldPosition[file.Ino],
			Filename:    file.Metadata.Filename,
			Ino:         file.Ino,
			DurationSec: file.Duration,
		})
	}
	for _, file := range skipped {
		planned = append(planned, plannedTrack{
			OldPosition: oldPosition[file.Ino],
			Filename:    file.Metadata.Filename,
			Ino:         file.Ino,
			DurationSec: file.Duration,
			Excluded:    true,
		})
	}
	return planned, nil
}

func trackOrderChanged(planned []plannedTrack, files []absAudioFile) bool {
	wasExcluded := make(map[string]bool, len(files))
	for _, file := range files {
		wasExcluded[file.Ino] = file.Exclude
	}
	for _, track := range planned {
		if track.Excluded != wasExcluded[track.Ino] || track.Position != track.OldPosition {
			return true
		}
	}
	return false
}

func handleReorderTracks(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	baseURL, token, err := getABSConfig(request)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	itemID, err := requireNonBlankString(request, "item_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	opts := trackOrderOptions{
		SortBy:  request.GetString("sort_by", ""),
		Reverse: request.GetBool("reverse", false),
		Order:   request.GetStringSlice("order", nil),
		Exclude: request.GetStringSlice("exclude", nil),
		Include: request.GetStringSlice("include", nil),
	}
	if opts.SortBy == "" && !opts.Reverse && len(opts.Order) == 0 && len(opts.Exclude) == 0 && len(opts.Include) == 0 {
		return mcp.NewToolResultError("pass at least one of sort_by, reverse, order, exclude or include"), nil
	}
	dryRun := request.GetBool("dry_run", true)

	book, err := fetchBookItem(ctx, baseURL, token, itemID)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if len(book.Media.AudioFiles) == 0 {
		return mcp.NewToolResultError("the item has no audio files"), nil
	}

	planned, err := planTrackOrder(book.Media.AudioFiles, opts)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	changed := trackOrderChanged(planned, book.Media.AudioFiles)

	result := map[string]interface{}{
		"item_id":  itemID,
		"title":    book.Media.Metadata.Title,
		"dry_run":  dryRun,
		"changed":  changed,
		"applied":  false,
		"tracks":   planned,
		"warnings": []string{"chapters are not changed, regenerate or update them afterwards if they follow the files"},
	}

	if changed && !dryRun {
		orderedFileData := make([]map[string]interface{}, 0, len(planned))
		for _, track := range planned {
			orderedFileData = append(orderedFileData, map[string]interface{}{"ino": track.Ino, "exclude": track.Excluded})
		}
		if _, err := absPATCH(ctx, baseURL, token, fmt.Sprintf("/items/%s/tracks", url.PathEscape(itemID)), map[string]interface{}{"orderedFileData": orderedFileData}); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		result["applied"] = true
	}

	output, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("encode result: %s", err)), nil
	}
	return mcp.NewToolResultText(string(output)), nil
}
