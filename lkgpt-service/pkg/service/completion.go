package service

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/livekit/protocol/logger"
	openai "github.com/sashabaranov/go-openai"
)

type ChatCompletion struct {
	client *openai.Client
}

func NewChatCompletion(client *openai.Client) *ChatCompletion {
	return &ChatCompletion{
		client: client,
	}
}

func (c *ChatCompletion) Complete(ctx context.Context, history []*Sentence, prompt *Sentence, language *Language) (*ChatStream, error) {
	var sb strings.Builder
	for _, s := range history {
		if s.IsBot {
			sb.WriteString(fmt.Sprintf("You: %s", s.Text))
		} else {
			sb.WriteString(fmt.Sprintf("%s: %s", s.ParticipantName, s.Text))
		}
		sb.WriteString("\n\n")
	}

	conversation := sb.String()
	prePrompt := "You are a voice assistant in a meeting named KITT. Keep your responses concise while still being friendly and personable. If your response is a question, please append a question mark symbol to the end of it."
	
	stream, err := c.client.CreateChatCompletionStream(ctx, openai.ChatCompletionRequest{
		Model: openai.GPT3Dot5Turbo,
		Messages: []openai.ChatCompletionMessage{
			{
				Role: openai.ChatMessageRoleSystem,
				Content: prePrompt,
			},
			{
				Role:    openai.ChatMessageRoleSystem,
				Content: "Here is the history of the conversation we've had thus far:\n" + conversation,
			},
			{
				Role:    openai.ChatMessageRoleSystem,
				Content: fmt.Sprintf("You are talking to %s, the current language is %s (%s)", prompt.ParticipantName, language.Label, language.Code),
			},
			{
				Role:    openai.ChatMessageRoleUser,
				Content: prompt.Text,
			},
		},
		Stream: true,
	})

	if err != nil {
		logger.Errorw("error creating chat completion stream", err)
		return nil, err
	}

	return &ChatStream{
		stream: stream,
	}, nil
}

// Wrapper around openai.ChatCompletionStream to return only complete sentences
type ChatStream struct {
	stream *openai.ChatCompletionStream
}

func (c *ChatStream) Recv() (string, error) {
	sb := strings.Builder{}
	for {
		response, err := c.stream.Recv()
		if err != nil {
			content := sb.String()
			if err == io.EOF && len(strings.TrimSpace(content)) != 0 {
				return content, nil
			}
			return "", err
		}

		delta := response.Choices[0].Delta.Content
		sb.WriteString(delta)

		if strings.HasSuffix(strings.TrimSpace(delta), ".") {
			return sb.String(), nil
		}
	}
}

func (c *ChatStream) Close() {
	c.stream.Close()
}
