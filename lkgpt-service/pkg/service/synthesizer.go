package service

import (
	"context"

	tts "cloud.google.com/go/texttospeech/apiv1"
	ttspb "cloud.google.com/go/texttospeech/apiv1/texttospeechpb"
)

type Synthesizer struct {
	client   *tts.Client
	language *Language
}

func NewSynthesizer(client *tts.Client, language *Language) *Synthesizer {
	return &Synthesizer{
		client:   client,
		language: language,
	}
}

func (s *Synthesizer) Synthesize(ctx context.Context, ssml string) (*ttspb.SynthesizeSpeechResponse, error) {
	req := &ttspb.SynthesizeSpeechRequest{
		Input: &ttspb.SynthesisInput{
			InputSource: &ttspb.SynthesisInput_Ssml{
				Ssml: ssml,
			},
		},
		Voice: &ttspb.VoiceSelectionParams{
			Name: s.language.SynthesizerModel,
		},
		AudioConfig: &ttspb.AudioConfig{
			AudioEncoding:   ttspb.AudioEncoding_OGG_OPUS,
			SampleRateHertz: 48000,
		},
	}

	resp, err := s.client.SynthesizeSpeech(ctx, req)
	return resp, err
}
