package service

import (
	"context"

	tts "cloud.google.com/go/texttospeech/apiv1"
	ttspb "cloud.google.com/go/texttospeech/apiv1/texttospeechpb"
)

type Synthesizer struct {
	client *tts.Client
}

func NewSynthesizer(client *tts.Client) *Synthesizer {
	return &Synthesizer{
		client: client,
	}
}

func (s *Synthesizer) Synthesize(ctx context.Context, text string, language *Language) (*ttspb.SynthesizeSpeechResponse, error) {
	req := &ttspb.SynthesizeSpeechRequest{
		Input: &ttspb.SynthesisInput{
			InputSource: &ttspb.SynthesisInput_Text{
				Text: text,
			},
		},
		Voice: &ttspb.VoiceSelectionParams{
			LanguageCode: language.Code,
			Name:         language.SynthesizerModel,
		},
		AudioConfig: &ttspb.AudioConfig{
			AudioEncoding:   ttspb.AudioEncoding_OGG_OPUS,
			SampleRateHertz: 48000,
		},
	}

	resp, err := s.client.SynthesizeSpeech(ctx, req)
	return resp, err
}
