package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	textdiff "github.com/adrianliechti/wingman-agent/pkg/diff"
	"github.com/adrianliechti/wingman-agent/pkg/settings"
)

const (
	editorTabEditLinesAbove      = 2
	editorTabEditLinesBelow      = 5
	editorTabContextLines        = 15
	editorTabMaxPath             = 4 << 10
	editorTabMaxDocument         = 1 << 20
	editorTabMaxPrompt           = 48 << 10
	editorTabMaxHeader           = 4 << 10
	editorTabMaxBlock            = 8 << 10
	editorTabMaxChange           = 4 << 10
	editorTabMaxOutput           = 10 << 10
	editorTabRequestLimit        = 2*editorTabMaxDocument + 32<<10
	editorTabDefaultCursorMarker = "<CURSOR>"
	editorTabDefaultStartMarker  = "<EDIT_START>"
	editorTabDefaultEndMarker    = "<EDIT_END>"
	editorTabDefaultAreaStart    = "<AREA_START>"
	editorTabDefaultAreaEnd      = "<AREA_END>"
	editorTabDefaultOmitMarker   = "<OMITTED>"
)

const editorTabInstructions = `Predict the single next code edit the developer is most likely to make.
RECENT_CHANGE is their latest edit and is the strongest evidence of intent. AREA_START and AREA_END identify the local focus. CURSOR is the current caret. Only text between EDIT_START and EDIT_END is writable; everything else is context.

Continue an unfinished expression or statement at the cursor when the continuation is clear. Otherwise make one nearby follow-up that is directly implied by the recent edit, such as updating the next use after a rename. Do not invent a new task, refactor unrelated code, repeat code already in CURRENT_FILE, or undo the recent edit.

Classify edit_intent as high only when the edit is directly implied and very likely to be accepted; medium when plausible but not certain; low when speculative; and no_edit when no useful edit is clear.

Return the complete writable window in updated_window with exactly one contiguous edit applied. Preserve all other bytes and do not skip any lines. For no_edit, return the writable window unchanged. Return no explanation or Markdown and never copy prompt markers into updated_window.`

var editorTabOutputSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"updated_window": map[string]any{
			"type":        "string",
			"description": "The complete current edit window with one likely next edit applied, or the unchanged window.",
		},
		"edit_intent": map[string]any{
			"type":        "string",
			"enum":        []string{"no_edit", "low", "medium", "high"},
			"description": "Confidence that the proposed edit is the developer's immediate next action.",
		},
	},
	"required":             []string{"updated_window", "edit_intent"},
	"additionalProperties": false,
}

type editorTabRequest struct {
	Path            string `json:"path"`
	Content         string `json:"content"`
	PreviousContent string `json:"previous_content"`
	Line            int    `json:"line"`
	Column          int    `json:"column"`
	Version         int    `json:"version"`
}

type editorTabRange struct {
	StartLine   int `json:"start_line"`
	StartColumn int `json:"start_column"`
	EndLine     int `json:"end_line"`
	EndColumn   int `json:"end_column"`
}

type editorTabEdit struct {
	InsertText   string         `json:"insert_text"`
	ExpectedText string         `json:"expected_text"`
	Range        editorTabRange `json:"range"`
}

type editorTabResponse struct {
	Edit    *editorTabEdit `json:"edit"`
	Version int            `json:"version"`
}

type editorTabService struct {
	modelOverride string
	complete      func(context.Context, string) (editorTabCompletion, error)
}

type editorTabCompletion struct {
	UpdatedWindow string `json:"updated_window"`
	EditIntent    string `json:"edit_intent"`
}

type editorTabInputError struct {
	message string
}

func (e editorTabInputError) Error() string { return e.message }

type editorTabPrompt struct {
	text              string
	block             string
	blockOffset       int
	cursorOffset      int
	recentInsertStart int
	recentInsertEnd   int
	cursorMarker      string
	editStartMarker   string
	editEndMarker     string
	areaStartMarker   string
	areaEndMarker     string
	omitMarker        string
}

type editorTabMarkers struct {
	start     string
	cursor    string
	end       string
	areaStart string
	areaEnd   string
	omitted   string
}

