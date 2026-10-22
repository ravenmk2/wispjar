package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

const ReplayKey = "wispjar_responses_replay"

type responsesModel struct {
	baseURL, apiKey, providerID, providerType, modelCode string
	params                                               map[string]any
	caps                                                 Capabilities
	media                                                *mediaStore
	client                                               *http.Client
	tools                                                []*schema.ToolInfo
}

type responseReplay struct {
	Scope  string            `json:"scope"`
	Model  string            `json:"model"`
	Output []json.RawMessage `json:"output"`
}

type responseItem struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Content   []struct {
		Type    string `json:"type"`
		Text    string `json:"text"`
		Refusal string `json:"refusal"`
	} `json:"content"`
	Summary []struct {
		Text string `json:"text"`
	} `json:"summary"`
}

type responseBody struct {
	Status string            `json:"status"`
	Output []json.RawMessage `json:"output"`
	Usage  struct {
		Input        int `json:"input_tokens"`
		Output       int `json:"output_tokens"`
		Total        int `json:"total_tokens"`
		InputDetails struct {
			Cached *int64 `json:"cached_tokens"`
		} `json:"input_tokens_details"`
		OutputDetails struct {
			Reasoning int `json:"reasoning_tokens"`
		} `json:"output_tokens_details"`
	} `json:"usage"`
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	Incomplete struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
}

func (m *responsesModel) WithTools(infos []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	copy := *m
	copy.tools = append([]*schema.ToolInfo(nil), infos...)
	return &copy, nil
}

func (m *responsesModel) input(ctx context.Context, messages []*schema.Message) ([]any, map[string]string, error) {
	projected, err := m.media.project(ctx, messages, m.caps.ToolImages && m.caps.Tools && m.caps.Vision)
	if err != nil {
		return nil, nil, err
	}
	input := []any{}
	files := map[string]string{}
	for _, message := range projected {
		if message.Role == schema.Assistant {
			var replay responseReplay
			if raw, ok := message.Extra[ReplayKey].(string); ok && json.Unmarshal([]byte(raw), &replay) == nil && replay.Scope == m.scope(ctx) && replay.Model == m.modelCode && len(replay.Output) > 0 {
				for _, item := range replay.Output {
					input = append(input, item)
				}
				continue
			}
			if message.Content != "" {
				input = append(input, map[string]any{"role": "assistant", "content": message.Content})
			}
			for _, call := range message.ToolCalls {
				input = append(input, map[string]any{"type": "function_call", "call_id": call.ID, "name": call.Function.Name, "arguments": call.Function.Arguments})
			}
			continue
		}
		parts := []any{}
		if message.Content != "" {
			parts = append(parts, map[string]any{"type": "input_text", "text": message.Content})
		}
		for _, part := range message.UserInputMultiContent {
			if part.Type == schema.ChatMessagePartTypeText {
				parts = append(parts, map[string]any{"type": "input_text", "text": part.Text})
				continue
			}
			if part.Image == nil {
				return nil, nil, errors.New("provider: unsupported Responses content part")
			}
			if part.Image.Base64Data != nil {
				parts = append(parts, map[string]any{"type": "input_image", "image_url": "data:" + part.Image.MIMEType + ";base64," + *part.Image.Base64Data})
				continue
			}
			id := referenceID(part)
			fileID, ok := files[id]
			if !ok {
				fileID, err = m.fileID(ctx, id)
				if err != nil {
					return nil, nil, err
				}
				files[id] = fileID
			}
			image := map[string]any{"type": "input_image", "file_id": fileID}
			if m.providerType != "ark" {
				image["detail"] = "auto"
			}
			parts = append(parts, image)
		}
		if message.Role == schema.Tool {
			var output any = parts
			if len(message.UserInputMultiContent) == 0 {
				output = message.Content
			}
			input = append(input, map[string]any{"type": "function_call_output", "call_id": message.ToolCallID, "output": output})
		} else {
			if len(parts) == 0 {
				parts = append(parts, map[string]any{"type": "input_text", "text": ""})
			}
			input = append(input, map[string]any{"role": message.Role, "content": parts})
		}
	}
	return input, files, nil
}

