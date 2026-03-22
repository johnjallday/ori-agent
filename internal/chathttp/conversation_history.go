package chathttp

import (
	"fmt"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/param"

	"github.com/johnjallday/ori-agent/internal/llm"
)

func (h *Handler) buildChatSystemPrompt(
	ag *resolvedChatAgent,
	agentName string,
	defaultPrompt string,
	tools []llm.Tool,
) string {
	systemPrompt := h.buildSystemPromptWithSkills(ag, agentName, defaultPrompt)
	if len(tools) == 0 {
		return systemPrompt
	}

	toolNames := make([]string, 0, len(tools))
	seen := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		name := strings.TrimSpace(tool.Name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		toolNames = append(toolNames, name)
	}
	if len(toolNames) == 0 {
		return systemPrompt
	}
	return systemPrompt + " Available tools: " + strings.Join(toolNames, ", ") + "."
}

func prependOpenAISystemPrompt(
	messages []openai.ChatCompletionMessageParamUnion,
	systemPrompt string,
) []openai.ChatCompletionMessageParamUnion {
	trimmed := strings.TrimSpace(systemPrompt)
	if trimmed == "" {
		return messages
	}

	withSystem := make([]openai.ChatCompletionMessageParamUnion, 0, len(messages)+1)
	withSystem = append(withSystem, openai.SystemMessage(trimmed))
	withSystem = append(withSystem, messages...)
	return withSystem
}

func buildOpenAIConversationMessages(
	history []openai.ChatCompletionMessageParamUnion,
	userMessage string,
	images []llm.ImageAttachment,
) []openai.ChatCompletionMessageParamUnion {
	messages := append([]openai.ChatCompletionMessageParamUnion{}, history...)
	messages = append(messages, buildOpenAIUserMessage(userMessage, images))
	return messages
}

func buildOpenAIUserMessage(userMessage string, images []llm.ImageAttachment) openai.ChatCompletionMessageParamUnion {
	if len(images) == 0 {
		return openai.UserMessage(userMessage)
	}

	contentParts := make([]openai.ChatCompletionContentPartUnionParam, 0, len(images)+1)
	contentParts = append(contentParts, openai.TextContentPart(userMessage))
	for _, img := range images {
		dataURL := fmt.Sprintf("data:%s;base64,%s", img.MimeType, img.Base64Data)
		contentParts = append(contentParts, openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{
			URL:    dataURL,
			Detail: "auto",
		}))
	}
	return openai.UserMessage(contentParts)
}

func buildLLMConversationMessages(
	history []openai.ChatCompletionMessageParamUnion,
	userMessage string,
	images []llm.ImageAttachment,
) []llm.Message {
	messages := make([]llm.Message, 0, len(history)+1)

	for _, msg := range history {
		switch {
		case msg.OfSystem != nil:
			if content := systemMessageText(msg.OfSystem); strings.TrimSpace(content) != "" {
				messages = append(messages, llm.NewSystemMessage(content))
			}
		case msg.OfUser != nil:
			if content := userMessageText(msg.OfUser); strings.TrimSpace(content) != "" {
				messages = append(messages, llm.NewUserMessage(content))
			}
		case msg.OfAssistant != nil:
			if content := assistantMessageText(msg.OfAssistant); strings.TrimSpace(content) != "" {
				messages = append(messages, llm.NewAssistantMessage(content))
			}
		case msg.OfTool != nil:
			content := toolMessageText(msg.OfTool)
			if strings.TrimSpace(content) == "" && strings.TrimSpace(msg.OfTool.ToolCallID) == "" {
				continue
			}
			messages = append(messages, llm.NewToolMessage(msg.OfTool.ToolCallID, content))
		}
	}

	if len(images) > 0 {
		messages = append(messages, llm.NewUserMessageWithImages(userMessage, images))
	} else {
		messages = append(messages, llm.NewUserMessage(userMessage))
	}

	return messages
}

func systemMessageText(msg *openai.ChatCompletionSystemMessageParam) string {
	if msg == nil {
		return ""
	}
	if !param.IsOmitted(msg.Content.OfString) {
		return msg.Content.OfString.Value
	}

	var parts []string
	for _, part := range msg.Content.OfArrayOfContentParts {
		if text := part.Text; strings.TrimSpace(text) != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func userMessageText(msg *openai.ChatCompletionUserMessageParam) string {
	if msg == nil {
		return ""
	}
	if !param.IsOmitted(msg.Content.OfString) {
		return msg.Content.OfString.Value
	}

	var parts []string
	for _, part := range msg.Content.OfArrayOfContentParts {
		if text := part.GetText(); text != nil && strings.TrimSpace(*text) != "" {
			parts = append(parts, *text)
		}
	}
	return strings.Join(parts, "\n")
}

func toolMessageText(msg *openai.ChatCompletionToolMessageParam) string {
	if msg == nil {
		return ""
	}
	if !param.IsOmitted(msg.Content.OfString) {
		return msg.Content.OfString.Value
	}

	var parts []string
	for _, part := range msg.Content.OfArrayOfContentParts {
		if strings.TrimSpace(part.Text) != "" {
			parts = append(parts, part.Text)
		}
	}
	return strings.Join(parts, "\n")
}