func newEditorTabService(cfg *agent.Config) *editorTabService {
	return newEditorTabServiceForModel(cfg, os.Getenv("WINGMAN_MODEL_TAB"))
}

func newEditorTabServiceForModel(cfg *agent.Config, model string) *editorTabService {
	service := &editorTabService{modelOverride: strings.TrimSpace(model)}
	service.complete = func(ctx context.Context, prompt string) (editorTabCompletion, error) {
		modelID, effort := resolveGenerationTarget(cfg, "utility", service.modelOverride)
		result, err := cfg.Generate(ctx, agent.GenerateOptions{
			Model:           modelID,
			Effort:          effort,
			Instructions:    editorTabInstructions,
			Input:           prompt,
			OutputSchema:    editorTabOutputSchema,
			MaxOutputTokens: 4_096,
		})
		if err != nil {
			return editorTabCompletion{}, err
		}
		var output editorTabCompletion
		if err := json.Unmarshal([]byte(result.Text), &output); err != nil {
			return editorTabCompletion{}, fmt.Errorf("decode Tab prediction: %w", err)
		}
		return output, nil
	}
	return service
}

func (s *editorTabService) predict(ctx context.Context, input editorTabRequest) (editorTabResponse, error) {
	prompt, err := buildEditorTabPrompt(input)
	if err != nil {
		return editorTabResponse{}, err
	}
	completion, err := s.complete(ctx, prompt.text)
	if err != nil {
		return editorTabResponse{}, err
	}
	if !editorTabShowsIntent(completion.EditIntent) {
		return editorTabResponse{Version: input.Version}, nil
	}
	return editorTabResponse{
		Edit:    editorTabPrediction(input.Content, prompt, completion.UpdatedWindow),
		Version: input.Version,
	}, nil
}

// Copilot's neutral/default aggressiveness shows medium and high intent.
// Wingman has one on/off setting, so use that neutral threshold here.
func editorTabShowsIntent(intent string) bool {
	return intent == "medium" || intent == "high"
}

func buildEditorTabPrompt(input editorTabRequest) (editorTabPrompt, error) {
	if input.Path == "" ||
		len(input.Path) > editorTabMaxPath ||
		strings.ContainsAny(input.Path, "\r\n") {
		return editorTabPrompt{}, editorTabInputError{message: "invalid path"}
	}
	if len(input.Content) > editorTabMaxDocument || len(input.PreviousContent) > editorTabMaxDocument {
		return editorTabPrompt{}, editorTabInputError{message: "document is too large for Tab"}
	}
	cursor, err := utf16PositionOffset(input.Content, input.Line, input.Column)
	if err != nil {
		return editorTabPrompt{}, editorTabInputError{message: err.Error()}
	}
	blockStartLine, blockEndLine, blockStart, _, block := editorLineWindow(input.Content, input.Line, editorTabEditLinesAbove, editorTabEditLinesBelow)
	if len(block) > editorTabMaxBlock {
		return editorTabPrompt{}, editorTabInputError{message: "cursor window is too large for Tab"}
	}
	areaStartLine, areaEndLine, areaStart, areaEnd, _ := editorLineWindow(input.Content, input.Line, editorTabContextLines, editorTabContextLines)
	relativeCursor := cursor - blockStart
	if relativeCursor < 0 || relativeCursor > len(block) {
		return editorTabPrompt{}, editorTabInputError{message: "cursor is outside the edit window"}
	}
	markers := editorTabPromptMarkers(input.Content, input.PreviousContent)
	recentChange := editorTabRecentChange(input.Path, input.PreviousContent, input.Content)
	recentInsertStart, recentInsertEnd := editorTabRecentInsertion(input.PreviousContent, input.Content)

	var out strings.Builder
	fmt.Fprintf(
		&out,
		"File: %s\nCursor: %d:%d\nFocus: lines %d-%d (%s %s)\nWritable: lines %d-%d (%s %s)\nCursor marker: %s\nOmission marker: %s\n\n",
		input.Path,
		input.Line,
		input.Column,
		areaStartLine,
		areaEndLine,
		markers.areaStart,
		markers.areaEnd,
		blockStartLine,
		blockEndLine,
		markers.start,
		markers.end,
		markers.cursor,
		markers.omitted,
	)
	writeEditorTabPromptBlock(&out, "RECENT_CHANGE", recentChange)
	out.WriteString("<CURRENT_FILE>\n")
	closing := "\n</CURRENT_FILE>\n"
	fileLimit := editorTabMaxPrompt - out.Len() - len(closing)
	fileContext, err := editorTabCurrentFileContext(
		input.Content,
		areaStart,
		areaEnd,
		blockStart,
		blockStart+len(block),
		cursor,
		fileLimit,
		markers,
	)
	if err != nil {
		return editorTabPrompt{}, editorTabInputError{message: err.Error()}
	}
	out.WriteString(fileContext)
	out.WriteString(closing)
	if out.Len() > editorTabMaxPrompt {
		return editorTabPrompt{}, editorTabInputError{message: "Tab prompt exceeds its context budget"}
	}

	return editorTabPrompt{
		text:              out.String(),
		block:             block,
		blockOffset:       blockStart,
		cursorOffset:      relativeCursor,
		recentInsertStart: recentInsertStart,
		recentInsertEnd:   recentInsertEnd,
		cursorMarker:      markers.cursor,
		editStartMarker:   markers.start,
		editEndMarker:     markers.end,
		areaStartMarker:   markers.areaStart,
		areaEndMarker:     markers.areaEnd,
		omitMarker:        markers.omitted,
	}, nil
}

