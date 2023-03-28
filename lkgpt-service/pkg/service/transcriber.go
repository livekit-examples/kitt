package service

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	speech "cloud.google.com/go/speech/apiv1"
	"cloud.google.com/go/speech/apiv1/speechpb"
	"github.com/livekit/server-sdk-go/pkg/samplebuilder"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v3"

	"github.com/pion/webrtc/v3/pkg/media/oggwriter"
)

// To achieve endless streaming speech recognition, we need to split the transcribe requests to GCP,
// otherwise, the duration is limited to ~5 minutes.
// https://cloud.google.com/speech-to-text/docs/endless-streaming-tutorial
//
// When the speech stream is available:
// 1. Listen to RTP packets
// 2. Sample opus packets from the RTP packets
// 3. Write the opus packets to an OGG container
// 4. Send the OGG data to GCP
//
// Otherwise:
// 1. Try to recreate a new speech stream
// 2. Wait 4.5 minutes and close the stream

const MaxSpeechStreamDuration = 4 * time.Minute

type Transcriber struct {
	track        *webrtc.TrackRemote
	speechClient *speech.Client

	onTranscription func(resp *speechpb.StreamingRecognizeResponse)
	lock            sync.Mutex

	doneChan   chan struct{}
	closedChan chan struct{}
}

func NewTranscriber(track *webrtc.TrackRemote, speechClient *speech.Client) (*Transcriber, error) {
	rtpCodec := track.Codec()

	if !strings.EqualFold(rtpCodec.MimeType, "audio/opus") {
		return nil, errors.New("only opus is supported")
	}

	return &Transcriber{
		track:        track,
		speechClient: speechClient,
		doneChan:     make(chan struct{}),
		closedChan:   make(chan struct{}),
	}, nil
}

func (t *Transcriber) OnTranscriptionReceived(f func(resp *speechpb.StreamingRecognizeResponse)) {
	t.lock.Lock()
	t.onTranscription = f
	t.lock.Unlock()
}

func (t *Transcriber) Start() error {

	go func() {
		// Create a new stream each 4 minutes
	loop:
		for {
			pr, pw := io.Pipe()
			sb := samplebuilder.New(200, &codecs.OpusPacket{}, t.track.Codec().ClockRate)
			oggReader, bpw := bufio.NewReader(pr), bufio.NewWriter(pw)
			oggWriter, err := oggwriter.NewWith(bpw, t.track.Codec().ClockRate, t.track.Codec().Channels)

			if err != nil {
				fmt.Printf("failed to create a new ogg writer %v", err)
				return
			}

			speech, err := newSpeechStream(context.Background(), t.speechClient, t.track.Codec())
			if err != nil {
				fmt.Printf("failed to create a new speech stream %v", err)
				return
			}

			cycleChan := make(chan struct{}) // closed when the stream is done
			go func() {
				closeChan := make(chan struct{})
				wg := sync.WaitGroup{}
				wg.Add(3)

				go func() {
					if err := t.readTrack(closeChan, &wg, sb, oggWriter); err != nil {
						fmt.Printf("failed to read track %v", err)
					}
				}()

				go func() {
					if err := t.writeStream(closeChan, &wg, speech, oggReader); err != nil {
						fmt.Printf("failed to write stream %v", err)
					}
				}()

				utteranceChan := make(chan struct{})
				go func() {
					if err := t.readStream(closeChan, utteranceChan, &wg, speech); err != nil {
						fmt.Printf("failed to read stream %v", err)
					}
				}()

				select {
				case <-t.doneChan:
				case <-time.After(MaxSpeechStreamDuration):
				case <-utteranceChan:
					close(closeChan)
				}

				// Not really clean, the above goroutines don't really stop when closeChan is closed, so we need to close the pipe here
				pr.Close()
				pw.Close()
				oggWriter.Close()

				wg.Wait()
				close(cycleChan)
			}()

			select {
			case <-cycleChan:
				break
			case <-t.doneChan:
				break loop
			}
		}

		close(t.closedChan)
	}()

	<-t.doneChan
	return nil
}

func (t *Transcriber) Stop() {
	close(t.doneChan)
	<-t.closedChan
}

// Read the RTP packets from the track
// Create opus samples and put them inside an ogg container
func (t *Transcriber) readTrack(closeChan chan struct{}, wg *sync.WaitGroup, sb *samplebuilder.SampleBuilder, oggWriter *oggwriter.OggWriter) error {
	defer wg.Done()

	for {
		select {
		case <-closeChan:
			return nil
		default:
			pkt, _, err := t.track.ReadRTP()
			if err != nil {
				fmt.Printf("failed to read track %v", err)
			}

			sb.Push(pkt)

			for _, p := range sb.PopPackets() {
				oggWriter.WriteRTP(p)
			}
		}
	}
}

// Forward the ogg data to Speech To Text API
func (t *Transcriber) writeStream(closeChan chan struct{}, wg *sync.WaitGroup, speech speechpb.Speech_StreamingRecognizeClient, oggReader *bufio.Reader) error {
	defer wg.Done()

	buf := make([]byte, 1024)
	for {
		select {
		case <-closeChan:
			return nil
		default:
			n, err := oggReader.Read(buf)
			if err != nil {
				if err == io.ErrClosedPipe {
					return nil
				}
				return err
			}
			if n > 0 {
				if err := speech.Send(&speechpb.StreamingRecognizeRequest{
					StreamingRequest: &speechpb.StreamingRecognizeRequest_AudioContent{
						AudioContent: buf[:n],
					},
				}); err != nil {
					return err
				}

			}
		}
	}
}

// Read the responses from Google
// It includes estimation with the stability score and the final result
func (t *Transcriber) readStream(closeChan chan struct{}, utterance chan struct{}, wg *sync.WaitGroup, speech speechpb.Speech_StreamingRecognizeClient) error {
	defer wg.Done()

	for {
		select {
		case <-closeChan:
			return nil
		default:
			resp, err := speech.Recv()
			if err != nil {
				return err
			}

			fmt.Printf("received transcription results: %v", resp)

			t.lock.Lock()
			onTranscription := t.onTranscription
			t.lock.Unlock()

			if onTranscription != nil {
				onTranscription(resp)
			}

			if resp.SpeechEventType == speechpb.StreamingRecognizeResponse_END_OF_SINGLE_UTTERANCE {
				utterance <- struct{}{}
			}
		}
	}
}

func newSpeechStream(ctx context.Context, speechClient *speech.Client, rtpCodec webrtc.RTPCodecParameters) (speechpb.Speech_StreamingRecognizeClient, error) {
	stream, err := speechClient.StreamingRecognize(ctx)
	if err != nil {
		return nil, err
	}

	// Send the initial configuration message.
	if err := stream.Send(&speechpb.StreamingRecognizeRequest{
		StreamingRequest: &speechpb.StreamingRecognizeRequest_StreamingConfig{
			StreamingConfig: &speechpb.StreamingRecognitionConfig{
				SingleUtterance: true,
				InterimResults:  true,
				Config: &speechpb.RecognitionConfig{
					Model:             "command_and_search",
					UseEnhanced:       true,
					Encoding:          speechpb.RecognitionConfig_OGG_OPUS,
					SampleRateHertz:   int32(rtpCodec.ClockRate),
					AudioChannelCount: int32(rtpCodec.Channels),
					LanguageCode:      "fr-FR", // TODO(theomonnom): Support multiple languages
				},
			},
		},
	}); err != nil {
		return nil, err
	}

	return stream, nil
}
