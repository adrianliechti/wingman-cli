package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
)

const (
	editorTransformMaxInstruction = 8 << 10
	editorTransformMaxSelection   = 48 << 10
	editorTransformMaxContext     = 24 << 10
	editorTransformMaxOutput      = 64 << 10
	editorTransformRequestLimit   = editorTabMaxDocument + 64<<10
)

const editorTransformInstructions = `Transform exactly the selected text according to the user's instruction.
The surrounding file excerpt is context only. Do not modify or repeat text outside the selection. Preserve the file's style, indentation, and line endings. Return replacement text, not a diff or explanation. Do not wrap the replacement in Markdown fences unless the user explicitly asks for Markdown.`

var editorTransformOutputSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"replacement": map[string]any{
			"type":        "string",
			"description": "The complete replacement for the selected text.",
		},
	},
	"required":             []string{"replacement"},
	"additionalProperties": false,
}

type editorTransformRange struct {
	StartLine   int `json:"start_line"`
	StartColumn int `json:"start_column"`
	EndLine     int `json:"end_line"`
	EndColumn   int `json:"end_column"`
}

type editorTransformRequest struct {
	Path        string               `json:"path"`
	Content     string               `json:"content"`
	Range       editorTransformRange `json:"range"`
	Instruction string               `json:"instruction"`
	Version     int                  `json:"version"`
}

type editorTransformEdit struct {
	Range        editorTransformRange `json:"range"`
	ExpectedText string               `json:"expected_text"`
	Replacement  string               `json:"replacement"`
}

type editorTransformResponse struct {
	Edit    *editorTransformEdit `json:"edit"`
	Version int                  `json:"version"`
}

type editorTransformCompletion struct {
	Replacement string `json:"replacement"`
}

type editorTransformService struct {
	complete func(context.Context, string) (editorTransformCompletion, error)
}

type editorTransformInputError struct {
	message string
}

func (e editorTransformInputError) Error() string { return e.message }

func newEditorTransformService(cfg *agent.Config) *editorTransformService {
	service := &editorTransformService{}
	service.complete = func(ctx context.Context, prompt string) (editorTransformCompletion, error) {
		modelID, effort := resolveGenerationTarget(cfg, "utility", "")
		result, err := cfg.Generate(ctx, agent.GenerateOptions{
			Model:           modelID,
			Effort:          effort,
			Instructions:    editorTransformInstructions,
			Input:           prompt,
			OutputSchema:    editorTransformOutputSchema,
			MaxOutputTokens: 16_384,
		})
		if err != nil {
			return editorTransformCompletion{}, err
		}
		var output editorTransformCompletion
		if err := json.Unmarshal([]byte(result.Text), &output); err != nil {
			return editorTransformCompletion{}, fmt.Errorf("decode inline transformation: %w", err)
		}
		return output, nil
	}
	return service
}

func (s *editorTransformService) transform(ctx context.Context, input editorTransformRequest) (editorTransformResponse, error) {
	prompt, selected, markers, err := buildEditorTransformPrompt(input)
	if err != nil {
		return editorTransformResponse{}, err
	}
	completion, err := s.complete(ctx, prompt)
	if err != nil {
		return editorTransformResponse{}, err
	}
	if !utf8.ValidString(completion.Replacement) || len(completion.Replacement) > editorTransformMaxOutput || strings.ContainsRune(completion.Replacement, 0) {
		return editorTransformResponse{}, errors.New("inline transformation returned invalid replacement text")
	}
	for _, marker := range markers {
		if strings.Contains(completion.Replacement, marker) {
			return editorTransformResponse{}, errors.New("inline transformation copied a prompt marker")
		}
	}
	response := editorTransformResponse{Version: input.Version}
	if completion.Replacement == selected {
		return response, nil
	}
	response.Edit = &editorTransformEdit{
		Range:        input.Range,
		ExpectedText: selected,
		Replacement:  completion.Replacement,
	}
	return response, nil
}