// editorTabRecentInsertion is the inserted portion of the normalized recent
// edit in current-document coordinates. The Monaco bridge sends the document
// before and after the current typing burst, so one normalized range represents
// the recent edit history used for this request.
func editorTabRecentInsertion(previous, current string) (start, end int) {
	start = commonUTF8Prefix(previous, current)
	suffix := commonUTF8Suffix(previous[start:], current[start:])
	return start, len(current) - suffix
}

func editorTabPromptMarkers(contents ...string) editorTabMarkers {
	for sequence := 0; ; sequence++ {
		suffix := ""
		if sequence > 0 {
			suffix = fmt.Sprintf("_%d", sequence)
		}
		markers := editorTabMarkers{
			start:     strings.TrimSuffix(editorTabDefaultStartMarker, ">") + suffix + ">",
			cursor:    strings.TrimSuffix(editorTabDefaultCursorMarker, ">") + suffix + ">",
			end:       strings.TrimSuffix(editorTabDefaultEndMarker, ">") + suffix + ">",
			areaStart: strings.TrimSuffix(editorTabDefaultAreaStart, ">") + suffix + ">",
			areaEnd:   strings.TrimSuffix(editorTabDefaultAreaEnd, ">") + suffix + ">",
			omitted:   strings.TrimSuffix(editorTabDefaultOmitMarker, ">") + suffix + ">",
		}
		collision := false
		for _, content := range contents {
			if strings.Contains(content, markers.start) ||
				strings.Contains(content, markers.cursor) ||
				strings.Contains(content, markers.end) ||
				strings.Contains(content, markers.areaStart) ||
				strings.Contains(content, markers.areaEnd) ||
				strings.Contains(content, markers.omitted) {
				collision = true
				break
			}
		}
		if !collision {
			return markers
		}
	}
}

func editorTabRecentChange(path, previous, current string) string {
	if previous == current {
		return "(none)"
	}
	prefix := commonUTF8Prefix(previous, current)
	suffix := commonUTF8Suffix(previous[prefix:], current[prefix:])
	oldStart := lineStartOffset(previous, prefix)
	newStart := lineStartOffset(current, prefix)
	oldEnd := lineEndOffset(previous, len(previous)-suffix)
	newEnd := lineEndOffset(current, len(current)-suffix)
	oldText := previous[oldStart:oldEnd]
	newText := current[newStart:newEnd]
	if len(oldText)+len(newText) > editorTabMaxChange {
		return "The recent change is too large to summarize safely."
	}
	line, _ := offsetUTF16Position(previous, oldStart)
	var out strings.Builder
	fmt.Fprintf(&out, "--- %s\n+++ %s\n@@ line %d @@\n", path, path, line)
	writeEditorTabDiffLines(&out, '-', oldText)
	writeEditorTabDiffLines(&out, '+', newText)
	return strings.TrimSuffix(out.String(), "\n")
}

