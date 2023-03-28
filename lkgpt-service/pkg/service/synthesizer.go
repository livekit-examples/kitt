package service

import (
	"context"

	texttospeech "cloud.google.com/go/texttospeech/apiv1"
	"cloud.google.com/go/texttospeech/apiv1/texttospeechpb"
)

type Synthesizer struct {
	client   *texttospeech.Client
	language string
}

func NewSynthesizer(client *texttospeech.Client, language string) *Synthesizer {
	return &Synthesizer{
		client:   client,
		language: language,
	}
}

func (s *Synthesizer) Synthesize(ctx context.Context, ssml string) (resp *texttospeechpb.SynthesizeSpeechResponse, err error) {
	req := &texttospeechpb.SynthesizeSpeechRequest{
		Input: &texttospeechpb.SynthesisInput{
			InputSource: &texttospeechpb.SynthesisInput_Ssml{
				Ssml: ssml,
			},
		},
		Voice: &texttospeechpb.VoiceSelectionParams{
			LanguageCode: s.language,
			SsmlGender:   texttospeechpb.SsmlVoiceGender_MALE,
		},
		AudioConfig: &texttospeechpb.AudioConfig{
			AudioEncoding:   texttospeechpb.AudioEncoding_OGG_OPUS,
			SampleRateHertz: 48000,
		},
	}

	resp, err = s.client.SynthesizeSpeech(ctx, req)
	return
}
