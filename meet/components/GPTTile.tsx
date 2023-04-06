import {
  ConnectionQualityIndicator,
  ParticipantContextIfNeeded,
  ParticipantName,
  TrackMutedIndicator,
  useDataChannel,
  useEnsureParticipant,
  useMediaTrack,
  useParticipantTile,
} from '@livekit/components-react';
import { AudioSource } from '@livekit/components-core';
import { Participant, Track, TrackPublication } from 'livekit-client';
import React from 'react';
import { useEffect, useState } from 'react';
import { Box, Flex } from '@chakra-ui/react';

export interface ConfigType {
  columnWidth: string;
  barHeight: string;
  barCounts: number[];
  barGap: string;
  backgroundColor: string;
  inactiveBackgroundColor: string;
  boxShadow: string;
  thinkingStartRange: {
    start: number;
    end: number;
  };
  thinkingTargetRange: {
    start: number;
    end: number;
  };
  thinkingSpeed: number;
}

interface SpeakerViewProps {
  state: 'talking' | 'idle' | 'thinking';
  volume: number;
  config: ConfigType;
}

interface SoundColumnsProps {
  count: number;
  volume: number;
  config: ConfigType;
}

const SoundColumn = (props: SoundColumnsProps) => {
  const items = Array.from(Array(props.count).keys());
  return (
    <Box display="flex" flexDirection="column" gap={props.config.barGap}>
      {items.map((_, index) => {
        const center = Math.floor(props.count / 2.0);
        const distanceFromCenter = Math.abs(index - center);
        const maxVolumeDistance = Math.floor(props.volume * center);
        const isOff = distanceFromCenter > maxVolumeDistance || props.volume === 0;
        const backgroundColor = isOff
          ? props.config.inactiveBackgroundColor
          : props.config.backgroundColor;
        const percentageFromCenter = distanceFromCenter / center;
        return (
          <Box
            key={'row-' + index}
            width={props.config.columnWidth}
            height={props.config.barHeight}
            backgroundColor={backgroundColor}
            transition="background-color 0.1s ease-out"
            boxShadow={isOff ? 'none' : props.config.boxShadow}
            opacity={isOff ? 1 : 1 - percentageFromCenter}
          ></Box>
        );
      })}
    </Box>
  );
};

const SpeakingView = (props: SpeakerViewProps) => {
  let adjustedVolume = props.volume > 1 ? 1 : props.volume;
  adjustedVolume = props.volume < 0 ? 0 : props.volume;

  return (
    <Flex flexDirection="row" gap={props.config.barGap} alignItems="center">
      {props.config.barCounts.map((count, idx) => {
        return (
          <SoundColumn
            key={'sound-column-' + idx}
            count={count}
            volume={adjustedVolume}
            config={props.config}
          />
        );
      })}
    </Flex>
  );
};

const ThinkingView = (props: { config: ConfigType }) => {
  const [direction, setDirection] = useState<'left' | 'right'>('right');
  const [lowerBound, setLowerBound] = useState(props.config.thinkingStartRange.start);
  const [upperBound, setUpperBound] = useState(props.config.thinkingStartRange.end);

  const adjustBounds = (bound: number, targetBound: number, direction: 'left' | 'right') => {
    const { thinkingSpeed } = props.config;
    const isAtBounds = direction === 'right' ? bound <= targetBound : bound >= targetBound;

    return isAtBounds ? bound + (direction === 'right' ? thinkingSpeed : -thinkingSpeed) : bound;
  };

  useEffect(() => {
    const frame = requestAnimationFrame(() => {
      const { thinkingStartRange, thinkingTargetRange } = props.config;

      const newLowerBound = adjustBounds(
        lowerBound,
        direction === 'right' ? thinkingTargetRange.start : thinkingStartRange.start,
        direction,
      );
      const newUpperBound = adjustBounds(
        upperBound,
        direction === 'right' ? thinkingTargetRange.end : thinkingStartRange.end,
        direction,
      );

      setLowerBound(newLowerBound);
      setUpperBound(newUpperBound);

      const isAtLowerBounds = newLowerBound === lowerBound;
      const isAtHigherBounds = newUpperBound === upperBound;

      if (isAtHigherBounds && isAtLowerBounds) {
        setDirection(direction === 'right' ? 'left' : 'right');
      }
    });
    return () => cancelAnimationFrame(frame);
  }, [direction, lowerBound, upperBound]);

  return (
    <Flex
      flexDirection="row"
      gap={props.config.barGap}
      alignItems="center"
      transition="opacity 0.5s ease-out"
    >
      {props.config.barCounts.map((_, idx) => {
        const visible = idx <= upperBound && idx >= lowerBound;
        return (
          <Box
            key={'thinking-item' + idx}
            width={props.config.columnWidth}
            height={props.config.barHeight}
            backgroundColor={visible ? props.config.backgroundColor : 'transparent'}
            boxShadow={visible ? props.config.boxShadow : 'none'}
            transition="background-color 0.5s ease-out"
          />
        );
      })}
    </Flex>
  );
};