func lineStartOffset(content string, offset int) int {
	offset = min(max(offset, 0), len(content))
	return strings.LastIndexByte(content[:offset], '\n') + 1
}

func lineEndOffset(content string, offset int) int {
	offset = min(max(offset, 0), len(content))
	if newline := strings.IndexByte(content[offset:], '\n'); newline >= 0 {
		return offset + newline
	}
	return len(content)
}

func writeEditorTabDiffLines(out *strings.Builder, prefix byte, text string) {
	lines := strings.Split(text, "\n")
	if len(lines) > 1 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for _, line := range lines {
		out.WriteByte(prefix)
		out.WriteString(line)
		out.WriteByte('\n')
	}
}

func editorTabCurrentFileContext(
	content string,
	areaStart, areaEnd int,
	blockStart, blockEnd, cursor, limit int,
	markers editorTabMarkers,
) (string, error) {
	if areaStart < 0 || blockStart < areaStart || cursor < blockStart || cursor > blockEnd ||
		blockEnd > areaEnd || areaEnd > len(content) {
		return "", errors.New("invalid Tab edit window")
	}
	markerBytes := len(markers.areaStart) + len(markers.start) + len(markers.cursor) +
		len(markers.end) + len(markers.areaEnd) + 6
	if len(content)+markerBytes <= limit {
		return renderEditorTabFileSlice(content, 0, len(content), areaStart, areaEnd, blockStart, blockEnd, cursor, markers), nil
	}
	omissionBytes := len(markers.omitted) + 2
	sourceBudget := limit - markerBytes - 2*omissionBytes
	areaBytes := areaEnd - areaStart
	if sourceBudget < areaBytes {
		return "", errors.New("cursor window exceeds the Tab prompt budget")
	}

	headerBudget := min(editorTabMaxHeader, sourceBudget/8)
	localBudget := sourceBudget - headerBudget
	extra := localBudget - areaBytes
	prefixBytes := min(areaStart, extra*3/4)
	suffixBytes := min(len(content)-areaEnd, extra-prefixBytes)
	remaining := extra - prefixBytes - suffixBytes
	if remaining > 0 {
		added := min(areaStart-prefixBytes, remaining)
		prefixBytes += added
		remaining -= added
		suffixBytes += min(len(content)-areaEnd-suffixBytes, remaining)
	}

	localStart := lineStartAtOrAfter(content, areaStart-prefixBytes, areaStart)
	localEnd := lineEndAtOrBefore(content, areaEnd, areaEnd+suffixBytes)
	headerEnd := lineEndAtOrBefore(content, 0, headerBudget)
	if headerEnd >= localStart {
		headerEnd = 0
		localStart = 0
		localEnd = lineEndAtOrBefore(content, areaEnd, min(len(content), localEnd+headerBudget))
	}

	var out strings.Builder
	if headerEnd > 0 {
		out.WriteString(content[:headerEnd])
		out.WriteByte('\n')
		out.WriteString(markers.omitted)
		out.WriteByte('\n')
	} else if localStart > 0 {
		out.WriteString(markers.omitted)
		out.WriteByte('\n')
	}
	out.WriteString(renderEditorTabFileSlice(content, localStart, localEnd, areaStart, areaEnd, blockStart, blockEnd, cursor, markers))
	if localEnd < len(content) {
		out.WriteByte('\n')
		out.WriteString(markers.omitted)
	}
	if out.Len() > limit {
		return "", errors.New("Tab prompt exceeds its context budget")
	}
	return out.String(), nil
}

func renderEditorTabFileSlice(
	content string, start, end, areaStart, areaEnd, blockStart, blockEnd, cursor int,
	markers editorTabMarkers,
) string {
	var out strings.Builder
	out.Grow(end - start + len(markers.areaStart) + len(markers.start) + len(markers.cursor) + len(markers.end) + len(markers.areaEnd) + 6)
	out.WriteString(content[start:areaStart])
	out.WriteString(markers.areaStart)
	out.WriteByte('\n')
	out.WriteString(content[areaStart:blockStart])
	out.WriteString(markers.start)
	out.WriteByte('\n')
	out.WriteString(content[blockStart:cursor])
	out.WriteString(markers.cursor)
	out.WriteString(content[cursor:blockEnd])
	if blockEnd > blockStart && content[blockEnd-1] != '\n' {
		out.WriteByte('\n')
	}
	out.WriteString(markers.end)
	out.WriteByte('\n')
	out.WriteString(content[blockEnd:areaEnd])
	if areaEnd > blockEnd && content[areaEnd-1] != '\n' {
		out.WriteByte('\n')
	}
	out.WriteString(markers.areaEnd)
	out.WriteByte('\n')
	out.WriteString(content[areaEnd:end])
	return out.String()
}

