import {
  ConnectionQualityIndicator,
  ParticipantContextIfNeeded,
  ParticipantName,
  TrackMutedIndicator,
  useDataChannel,
  useEnsureParticipant,
  useMediaTrack,
  useParticipants,
  useParticipantTile,
} from '@livekit/components-react';
import { Participant, Track } from 'livekit-client';
import React, { useCallback } from 'react';
import { useEffect } from 'react';
import { Box } from '@chakra-ui/react';
import { GPTState, Packet, PacketType, StatePacket } from '../lib/packet';
import { AIVisualizer } from './AIVisualizer';
import type { ReceivedDataMessage } from '@livekit/components-core';

export type GPTTileProps = React.HTMLAttributes<HTMLDivElement> & {
  participant?: Participant;
};

const decoder = new TextDecoder();

export const GPTTile = ({ participant, ...htmlProps }: GPTTileProps) => {
  const participants = useParticipants();
  const [volume, setVolume] = React.useState(0);
  const [state, setState] = React.useState<GPTState>(GPTState.Idle);
  const activateSoundRef = React.useRef<HTMLAudioElement>(null);
  const p = useEnsureParticipant(participant);

  const onData = useCallback((message: ReceivedDataMessage) => {
    const packet = JSON.parse(decoder.decode(message.payload)) as Packet;

    if (packet.type == PacketType.State) {
      const statePacket = packet.data as StatePacket;
      setState(statePacket.state);

      if (statePacket.state == GPTState.Active && participants.length > 2)
        activateSoundRef.current?.play();
    }
  }, []);

  useDataChannel(undefined, onData);

  const tile = useParticipantTile({
    participant: p,
    htmlProps,
    source: Track.Source.Microphone,
  });

  const audio = React.useRef<HTMLAudioElement>(null);
  const track = useMediaTrack(Track.Source.Microphone, p, { element: audio });

  useEffect(() => {
    if (!track.track?.mediaStream) {
      return;
    }

    const ctx = new AudioContext();
    const source = ctx.createMediaStreamSource(track.track?.mediaStream);
    const analyser = ctx.createAnalyser();
    analyser.fftSize = 32;
    analyser.smoothingTimeConstant = 0;
    source.connect(analyser);

    const bufferLength = analyser.frequencyBinCount;
    const dataArray = new Uint8Array(bufferLength);

    const updateVolume = () => {
      analyser.getByteFrequencyData(dataArray);
      let sum = 0;
      for (const a of dataArray) sum += a * a;
      setVolume(Math.sqrt(sum / dataArray.length) / 255);
    };

    const interval = setInterval(updateVolume, 1000 / 30);

    return () => {
      source.disconnect();
      clearInterval(interval);
    };
  }, [track.track?.mediaStream]);

  const aiState: Record<string, 'idle' | 'thinking' | 'talking' | 'activated'> = {
    [GPTState.Idle]: 'idle',
    [GPTState.Loading]: 'thinking',
    [GPTState.Speaking]: 'talking',
    [GPTState.Active]: 'activated',
  };

  return (
    <div style={{ position: 'relative' }} {...tile.elementProps}>
      <audio ref={activateSoundRef} src="/sfx/activate.wav" />
      <ParticipantContextIfNeeded participant={p}>
        <audio ref={audio} {...track.elementProps}></audio>
        <Box h="100%" bgColor="#000" display="flex" alignItems="center" justifyContent="center">
          <AIVisualizer
            state={aiState[state]}
            volume={volume}
            participantCount={participants.length}
            config={{
              columnWidth: '2.25rem',
              barHeight: '0.375rem',
              barCounts: [3, 7, 11, 7, 3],
              barGap: '0.375rem',
              backgroundColor: '#FF6352',
              inactiveBackgroundColor: 'rgba(255, 255, 255, 0.06)',
              boxShadow: '0px 0px 10px #E64938',
              thinkingStartRange: { start: -4, end: 0 },
              thinkingTargetRange: { start: 4, end: 8 },
              thinkingSpeed: 0.015,
            }}
          />
        </Box>
        <div className="lk-participant-metadata">
          <div className="lk-participant-metadata-item">
            <TrackMutedIndicator source={Track.Source.Microphone} show={'muted'} />
            <ParticipantName />
          </div>
          <ConnectionQualityIndicator className="lk-participant-metadata-item" />
        </div>
      </ParticipantContextIfNeeded>
    </div>
  );
};
