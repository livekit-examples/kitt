package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

var (
	ErrAlreadyUsed = errors.New("chat completion already used")
)

type ChatCompletion struct {
	client *openai.Client
}

func NewChatCompletion(client *openai.Client) *ChatCompletion {
	return &ChatCompletion{
		client: client,
	}
}

func (c *ChatCompletion) Complete(ctx context.Context, history []*Sentence, prompt string) (*ChatStream, error) {
	var sb strings.Builder
	for _, s := range history {
		sb.WriteString(s.Name)
		sb.WriteString(": ")
		sb.WriteString(s.Transcript)
		sb.WriteString("\n")
	}
	conversation := sb.String()

	stream, err := c.client.CreateChatCompletionStream(ctx, openai.ChatCompletionRequest{
		Model: openai.GPT3Dot5Turbo,
		Messages: []openai.ChatCompletionMessage{
			{
				Role: openai.ChatMessageRoleSystem,
				Content: "You are a voice assistant named GPT." +
					"Answer with multiple small/medium sentences with the right punctuation. Only use dot (.) to end a sentence" +
					"Here is the current conversation, the name of the user is prefixed to each message." +
					"Answer the user question.",
			},
			{
				Role:    openai.ChatMessageRoleSystem,
				Content: conversation,
			},
			{
				Role:    openai.ChatMessageRoleUser,
				Content: prompt,
			},
		},
		Stream: true,
	})

	if err != nil {
		fmt.Println("error creating chat completion stream:", err)
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

func (c *ChatStream) Read() (string, error) {
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
