package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// defaultSnapToleranceSec is how far a chapter start may be from a track
// boundary and still move onto it.
const defaultSnapToleranceSec = 15.0

// Differences between the Audible runtime and the audio duration beyond this
// many seconds usually mean the recording is a different edition.
const runtimeMismatchWarnSec = 5.0

// Settings for estimating how far the recording is shifted against the Audible
// chapter list.
const (
	// offsetWindow is the number of chapters on each side that vote on the
	// offset of a chapter.
	offsetWindow = 4
	// offsetAgreeSec is how far a chapter's own offset may differ from the
	// local estimate and still count as agreeing with it.
	offsetAgreeSec = 3.0
	// offsetMinSupport is the share of chapters that must agree with the
	// estimate before it is used at all.
	offsetMinSupport  = 0.5
	offsetMinChapters = 8
	offsetMinTracks   = 4
)

// trackStepWarnSec is how much the offset between a track and its Audible
// chapter may change from one track to the next before it is reported.
const trackStepWarnSec = 15.0

type audibleChapter struct {
	Title         string `json:"title"`
	LengthMs      int64  `json:"lengthMs"`
	StartOffsetMs int64  `json:"startOffsetMs"`
}

type audibleChapterData struct {
	ASIN                 string           `json:"asin"`
	BrandIntroDurationMs int64            `json:"brandIntroDurationMs"`
	BrandOutroDurationMs int64            `json:"brandOutroDurationMs"`
	RuntimeLengthMs      int64            `json:"runtimeLengthMs"`
	Chapters             []audibleChapter `json:"chapters"`
	Error                string           `json:"error"`
}