func runeStartAtOrAfter(content string, offset int) int {
	offset = min(max(offset, 0), len(content))
	for offset < len(content) && !utf8.RuneStart(content[offset]) {
		offset++
	}
	return offset
}

func runeStartAtOrBefore(content string, offset int) int {
	offset = min(max(offset, 0), len(content))
	for offset > 0 && offset < len(content) && !utf8.RuneStart(content[offset]) {
		offset--
	}
	return offset
}

func lineStartAtOrAfter(content string, offset, limit int) int {
	offset = runeStartAtOrAfter(content, offset)
	limit = min(max(limit, offset), len(content))
	if offset == 0 || offset >= limit {
		return offset
	}
	if newline := strings.IndexByte(content[offset:limit], '\n'); newline >= 0 {
		return offset + newline + 1
	}
	return offset
}

func lineEndAtOrBefore(content string, start, offset int) int {
	offset = runeStartAtOrBefore(content, offset)
	start = min(max(start, 0), offset)
	if offset == len(content) || offset <= start {
		return offset
	}
	if newline := strings.LastIndexByte(content[start:offset], '\n'); newline >= 0 {
		return start + newline + 1
	}
	return offset
}

func writeEditorTabPromptBlock(out *strings.Builder, name, contents string) {
	fmt.Fprintf(out, "<%s>\n", name)
	out.WriteString(contents)
	if !strings.HasSuffix(contents, "\n") {
		out.WriteByte('\n')
	}
	fmt.Fprintf(out, "</%s>\n", name)
	out.WriteByte('\n')
}

func commonUTF8Prefix(a, b string) int {
	limit := min(len(a), len(b))
	i := 0
	for i < limit && a[i] == b[i] {
		i++
	}
	for i > 0 && i < len(a) && !utf8.RuneStart(a[i]) {
		i--
	}
	return i
}

func commonUTF8Suffix(a, b string) int {
	limit := min(len(a), len(b))
	i := 0
	for i < limit && a[len(a)-1-i] == b[len(b)-1-i] {
		i++
	}
	for i > 0 && i < len(a) && !utf8.RuneStart(a[len(a)-i]) {
		i--
	}
	return i
}

func editorLineWindow(content string, line, above, below int) (startLine, endLine, startOffset, endOffset int, text string) {
	starts := []int{0}
	for i := 0; i < len(content); i++ {
		if content[i] == '\n' {
			starts = append(starts, i+1)
		}
	}
	line = min(max(line, 1), len(starts))
	startLine = max(1, line-above)
	endLine = min(len(starts), line+below)
	startOffset = starts[startLine-1]
	if endLine < len(starts) {
		endOffset = starts[endLine]
	} else {
		endOffset = len(content)
	}
	return startLine, endLine, startOffset, endOffset, content[startOffset:endOffset]
}

