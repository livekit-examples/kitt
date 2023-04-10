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
			sb.WriteString(fmt.Sprintf("You (%s)", BotIdentity))
		} else {
			sb.WriteString(s.ParticipantName)
		}

		sb.WriteString(" said ")
		sb.WriteString(s.Text)
		sb.WriteString("\n\n")
	}

	var voiceSb strings.Builder
	for _, lang := range Languages {
		voiceSb.WriteString(fmt.Sprintf("<%s>, ", lang.Code))
	}

	conversation := sb.String()
	stream, err := c.client.CreateChatCompletionStream(ctx, openai.ChatCompletionRequest{
		Model: openai.GPT3Dot5Turbo,
		Messages: []openai.ChatCompletionMessage{
			{
				Role: openai.ChatMessageRoleSystem,
				Content: "You are a voice assistant in a meeting named KITT, make concise/short answers. " +
					fmt.Sprintf("Always answer in %s. ", language.Label) +
					"Always prepend the right language code before answering: " + voiceSb.String() +
					"e.g: <fr-FR> <sentence in French>",
			},
			{
				Role:    openai.ChatMessageRoleUser,
				Content: "Here is the history of the conversation we just had:\n" + conversation,
			},
			{
				Role:    openai.ChatMessageRoleSystem,
				Content: fmt.Sprintf("You are talking to %s", prompt.ParticipantName),
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