type absChapter struct {
	ID    int     `json:"id"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Title string  `json:"title"`
}

type absAudioFile struct {
	Index                int     `json:"index"`
	Ino                  string  `json:"ino"`
	Duration             float64 `json:"duration"`
	Exclude              bool    `json:"exclude"`
	TrackNumFromMeta     *int    `json:"trackNumFromMeta"`
	DiscNumFromMeta      *int    `json:"discNumFromMeta"`
	TrackNumFromFilename *int    `json:"trackNumFromFilename"`
	DiscNumFromFilename  *int    `json:"discNumFromFilename"`
	Metadata             struct {
		Filename string `json:"filename"`
	} `json:"metadata"`
}

type absBookItem struct {
	Media struct {
		Metadata struct {
			Title string `json:"title"`
			ASIN  string `json:"asin"`
		} `json:"metadata"`
		AudioFiles []absAudioFile `json:"audioFiles"`
		Chapters   []absChapter   `json:"chapters"`
	} `json:"media"`
}

type plannedChapter struct {
	Index       int      `json:"index"`
	Title       string   `json:"title"`
	OldTitle    string   `json:"old_title,omitempty"`
	Start       float64  `json:"start"`
	End         float64  `json:"end"`
	SnappedFrom *float64 `json:"snapped_from,omitempty"`
	ShiftedFrom *float64 `json:"shifted_from,omitempty"`
	// AudibleStart is the original Audible start time when an estimated offset
	// moved the chapter.
	AudibleStart *float64 `json:"audible_start,omitempty"`
}

func fetchBookItem(ctx context.Context, baseURL, token, itemID string) (*absBookItem, error) {
	body, err := absGET(ctx, baseURL, token, fmt.Sprintf("/items/%s", url.PathEscape(itemID)))
	if err != nil {
		return nil, err
	}
	var item absBookItem
	if err := json.Unmarshal(body, &item); err != nil {
		return nil, fmt.Errorf("decode item: %w", err)
	}
	return &item, nil
}

// includedAudioFiles returns the playable tracks in playback order.
func includedAudioFiles(files []absAudioFile) []absAudioFile {
	included := make([]absAudioFile, 0, len(files))
	for _, file := range files {
		if !file.Exclude {
			included = append(included, file)
		}
	}
	sort.SliceStable(included, func(i, j int) bool { return included[i].Index < included[j].Index })
	return included
}

// trackStartOffsets returns the start time of every playable track and the
// total duration, both in seconds.
func trackStartOffsets(files []absAudioFile) ([]float64, float64) {
	included := includedAudioFiles(files)
	starts := make([]float64, 0, len(included))
	total := 0.0
	for _, file := range included {
		starts = append(starts, total)
		total += file.Duration
	}
	return starts, total
}

// removeAudibleBranding mirrors the web client: chapter starts move forward by
// the length of the Audible intro, and a trailing chapter that is no longer
// than the Audible outro is dropped.
func removeAudibleBranding(data *audibleChapterData) {
	intro, outro := data.BrandIntroDurationMs, data.BrandOutroDurationMs

	for i := range data.Chapters {
		chapter := &data.Chapters[i]
		if chapter.StartOffsetMs < intro {
			chapter.StartOffsetMs = int64(i) * 1000
		} else {
			chapter.StartOffsetMs -= intro
		}
	}

	if n := len(data.Chapters); n > 0 && data.Chapters[n-1].LengthMs <= outro {
		data.Chapters = data.Chapters[:n-1]
	}

	data.RuntimeLengthMs -= intro + outro
}

// brandingAlignmentScore adds up how far each chapter start is from the
// nearest track boundary, with every distance capped so chapters that start in
// the middle of a file weigh the same for both variants.
func brandingAlignmentScore(data audibleChapterData, trackStarts []float64) float64 {
	const maxDistance = 30.0
	score := 0.0
	for _, chapter := range data.Chapters {
		start := float64(chapter.StartOffsetMs) / 1000
		best := maxDistance
		for _, trackStart := range trackStarts {
			if diff := math.Abs(trackStart - start); diff < best {
				best = diff
			}
		}
		score += best
	}
	return score
}

// shouldRemoveBranding decides whether the audio was cut without the Audible
// intro and outro. It compares both variants against the track boundaries and
// only removes the branding if that lines more chapters up with the files. It
// needs at least two tracks, otherwise there is nothing to compare against.
func shouldRemoveBranding(data audibleChapterData, trackStarts []float64) bool {
	if len(trackStarts) < 2 {
		return false
	}
	removed := data
	removed.Chapters = append([]audibleChapter(nil), data.Chapters...)
	removeAudibleBranding(&removed)
	return brandingAlignmentScore(removed, trackStarts) < brandingAlignmentScore(data, trackStarts)
}

func nearestTrackStart(start float64, trackStarts []float64, tolerance float64) (float64, bool) {
	best, bestDiff, found := 0.0, tolerance, false
	for _, trackStart := range trackStarts {
		diff := math.Abs(trackStart - start)
		if diff <= bestDiff {
			best, bestDiff, found = trackStart, diff, true
		}
	}
	return best, found
}

// estimateChapterOffsets works out how far the recording is shifted against
// the Audible chapter list at every chapter. Chapters usually start where a
// file starts, so the distance to the nearest track boundary is the offset. A
// running median over neighbouring chapters ignores chapters that start in the
// middle of a file, and follows offsets that change in steps, for example when
// the recording lacks a passage Audible has. The result is only returned if at
// least half of the chapters agree with it, which random distances would not;
// otherwise nil. Offsets are only found while they are smaller than half the
// distance between two track boundaries, because beyond that another boundary
// is nearer.
func estimateChapterOffsets(starts, trackStarts []float64) []float64 {
	if len(starts) < offsetMinChapters || len(trackStarts) < offsetMinTracks {
		return nil
	}

	raw := make([]float64, len(starts))
	for i, start := range starts {
		nearest := trackStarts[0]
		for _, trackStart := range trackStarts {
			if math.Abs(trackStart-start) < math.Abs(nearest-start) {
				nearest = trackStart
			}
		}
		raw[i] = nearest - start
	}

	offsets := make([]float64, len(starts))
	supported := 0
	for i := range raw {
		window := append([]float64(nil), raw[max(0, i-offsetWindow):min(len(raw), i+offsetWindow+1)]...)
		sort.Float64s(window)
		offsets[i] = window[len(window)/2]
		if math.Abs(raw[i]-offsets[i]) <= offsetAgreeSec {
			supported++
		}
	}
	if float64(supported) < offsetMinSupport*float64(len(starts)) {
		return nil
	}
	return offsets
}

// buildFullChapters turns Audible chapters into chapters covering the whole
// recording. Chapter starts that fall within tolerance seconds of a track
// boundary move onto that boundary, so a chapter does not begin with the last
// seconds of the previous file. A tolerance of 0 disables snapping.
func buildFullChapters(audible []audibleChapter, duration float64, trackStarts []float64, tolerance float64) ([]plannedChapter, []string) {
	var chapters []plannedChapter
	var warnings []string

	audibleStarts := make([]float64, len(audible))
	for i, chapter := range audible {
		audibleStarts[i] = float64(chapter.StartOffsetMs) / 1000
	}
	offsets := estimateChapterOffsets(audibleStarts, trackStarts)
	largestOffset := 0.0

	for i, chapter := range audible {
		audibleStart := audibleStarts[i]
		start := audibleStart
		var planned plannedChapter
		if offsets != nil && i > 0 && offsets[i] != 0 {
			start += offsets[i]
			largestOffset = math.Max(largestOffset, math.Abs(offsets[i]))
			if math.Abs(offsets[i]) >= 0.5 {
				planned.AudibleStart = &audibleStart
			}
		}
		if start >= duration {
			warnings = append(warnings, fmt.Sprintf("dropped %q: it starts at %.1fs, after the end of the audio (%.1fs)", chapter.Title, start, duration))
			continue
		}

		planned.Title, planned.Start = chapter.Title, start
		if tolerance > 0 && len(chapters) > 0 {
			if snapped, ok := nearestTrackStart(start, trackStarts, tolerance); ok && snapped != start {
				if snapped > chapters[len(chapters)-1].Start {
					original := start
					planned.Start = snapped
					planned.SnappedFrom = &original
				}
			}
		}

		if len(chapters) > 0 && planned.Start <= chapters[len(chapters)-1].Start {
			warnings = append(warnings, fmt.Sprintf("dropped %q: it would not start after the previous chapter", chapter.Title))
			continue
		}
		chapters = append(chapters, planned)
	}

	if tolerance > 0 {
		applyNeighbourShift(chapters)
	}
	if largestOffset >= 0.5 {
		warnings = append(warnings, fmt.Sprintf("the chapter times were corrected by up to %.0fs because most chapters start on file boundaries that are shifted against Audible; the original times are in audible_start", largestOffset))
	}

	for i := range chapters {
		chapters[i].Index = i
		if i+1 < len(chapters) {
			chapters[i].End = chapters[i+1].Start
		} else {
			chapters[i].End = duration
		}
	}

	return chapters, warnings
}

// applyNeighbourShift moves chapters that did not snap onto a track boundary,
// because they start in the middle of a file, by the shift of the nearest
// chapter that did. Recordings often differ from the Audible chapter list by a
// constant offset, for example a longer lead-in, or by a slow drift, and the
// snapped chapters measure exactly that. A chapter keeps its time if moving it
// would break the order of the chapters.
func applyNeighbourShift(chapters []plannedChapter) {
	var snapped []int
	for i, chapter := range chapters {
		if chapter.SnappedFrom != nil {
			snapped = append(snapped, i)
		}
	}
	if len(snapped) == 0 {
		return
	}

	for i := 1; i < len(chapters); i++ {
		if chapters[i].SnappedFrom != nil {
			continue
		}
		nearest := snapped[0]
		for _, j := range snapped {
			if math.Abs(chapters[i].Start-*chapters[j].SnappedFrom) < math.Abs(chapters[i].Start-*chapters[nearest].SnappedFrom) {
				nearest = j
			}
		}
		shift := chapters[nearest].Start - *chapters[nearest].SnappedFrom
		original := chapters[i].Start
		moved := original + shift
		if moved <= chapters[i-1].Start || (i+1 < len(chapters) && moved >= chapters[i+1].Start) {
			continue
		}
		chapters[i].Start = moved
		chapters[i].ShiftedFrom = &original
	}
}

// buildTrackChapters makes one chapter per audio file and names it after the
// Audible chapter at the same position. It needs as many tracks as chapters.
// Tracks that are much longer or shorter than their chapter are reported,
// because that means the files and the chapters are not the same segments.
func buildTrackChapters(audible []audibleChapter, files []absAudioFile) ([]plannedChapter, []string, error) {
	tracks := includedAudioFiles(files)
	if len(tracks) != len(audible) {
		return nil, nil, fmt.Errorf("the item has %d tracks but Audible lists %d chapters; mode \"tracks\" needs one chapter per track", len(tracks), len(audible))
	}

	var chapters []plannedChapter
	var warnings []string
	start, previousOffset, irregular := 0.0, 0.0, 0
	for i, track := range tracks {
		offset := start - float64(audible[i].StartOffsetMs)/1000
		if i > 0 && math.Abs(offset-previousOffset) > trackStepWarnSec {
			irregular++
			if irregular <= 5 {
				warnings = append(warnings, fmt.Sprintf("track %d (%s) is %.0fs %s than Audible chapter %d", i, tracks[i-1].Metadata.Filename, math.Abs(offset-previousOffset), map[bool]string{true: "longer", false: "shorter"}[offset > previousOffset], i))
			}
		}
		previousOffset = offset
		chapters = append(chapters, plannedChapter{Index: i, Title: audible[i].Title, Start: start, End: start + track.Duration})
		start += track.Duration
	}
	if irregular > 5 {
		warnings = append(warnings, fmt.Sprintf("%d tracks in total differ from their chapter by more than %.0fs", irregular, trackStepWarnSec))
	}
	return chapters, warnings, nil
}

// buildTitleOnlyChapters keeps the existing chapter times and takes the titles
// from Audible by position.
func buildTitleOnlyChapters(existing []absChapter, audible []audibleChapter, allowMismatch bool) ([]plannedChapter, error) {
	if len(existing) == 0 {
		return nil, fmt.Errorf("the item has no chapters, use mode \"full\" to create them")
	}
	if len(existing) != len(audible) && !allowMismatch {
		return nil, fmt.Errorf("the item has %d chapters but Audible lists %d; use mode \"full\" or set allow_count_mismatch to rename by position anyway", len(existing), len(audible))
	}

	sorted := append([]absChapter(nil), existing...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Start < sorted[j].Start })

	chapters := make([]plannedChapter, 0, len(sorted))
	for i, chapter := range sorted {
		title := chapter.Title
		if i < len(audible) {
			title = audible[i].Title
		}
		chapters = append(chapters, plannedChapter{
			Index:    i,
			Title:    title,
			OldTitle: chapter.Title,
			Start:    chapter.Start,
			End:      chapter.End,
		})
	}
	return chapters, nil
}

func handleSearchChapters(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	baseURL, token, err := getABSConfig(request)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	asin, err := requireNonBlankString(request, "asin")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	body, err := absGET(ctx, baseURL, token, "/search/chapters?"+chapterSearchQuery(asin, request.GetString("region", "")))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(string(body)), nil
}

func chapterSearchQuery(asin, region string) string {
	values := url.Values{}
	values.Set("asin", asin)
	if region != "" {
		values.Set("region", region)
	}
	return values.Encode()
}

func handleUpdateItemChapters(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	baseURL, token, err := getABSConfig(request)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	itemID, err := requireNonBlankString(request, "item_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	entries, ok := request.GetArguments()["chapters"].([]interface{})
	if !ok || len(entries) == 0 {
		return mcp.NewToolResultError("chapters must be a non-empty array"), nil
	}

	chapters := make([]map[string]interface{}, 0, len(entries))
	for i, entry := range entries {
		fields, ok := entry.(map[string]interface{})
		if !ok {
			return mcp.NewToolResultError(fmt.Sprintf("chapters[%d] must be an object", i)), nil
		}
		title, ok := fields["title"].(string)
		if !ok || strings.TrimSpace(title) == "" {
			return mcp.NewToolResultError(fmt.Sprintf("chapters[%d] requires a non-blank title", i)), nil
		}
		start, startOK := fields["start"].(float64)
		end, endOK := fields["end"].(float64)
		if !startOK || !endOK {
			return mcp.NewToolResultError(fmt.Sprintf("chapters[%d] requires numeric start and end in seconds", i)), nil
		}
		if start < 0 || end <= start {
			return mcp.NewToolResultError(fmt.Sprintf("chapters[%d] needs 0 <= start < end", i)), nil
		}
		chapters = append(chapters, map[string]interface{}{"title": title, "start": start, "end": end})
	}

	body, err := absPOST(ctx, baseURL, token, fmt.Sprintf("/items/%s/chapters", url.PathEscape(itemID)), map[string]interface{}{"chapters": chapters})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(string(body)), nil
}

func handleApplyAudibleChapters(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	baseURL, token, err := getABSConfig(request)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	itemID, err := requireNonBlankString(request, "item_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	mode := request.GetString("mode", "titles")
	if mode != "titles" && mode != "full" && mode != "tracks" {
		return mcp.NewToolResultError(`mode must be "titles", "full" or "tracks"`), nil
	}
	removeBranding, brandingExplicit := false, false
	if value, ok := request.GetArguments()["remove_branding"]; ok {
		flag, isBool := value.(bool)
		if !isBool {
			return mcp.NewToolResultError("remove_branding must be a boolean"), nil
		}
		removeBranding, brandingExplicit = flag, true
	}
	tolerance := request.GetFloat("snap_tolerance_sec", defaultSnapToleranceSec)
	if tolerance < 0 {
		return mcp.NewToolResultError("snap_tolerance_sec must not be negative"), nil
	}
	dryRun := request.GetBool("dry_run", true)

	book, err := fetchBookItem(ctx, baseURL, token, itemID)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	asin := request.GetString("asin", book.Media.Metadata.ASIN)
	if strings.TrimSpace(asin) == "" {
		return mcp.NewToolResultError("the item has no ASIN, pass asin or match the item first"), nil
	}

	body, err := absGET(ctx, baseURL, token, "/search/chapters?"+chapterSearchQuery(asin, request.GetString("region", "")))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	var data audibleChapterData
	if err := json.Unmarshal(body, &data); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("decode Audible chapters: %s", err)), nil
	}
	if data.Error != "" {
		return mcp.NewToolResultError(fmt.Sprintf("chapter lookup for %s failed: %s", asin, data.Error)), nil
	}
	if len(data.Chapters) == 0 {
		return mcp.NewToolResultError(fmt.Sprintf("Audible lists no chapters for %s", asin)), nil
	}

	trackStarts, duration := trackStartOffsets(book.Media.AudioFiles)
	if duration <= 0 {
		return mcp.NewToolResultError("the item has no playable audio tracks"), nil
	}

	if !brandingExplicit {
		removeBranding = shouldRemoveBranding(data, trackStarts)
	}
	if removeBranding {
		removeAudibleBranding(&data)
	}

	var warnings []string
	if diff := float64(data.RuntimeLengthMs)/1000 - duration; math.Abs(diff) > runtimeMismatchWarnSec {
		warnings = append(warnings, fmt.Sprintf("Audible runtime is %.0fs but the audio is %.0fs, a difference of %.0fs: this may be a different edition, check the result", float64(data.RuntimeLengthMs)/1000, duration, math.Abs(diff)))
	}

	stretchFactor := 1.0
	if request.GetBool("stretch_to_audio", false) {
		if mode != "full" {
			return mcp.NewToolResultError(`stretch_to_audio only applies to mode "full"`), nil
		}
		if data.RuntimeLengthMs <= 0 {
			return mcp.NewToolResultError("Audible gives no runtime to stretch the chapters to"), nil
		}
		stretchFactor = duration / (float64(data.RuntimeLengthMs) / 1000)
		for i := range data.Chapters {
			data.Chapters[i].StartOffsetMs = int64(math.Round(float64(data.Chapters[i].StartOffsetMs) * stretchFactor))
			data.Chapters[i].LengthMs = int64(math.Round(float64(data.Chapters[i].LengthMs) * stretchFactor))
		}
	}

	var planned []plannedChapter
	if mode == "titles" {
		planned, err = buildTitleOnlyChapters(book.Media.Chapters, data.Chapters, request.GetBool("allow_count_mismatch", false))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
	} else {
		var buildWarnings []string
		if mode == "tracks" {
			planned, buildWarnings, err = buildTrackChapters(data.Chapters, book.Media.AudioFiles)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
		} else {
			planned, buildWarnings = buildFullChapters(data.Chapters, duration, trackStarts, tolerance)
		}
		warnings = append(warnings, buildWarnings...)
		if len(planned) == 0 {
			return mcp.NewToolResultError("no Audible chapter fits into the audio"), nil
		}
		for i := range planned {
			if i < len(book.Media.Chapters) {
				planned[i].OldTitle = book.Media.Chapters[i].Title
			}
		}
	}

	result := map[string]interface{}{
		"item_id":            itemID,
		"title":              book.Media.Metadata.Title,
		"asin":               asin,
		"mode":               mode,
		"branding_removed":   removeBranding,
		"branding_detected":  !brandingExplicit,
		"stretch_factor":     stretchFactor,
		"dry_run":            dryRun,
		"audio_duration_sec": duration,
		"audible_runtime_ms": data.RuntimeLengthMs,
		"old_chapter_count":  len(book.Media.Chapters),
		"new_chapter_count":  len(planned),
		"warnings":           warnings,
		"chapters":           planned,
		"applied":            false,
	}

	if !dryRun {
		payload := make([]map[string]interface{}, 0, len(planned))
		for _, chapter := range planned {
			payload = append(payload, map[string]interface{}{"title": chapter.Title, "start": chapter.Start, "end": chapter.End})
		}
		response, err := absPOST(ctx, baseURL, token, fmt.Sprintf("/items/%s/chapters", url.PathEscape(itemID)), map[string]interface{}{"chapters": payload})
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		result["applied"] = true
		if json.Valid(response) {
			result["server_response"] = json.RawMessage(response)
		}
	}

	output, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("encode result: %s", err)), nil
	}
	return mcp.NewToolResultText(string(output)), nil
}