func buildEditorTransformPrompt(input editorTransformRequest) (string, string, []string, error) {
	input.Instruction = strings.TrimSpace(input.Instruction)
	if input.Path == "" || len(input.Path) > editorTabMaxPath || strings.ContainsAny(input.Path, "\r\n") {
		return "", "", nil, editorTransformInputError{message: "invalid path"}
	}
	if input.Instruction == "" || len(input.Instruction) > editorTransformMaxInstruction || !utf8.ValidString(input.Instruction) {
		return "", "", nil, editorTransformInputError{message: "invalid transformation instruction"}
	}
	if len(input.Content) > editorTabMaxDocument || !utf8.ValidString(input.Content) {
		return "", "", nil, editorTransformInputError{message: "document is too large or is not valid UTF-8"}
	}
	start, err := utf16PositionOffset(input.Content, input.Range.StartLine, input.Range.StartColumn)
	if err != nil {
		return "", "", nil, editorTransformInputError{message: "invalid selection start: " + err.Error()}
	}
	end, err := utf16PositionOffset(input.Content, input.Range.EndLine, input.Range.EndColumn)
	if err != nil {
		return "", "", nil, editorTransformInputError{message: "invalid selection end: " + err.Error()}
	}
	if end <= start {
		return "", "", nil, editorTransformInputError{message: "a non-empty selection is required"}
	}
	selected := input.Content[start:end]
	if len(selected) > editorTransformMaxSelection {
		return "", "", nil, editorTransformInputError{message: "selection is too large to transform inline"}
	}

	startMarker, endMarker, omitMarker := editorTransformPromptMarkers(input.Content, input.Instruction)
	before, beforeOmitted := transformContextBefore(input.Content, start, editorTransformMaxContext/2)
	after, afterOmitted := transformContextAfter(input.Content, end, editorTransformMaxContext/2)
	var excerpt strings.Builder
	if beforeOmitted {
		excerpt.WriteString(omitMarker)
		excerpt.WriteByte('\n')
	}
	excerpt.WriteString(before)
	excerpt.WriteString(startMarker)
	excerpt.WriteString(selected)
	excerpt.WriteString(endMarker)
	excerpt.WriteString(after)
	if afterOmitted {
		if excerpt.Len() > 0 && !strings.HasSuffix(excerpt.String(), "\n") {
			excerpt.WriteByte('\n')
		}
		excerpt.WriteString(omitMarker)
	}

	prompt := fmt.Sprintf(
		"File: %s\nSelection: %d:%d-%d:%d\nInstruction:\n%s\n\n<FILE_EXCERPT>\n%s\n</FILE_EXCERPT>",
		input.Path,
		input.Range.StartLine,
		input.Range.StartColumn,
		input.Range.EndLine,
		input.Range.EndColumn,
		input.Instruction,
		excerpt.String(),
	)
	return prompt, selected, []string{startMarker, endMarker, omitMarker}, nil
}

func editorTransformPromptMarkers(values ...string) (string, string, string) {
	for sequence := 0; ; sequence++ {
		suffix := ""
		if sequence > 0 {
			suffix = fmt.Sprintf("_%d", sequence)
		}
		start := "<SELECTION_START" + suffix + ">"
		end := "<SELECTION_END" + suffix + ">"
		omitted := "<OMITTED" + suffix + ">"
		collision := false
		for _, value := range values {
			if strings.Contains(value, start) || strings.Contains(value, end) || strings.Contains(value, omitted) {
				collision = true
				break
			}
		}
		if !collision {
			return start, end, omitted
		}
	}
}

func transformContextBefore(content string, end, limit int) (string, bool) {
	start := max(0, end-limit)
	for start < end && !utf8.RuneStart(content[start]) {
		start++
	}
	if start > 0 {
		if newline := strings.IndexByte(content[start:end], '\n'); newline >= 0 {
			start += newline + 1
		}
	}
	return content[start:end], start > 0
}

func transformContextAfter(content string, start, limit int) (string, bool) {
	end := min(len(content), start+limit)
	for end > start && end < len(content) && !utf8.RuneStart(content[end]) {
		end--
	}
	if end < len(content) {
		if newline := strings.LastIndexByte(content[start:end], '\n'); newline >= 0 {
			end = start + newline + 1
		}
	}
	return content[start:end], end < len(content)
}

func (s *Server) handleEditorTransform(w http.ResponseWriter, r *http.Request) {
	if s.transforms == nil {
		http.Error(w, "inline transformations are not configured", http.StatusNotFound)
		return
	}
	var request editorTransformRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, editorTransformRequestLimit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	response, err := s.transforms.transform(ctx, request)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			http.Error(w, "inline transformation timed out", http.StatusGatewayTimeout)
			return
		}
		var inputErr editorTransformInputError
		if errors.As(err, &inputErr) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Error(w, "inline transformation unavailable", http.StatusBadGateway)
		return
	}
	writeJSON(w, response)
}