func editorTabPrediction(content string, prompt editorTabPrompt, completion string) *editorTabEdit {
	if completion == "" || len(completion) > editorTabMaxOutput ||
		(prompt.omitMarker != "" && strings.Contains(completion, prompt.omitMarker)) ||
		(prompt.areaStartMarker != "" && strings.Contains(completion, prompt.areaStartMarker)) ||
		(prompt.areaEndMarker != "" && strings.Contains(completion, prompt.areaEndMarker)) {
		return nil
	}
	updated := strings.ReplaceAll(completion, prompt.cursorMarker, "")
	updated = strings.ReplaceAll(updated, prompt.editStartMarker, "")
	updated = strings.ReplaceAll(updated, prompt.editEndMarker, "")
	if strings.HasSuffix(prompt.block, "\n") && !strings.HasSuffix(updated, "\n") {
		updated += "\n"
	} else if !strings.HasSuffix(prompt.block, "\n") && strings.HasSuffix(updated, "\n") {
		updated = strings.TrimSuffix(updated, "\n")
	}
	if updated == prompt.block {
		return nil
	}
	change, ok := editorTabChangeAroundCursor(prompt.block, updated, prompt.cursorOffset)
	if !ok {
		return nil
	}
	start := prompt.blockOffset + change.BeforeStart
	end := prompt.blockOffset + change.BeforeEnd
	if start < 0 || end < start || end > len(content) {
		return nil
	}
	expected := content[start:end]
	insert := updated[change.AfterStart:change.AfterEnd]
	if expected == insert || len(expected)+len(insert) > editorTabMaxBlock {
		return nil
	}
	// Copilot's undo-insertion filter runs after diff normalization and only
	// rejects pure deletions that conflict with a recent user edit. Protect the
	// inserted part of our normalized typing burst in the same way.
	if insert == "" && start < prompt.recentInsertEnd && prompt.recentInsertStart < end {
		return nil
	}
	startLine, startColumn := offsetUTF16Position(content, start)
	endLine, endColumn := offsetUTF16Position(content, end)
	return &editorTabEdit{
		InsertText:   insert,
		ExpectedText: expected,
		Range: editorTabRange{
			StartLine:   startLine,
			StartColumn: startColumn,
			EndLine:     endLine,
			EndColumn:   endColumn,
		},
	}
}

// editorTabChangeAroundCursor mirrors Copilot's two-stage response reduction:
// select the relevant line hunk, then refine it to the semantic character hunk.
// Multiple semantic character hunks stay together because the protocol returns
// one coherent edit range.
func editorTabChangeAroundCursor(original, updated string, cursor int) (textdiff.Hunk, bool) {
	const timeout = 50 * time.Millisecond
	best, ok := editorTabClosestChange(textdiff.LineHunks(original, updated, timeout), cursor)
	if !ok {
		return textdiff.Hunk{}, false
	}

	originalBlock := original[best.BeforeStart:best.BeforeEnd]
	updatedBlock := updated[best.AfterStart:best.AfterEnd]
	characterHunks := textdiff.CharacterHunks(originalBlock, updatedBlock, timeout)
	semanticHunks := characterHunks[:0]
	for _, change := range characterHunks {
		oldText := originalBlock[change.BeforeStart:change.BeforeEnd]
		newText := updatedBlock[change.AfterStart:change.AfterEnd]
		if strings.TrimSpace(oldText) == "" && strings.TrimSpace(newText) == "" {
			continue
		}
		semanticHunks = append(semanticHunks, change)
	}
	if len(semanticHunks) == 0 {
		return textdiff.Hunk{}, false
	}
	change := semanticHunks[0]
	if len(semanticHunks) > 1 {
		last := semanticHunks[len(semanticHunks)-1]
		change.BeforeEnd = last.BeforeEnd
		change.AfterEnd = last.AfterEnd
	}
	beforeText := originalBlock[change.BeforeStart:change.BeforeEnd]
	afterText := updatedBlock[change.AfterStart:change.AfterEnd]
	prefix := commonUTF8Prefix(beforeText, afterText)
	suffix := commonUTF8Suffix(beforeText[prefix:], afterText[prefix:])
	change.BeforeStart += prefix
	change.BeforeEnd -= suffix
	change.AfterStart += prefix
	change.AfterEnd -= suffix
	change.BeforeStart += best.BeforeStart
	change.BeforeEnd += best.BeforeStart
	change.AfterStart += best.AfterStart
	change.AfterEnd += best.AfterStart
	return change, true
}

func editorTabClosestChange(hunks []textdiff.Hunk, cursor int) (textdiff.Hunk, bool) {
	if len(hunks) == 0 {
		return textdiff.Hunk{}, false
	}
	best := hunks[0]
	bestRank, bestDistance := editorTabChangeDistance(best, cursor)
	for _, candidate := range hunks[1:] {
		rank, distance := editorTabChangeDistance(candidate, cursor)
		if rank < bestRank || rank == bestRank && distance < bestDistance {
			best, bestRank, bestDistance = candidate, rank, distance
		}
	}
	return best, true
}