func (m *responsesModel) payload(input []any, stream bool, opts ...model.Option) (map[string]any, error) {
	options := model.GetCommonOptions(&model.Options{Tools: m.tools}, opts...)
	if len(options.Stop) > 0 || len(options.DeferredTools) > 0 || options.ToolSearchTool != nil {
		return nil, errors.New("provider: unsupported Responses model option")
	}
	body := map[string]any{"model": m.modelCode, "input": input, "stream": stream, "store": false}
	if m.providerType != "deepseek" {
		body["include"] = []string{"reasoning.encrypted_content"}
	}
	for _, key := range []string{"temperature", "top_p"} {
		if value, ok := numberParam(m.params, key); ok {
			body[key] = value
		}
	}
	if value, ok := numberParam(m.params, "max_tokens"); ok {
		body["max_output_tokens"] = int(value)
	}
	if options.Model != nil && *options.Model != m.modelCode {
		return nil, errors.New("provider: select another model through the factory")
	}
	if options.Temperature != nil {
		body["temperature"] = *options.Temperature
	}
	if options.TopP != nil {
		body["top_p"] = *options.TopP
	}
	if options.MaxTokens != nil {
		body["max_output_tokens"] = *options.MaxTokens
	}
	if effort, ok := stringParam(m.params, "reasoning_effort"); ok {
		if m.providerType == "deepseek" {
			switch effort {
			case "minimal", "low":
				effort = "low"
			case "medium", "high", "xhigh":
				effort = "high"
			}
		}
		body["reasoning"] = map[string]any{"effort": effort}
	}
	if m.providerType == "ark" {
		if key, ok := stringParam(m.params, "prompt_cache_key"); ok {
			body["prompt_cache_key"] = key
		}
	}
	if options.Tools != nil {
		infos := []any{}
		for _, info := range options.Tools {
			parameters, err := info.ToJSONSchema()
			if err != nil {
				return nil, err
			}
			var params any = parameters
			if parameters == nil {
				params = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			infos = append(infos, map[string]any{"type": "function", "name": info.Name, "description": info.Desc, "parameters": params, "strict": false})
		}
		body["tools"] = infos
	}
	if options.ToolChoice != nil {
		choice := string(*options.ToolChoice)
		if choice == "forced" {
			choice = "required"
		}
		body["tool_choice"] = choice
	}
	if len(options.AllowedToolNames) > 0 {
		if len(options.AllowedToolNames) != 1 {
			return nil, errors.New("provider: multiple allowed tool names are unsupported")
		}
		body["tool_choice"] = map[string]any{"type": "function", "name": options.AllowedToolNames[0]}
	}
	return body, nil
}

func (m *responsesModel) start(ctx context.Context, messages []*schema.Message, stream bool, opts ...model.Option) (*http.Response, error) {
	for attempt := 0; attempt < 2; attempt++ {
		input, files, err := m.input(ctx, messages)
		if err != nil {
			return nil, err
		}
		body, err := m.payload(input, stream, opts...)
		if err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		resp, err := m.request(ctx, http.MethodPost, "/responses", bytes.NewReader(encoded), "application/json")
		if err == nil {
			return resp, nil
		}
		var apiErr *apiError
		if attempt > 0 || !errors.As(err, &apiErr) || (apiErr.status != 400 && apiErr.status != 404) {
			return nil, err
		}
		code := strings.ToLower(apiErr.code)
		detail := strings.ToLower(apiErr.message)
		fileError := code == "file_not_found" || code == "file_expired" || code == "invalid_file_id" || strings.Contains(code, "filenotfound") || strings.Contains(code, "fileexpired")
		detailFileError := strings.Contains(detail, "file") && (strings.Contains(detail, "expired") || strings.Contains(detail, "not found") || strings.Contains(detail, "does not exist"))
		if !fileError && !detailFileError {
			return nil, err
		}
		invalidated := false
		for id, fileID := range files {
			if !fileError && !strings.Contains(apiErr.message, fileID) {
				continue
			}
			if deleteErr := m.media.invalidate(ctx, m.scope(ctx), id, fileID); deleteErr != nil {
				return nil, deleteErr
			}
			invalidated = true
		}
		if !invalidated {
			return nil, err
		}
	}
	return nil, errors.New("provider: file recovery exhausted")
}

func (m *responsesModel) decode(ctx context.Context, body *responseBody) (*schema.Message, error) {
	if body.Status != "completed" && (body.Status != "incomplete" || body.Incomplete.Reason != "max_output_tokens") {
		return nil, fmt.Errorf("provider: response %s (%s): %s", body.Status, body.Error.Code, body.Error.Message)
	}
	message := &schema.Message{Role: schema.Assistant}
	for index, raw := range body.Output {
		var item responseItem
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, err
		}
		switch item.Type {
		case "message":
			for _, part := range item.Content {
				if part.Type == "output_text" {
					message.Content += part.Text
				}
				if part.Type == "refusal" {
					message.Content += part.Refusal
				}
			}
		case "reasoning":
			for _, part := range item.Content {
				message.ReasoningContent += part.Text
			}
			if len(item.Content) == 0 {
				for _, part := range item.Summary {
					message.ReasoningContent += part.Text
				}
			}
		case "function_call":
			if item.CallID == "" || item.Name == "" {
				return nil, errors.New("provider: malformed function call")
			}
			message.ToolCalls = append(message.ToolCalls, schema.ToolCall{Index: &index, ID: item.CallID, Type: "function", Function: schema.FunctionCall{Name: item.Name, Arguments: item.Arguments}})
		default:
			return nil, fmt.Errorf("provider: unsupported response item %s", item.Type)
		}
	}
	replay, err := json.Marshal(responseReplay{Scope: m.scope(ctx), Model: m.modelCode, Output: body.Output})
	if err != nil {
		return nil, err
	}
	message.Extra = map[string]any{ReplayKey: string(replay)}
	finish := "stop"
	if len(message.ToolCalls) > 0 {
		finish = "tool_calls"
	}
	if body.Status == "incomplete" {
		finish = "length"
	}
	message.ResponseMeta = &schema.ResponseMeta{FinishReason: finish, Usage: &schema.TokenUsage{PromptTokens: body.Usage.Input, CompletionTokens: body.Usage.Output, TotalTokens: body.Usage.Total}}
	if cached := body.Usage.InputDetails.Cached; cached != nil {
		usageSinkFrom(ctx).add(CapturedUsage{CacheRead: *cached, Reported: true})
	}
	return message, nil
}