export const AIVisualizer = (props: SpeakerViewProps) => {
  return (
    <Box position="relative">
      <SpeakingView {...props} />
      <Box
        width="100%"
        height="100%"
        position="absolute"
        display="flex"
        flexDirection="column"
        alignItems="center"
        justifyContent="center"
        left={0}
        top={0}
      >
        {props.state === 'thinking' ? <ThinkingView config={props.config} /> : null}
      </Box>
    </Box>
  );
};

enum PacketType {
  Transcript = 0,
  State,
}

enum GPTState {
  Idle = 0,
  Loading,
  Speaking,
}

interface Packet {
  type: PacketType;
  data: TranscriptPacket | StatePacket;
}

interface TranscriptPacket {
  sid: string;
  name: string;
  transcript: string;
  isFinal: boolean;
}

interface StatePacket {
  state: GPTState;
}

export type GPTTileProps = React.HTMLAttributes<HTMLDivElement> & {
  participant?: Participant;
};

const decoder = new TextDecoder();
export const GPTTile = ({
  participant,
  ...htmlProps
}: GPTTileProps) => {
  const p = useEnsureParticipant(participant);
  const { message } = useDataChannel();

  const [volume, setVolume] = React.useState(0);
  const [state, setState] = React.useState<GPTState>(GPTState.Idle);

  useEffect(() => {
    if (!message) {
      return;
    }

    const packet = JSON.parse(decoder.decode(message.payload)) as Packet;

    if (packet.type == PacketType.State) {
      const statePacket = packet.data as StatePacket;
      setState(statePacket.state);
    }
  }, [message]);

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
    source.connect(analyser);

    const bufferLength = analyser.frequencyBinCount;
    const dataArray = new Uint8Array(bufferLength);

    const updateVolume = () => {
      analyser.getByteFrequencyData(dataArray);
      let sum = 0;
      for (const a of dataArray)
        sum += a * a;
      setVolume(Math.sqrt(sum / dataArray.length) / 255);
    };

    const interval = setInterval(updateVolume, 1000 / 30);

    return () => {
      source.disconnect();
      clearInterval(interval);
    }
  }, [track.track?.mediaStream]);

  return (
    <div style={{ position: 'relative' }} {...tile.elementProps}>
      <ParticipantContextIfNeeded participant={p}>
        <audio ref={audio} {...track.elementProps}></audio>
        <Box h="100%" bgColor="#000" display="flex" alignItems="center" justifyContent="center">
          <AIVisualizer
            state={state == GPTState.Loading ? 'thinking' : 'talking'}
            volume={volume}
            config={{
              columnWidth: '2.25rem',
              barHeight: '0.375rem',
              barCounts: [3, 7, 11, 7, 3],
              barGap: '0.375rem',
              backgroundColor: '#FF6352',
              inactiveBackgroundColor: 'rgba(255, 255, 255, 0.05)',
              boxShadow: '0px 0px 10px #E64938',
              thinkingStartRange: { start: -4, end: 0 },
              thinkingTargetRange: { start: 4, end: 8 },
              thinkingSpeed: 0.2,
            }}
          />
        </Box>
        <div className="lk-participant-metadata">
          <div className="lk-participant-metadata-item">
            <TrackMutedIndicator
              source={Track.Source.Microphone}
              show={'muted'}
            />
            <ParticipantName />
          </div>
          <ConnectionQualityIndicator className="lk-participant-metadata-item" />
        </div>
      </ParticipantContextIfNeeded>
    </div>
  );
};