func editorTabChangeDistance(change textdiff.Hunk, cursor int) (rank, distance int) {
	if change.BeforeStart <= cursor && cursor <= change.BeforeEnd {
		return 0, 0
	}
	if change.BeforeStart > cursor {
		return 1, change.BeforeStart - cursor
	}
	return 2, cursor - change.BeforeEnd
}

// utf16PositionOffset converts Monaco's one-based line and UTF-16 column to a
// UTF-8 byte offset.
func utf16PositionOffset(content string, line, column int) (int, error) {
	if line < 1 || column < 1 {
		return 0, errors.New("line and column must be positive")
	}
	lineStart := 0
	for current := 1; current < line; current++ {
		index := strings.IndexByte(content[lineStart:], '\n')
		if index < 0 {
			return 0, errors.New("line is outside the document")
		}
		lineStart += index + 1
	}
	targetUnits := column - 1
	units := 0
	for offset, r := range content[lineStart:] {
		if r == '\n' {
			break
		}
		if units == targetUnits {
			return lineStart + offset, nil
		}
		units += utf16.RuneLen(r)
		if units > targetUnits {
			return 0, errors.New("column splits a UTF-16 surrogate pair")
		}
	}
	if units == targetUnits {
		lineEnd := strings.IndexByte(content[lineStart:], '\n')
		if lineEnd < 0 {
			return len(content), nil
		}
		return lineStart + lineEnd, nil
	}
	return 0, errors.New("column is outside the document")
}

func offsetUTF16Position(content string, target int) (line, column int) {
	target = min(max(target, 0), len(content))
	line, column = 1, 1
	for offset, r := range content {
		if offset >= target {
			break
		}
		if r == '\n' {
			line, column = line+1, 1
			continue
		}
		column += utf16.RuneLen(r)
	}
	return line, column
}

func (s *Server) handleEditorTab(w http.ResponseWriter, r *http.Request) {
	if s.tab == nil {
		http.Error(w, "Tab is not configured", http.StatusNotFound)
		return
	}
	if !s.tabEnabled.Load() {
		http.Error(w, "Tab is disabled", http.StatusForbidden)
		return
	}
	var request editorTabRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, editorTabRequestLimit))
	if err := decoder.Decode(&request); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	ctx, finish := s.beginEditorTabRequest(r.Context())
	defer finish()
	response, err := s.tab.predict(ctx, request)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			http.Error(w, "Tab prediction timed out", http.StatusGatewayTimeout)
			return
		}
		var inputErr editorTabInputError
		if errors.As(err, &inputErr) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Error(w, "Tab prediction unavailable", http.StatusBadGateway)
		return
	}
	writeJSON(w, response)
}

// beginEditorTabRequest keeps only the newest model request alive. This caps
// useful concurrent work without rejecting the replacement after an edit or
// cursor move cancels a stale request.
func (s *Server) beginEditorTabRequest(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	s.tabRequestMu.Lock()
	previous := s.tabRequestCancel
	s.tabRequestID++
	requestID := s.tabRequestID
	s.tabRequestCancel = cancel
	s.tabRequestMu.Unlock()
	if previous != nil {
		previous()
	}
	return ctx, func() {
		cancel()
		s.tabRequestMu.Lock()
		if s.tabRequestID == requestID {
			s.tabRequestCancel = nil
		}
		s.tabRequestMu.Unlock()
	}
}

func (s *Server) handleEditorTabSettings(w http.ResponseWriter, r *http.Request) {
	if s.tab == nil {
		http.Error(w, "Tab is not configured", http.StatusNotFound)
		return
	}
	var request struct {
		Enabled *bool `json:"editor.tab.completion"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if request.Enabled == nil {
		http.Error(w, "editor.tab.completion is required", http.StatusBadRequest)
		return
	}
	s.tabSettingsMu.Lock()
	updated, err := settings.Update(func(value *settings.Settings) {
		value.EditorTabCompletion = *request.Enabled
	})
	if err == nil {
		s.tabEnabled.Store(updated.EditorTabCompletion)
	}
	s.tabSettingsMu.Unlock()
	if err != nil {
		http.Error(w, "could not save editor.tab.completion", http.StatusInternalServerError)
		return
	}
	s.broadcast(Frame{Type: EvtCapabilitiesChanged})
	writeJSON(w, map[string]bool{"editor.tab.completion": updated.EditorTabCompletion})
}
