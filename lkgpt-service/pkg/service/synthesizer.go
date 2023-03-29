package service

import (
	"context"

	tts "cloud.google.com/go/texttospeech/apiv1"
	ttspb "cloud.google.com/go/texttospeech/apiv1/texttospeechpb"
)

type Synthesizer struct {
	client   *tts.Client
	language string
}

func NewSynthesizer(client *tts.Client, language string) *Synthesizer {
	return &Synthesizer{
		client:   client,
		language: language,
	}
}

func (s *Synthesizer) Synthesize(ctx context.Context, ssml string) (resp *ttspb.SynthesizeSpeechResponse, err error) {
	req := &ttspb.SynthesizeSpeechRequest{
		Input: &ttspb.SynthesisInput{
			InputSource: &ttspb.SynthesisInput_Ssml{
				Ssml: ssml,
			},
		},
		Voice: &ttspb.VoiceSelectionParams{
			LanguageCode: s.language,
			SsmlGender:   ttspb.SsmlVoiceGender_MALE,
		},
		AudioConfig: &ttspb.AudioConfig{
			AudioEncoding:   ttspb.AudioEncoding_OGG_OPUS,
			SampleRateHertz: 48000,
		},
	}

	resp, err = s.client.SynthesizeSpeech(ctx, req)
	return
}