func (m *responsesModel) Generate(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	resp, err := m.start(ctx, messages, false, opts...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var body responseBody
	if err = json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(&body); err != nil {
		return nil, err
	}
	return m.decode(ctx, &body)
}

func (m *responsesModel) Stream(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	resp, err := m.start(ctx, messages, true, opts...)
	if err != nil {
		return nil, err
	}
	reader, writer := schema.Pipe[*schema.Message](1)
	go func() {
		defer writer.Close()
		defer func() { _ = resp.Body.Close() }()
		err := m.readStream(ctx, resp.Body, func(message *schema.Message) bool { return writer.Send(message, nil) })
		if err != nil {
			writer.Send(nil, err)
		}
	}()
	return reader, nil
}

func (m *responsesModel) readStream(ctx context.Context, body io.Reader, send func(*schema.Message) bool) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 4096), 16<<20)
	var text, reasoning string
	calls := map[int]schema.ToolCall{}
	var data []string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			continue
		}
		if line != "" || len(data) == 0 {
			continue
		}
		payload := strings.Join(data, "\n")
		data = nil
		var event struct {
			Type        string       `json:"type"`
			Delta       string       `json:"delta"`
			OutputIndex int          `json:"output_index"`
			Item        responseItem `json:"item"`
			Response    responseBody `json:"response"`
		}
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			return fmt.Errorf("provider: malformed Responses SSE: %w", err)
		}
		chunk := &schema.Message{Role: schema.Assistant}
		switch event.Type {
		case "response.output_text.delta", "response.refusal.delta":
			chunk.Content = event.Delta
			text += event.Delta
		case "response.reasoning_text.delta", "response.reasoning_summary_text.delta":
			chunk.ReasoningContent = event.Delta
			reasoning += event.Delta
		case "response.output_item.added":
			if event.Item.Type != "function_call" {
				continue
			}
			call := schema.ToolCall{Index: &event.OutputIndex, ID: event.Item.CallID, Type: "function", Function: schema.FunctionCall{Name: event.Item.Name, Arguments: event.Item.Arguments}}
			calls[event.OutputIndex] = call
			chunk.ToolCalls = []schema.ToolCall{call}
		case "response.function_call_arguments.delta":
			call, ok := calls[event.OutputIndex]
			if !ok {
				return errors.New("provider: function delta without call")
			}
			call.Function.Arguments += event.Delta
			calls[event.OutputIndex] = call
			chunk.ToolCalls = []schema.ToolCall{{Index: &event.OutputIndex, Type: "function", Function: schema.FunctionCall{Arguments: event.Delta}}}
		case "response.completed", "response.incomplete":
			final, err := m.decode(ctx, &event.Response)
			if err != nil {
				return err
			}
			if !strings.HasPrefix(final.Content, text) || !strings.HasPrefix(final.ReasoningContent, reasoning) {
				return errors.New("provider: inconsistent Responses stream")
			}
			final.Content = strings.TrimPrefix(final.Content, text)
			final.ReasoningContent = strings.TrimPrefix(final.ReasoningContent, reasoning)
			for index := range final.ToolCalls {
				call := &final.ToolCalls[index]
				if previous, ok := calls[*call.Index]; ok {
					if previous.ID != call.ID || !strings.HasPrefix(call.Function.Arguments, previous.Function.Arguments) {
						return errors.New("provider: inconsistent streamed tool call")
					}
					call.ID = ""
					call.Function.Name = ""
					call.Function.Arguments = strings.TrimPrefix(call.Function.Arguments, previous.Function.Arguments)
				}
			}
			send(final)
			return nil
		case "response.failed", "error":
			return errors.New("provider: Responses stream failed")
		default:
			continue
		}
		if send(chunk) {
			return context.Canceled
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return io.ErrUnexpectedEOF
}
