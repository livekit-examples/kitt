package service

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/livekit/protocol/logger"
	lksdk "github.com/livekit/server-sdk-go"
	openai "github.com/sashabaranov/go-openai"
)

// A sentence in the conversation (Used for the history)
type SpeechEvent struct {
	ParticipantName string
	IsBot           bool
	Text            string
}

type JoinLeaveEvent struct {
	Leave           bool
	ParticipantName string
	Time            time.Time
}

type MeetingEvent struct {
	Speech *SpeechEvent
	Join   *JoinLeaveEvent
}

type ChatCompletion struct {
	client *openai.Client
}

func NewChatCompletion(client *openai.Client) *ChatCompletion {
	return &ChatCompletion{
		client: client,
	}
}

func (c *ChatCompletion) Complete(ctx context.Context, events []*MeetingEvent, prompt *SpeechEvent,
	participant *lksdk.RemoteParticipant, room *lksdk.Room, language *Language) (*ChatStream, error) {

	var sb strings.Builder
	participants := room.GetParticipants()
	for i, participant := range participants {
		sb.WriteString(participant.Identity())
		if i != len(participants)-1 {
			sb.WriteString(", ")
		}
	}
	participantNames := sb.String()
	sb.Reset()

	messages := make([]openai.ChatCompletionMessage, 0, len(events)+3)
	messages = append(messages, openai.ChatCompletionMessage{
		Role: openai.ChatMessageRoleSystem,
		Content: "You are KITT, a voice assistant in a meeting created by LiveKit. " +
			"Keep your responses concise while still being friendly and personable. " +
			"If your response is a question, please append a question mark symbol to the end of it. " + // Used for auto-trigger
			fmt.Sprintf("There are actually %d participants in the meeting: %s. ", len(participants), participantNames) +
			fmt.Sprintf("Current language: %s Current date: %s", language.Label, time.Now().Format("January 2, 2006 3:04pm")),
	})

	for _, e := range events {
		if e.Speech != nil {
			if e.Speech.IsBot {
				messages = append(messages, openai.ChatCompletionMessage{
					Role:    openai.ChatMessageRoleAssistant,
					Content: e.Speech.Text,
					Name:    BotIdentity,
				})
			} else {
				messages = append(messages, openai.ChatCompletionMessage{
					Role:    openai.ChatMessageRoleUser,
					Content: fmt.Sprintf("%s said %s", e.Speech.ParticipantName, e.Speech.Text),
					Name:    e.Speech.ParticipantName,
				})
			}
		}

		if e.Join != nil {
			if e.Join.Leave {
				messages = append(messages, openai.ChatCompletionMessage{
					Role:    openai.ChatMessageRoleSystem,
					Content: fmt.Sprintf("%s left the meeting at %s", e.Join.ParticipantName, e.Join.Time.Format("3:04pm")),
				})
			} else {
				messages = append(messages, openai.ChatCompletionMessage{
					Role:    openai.ChatMessageRoleSystem,
					Content: fmt.Sprintf("%s joined the meeting at %s", e.Join.ParticipantName, e.Join.Time.Format("3:04pm")),
				})
			}
		}
	}

	messages = append(messages, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleSystem,
		Content: fmt.Sprintf("You are currently talking to %s", participant.Identity()),
	})

	// prompt
	messages = append(messages, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: prompt.Text,
		Name:    prompt.ParticipantName,
	})

	stream, err := c.client.CreateChatCompletionStream(ctx, openai.ChatCompletionRequest{
		Model:    openai.GPT3Dot5Turbo,
		Messages: messages,
		Stream:   true,
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

		if len(response.Choices) == 0 {
			continue
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
