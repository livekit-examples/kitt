import {
  AudioTrack,
  ConnectionQualityIndicator,
  ParticipantContextIfNeeded,
  ParticipantName,
  TrackMutedIndicator,
  useEnsureParticipant,
  useParticipantTile,
} from '@livekit/components-react';
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

export const defaultConfig: ConfigType = {
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
};

interface PlaybookType {
  delayToNext: number;
  state: 'talking' | 'idle' | 'thinking';
  volume: number;
}

export let playbook: PlaybookType[] = [];

const getRandomDelay = () => {
  const minDelayBetweenSteps = 0.04;
  const maxDelayBetweenSteps = 0.08;
  return (maxDelayBetweenSteps - minDelayBetweenSteps) * Math.random() + minDelayBetweenSteps;
};

const populatePlaybook = () => {
  const count = 50;
  playbook.push({ state: 'idle', volume: 0, delayToNext: 2 });
  playbook.push({ state: 'thinking', volume: 0, delayToNext: 5 });

  for (let i = 0; i < count; i++) {
    playbook.push({ state: 'talking', volume: Math.random(), delayToNext: getRandomDelay() });
  }

  playbook.push({ state: 'thinking', volume: 0, delayToNext: 5 });

  for (let i = 0; i < count; i++) {
    playbook.push({ state: 'talking', volume: Math.random(), delayToNext: getRandomDelay() });
  }

  playbook.push({ state: 'idle', volume: 0, delayToNext: 5 });
  playbook.push({ state: 'thinking', volume: 0, delayToNext: 5 });

  for (let i = 0; i < count; i++) {
    playbook.push({ state: 'talking', volume: Math.random(), delayToNext: getRandomDelay() });
  }

  for (let i = 0; i < count; i++) {
    playbook.push({ state: 'talking', volume: Math.random(), delayToNext: getRandomDelay() });
  }
};

populatePlaybook();

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

export type GPTTileProps = React.HTMLAttributes<HTMLDivElement> & {
  disableSpeakingIndicator?: boolean;
  participant?: Participant;
  source?: Track.Source;
  publication?: TrackPublication;
};

export const GPTTile = ({
  participant,
  children,
  source = Track.Source.Camera,
  publication,
  disableSpeakingIndicator,
  ...htmlProps
}: GPTTileProps) => {
  const p = useEnsureParticipant(participant);

  const { elementProps } = useParticipantTile({
    participant: p,
    htmlProps,
    source,
    publication,
    disableSpeakingIndicator,
  });

  const [playbookIndex, setPlaybookIndex] = useState(0);
  useEffect(() => {
    const timeout = setTimeout(() => {
      if (playbookIndex === playbook.length - 1) {
        setPlaybookIndex(0);
      } else {
        setPlaybookIndex(playbookIndex + 1);
      }
    }, playbook[playbookIndex].delayToNext * 1000);

    return () => {
      clearInterval(timeout);
    };
  }, [playbookIndex]);

  return (
    <div style={{ position: 'relative' }} {...elementProps}>
      <ParticipantContextIfNeeded participant={p}>
        <AudioTrack source={source} publication={publication} participant={participant} />
        <Box h="100%" bgColor="#000" display="flex" alignItems="center" justifyContent="center">
          <AIVisualizer
            state={playbook[playbookIndex].state}
            volume={playbook[playbookIndex].volume}
            config={{ ...defaultConfig }}
          />
        </Box>
        <div className="lk-participant-metadata">
          <div className="lk-participant-metadata-item">
            <TrackMutedIndicator
              source={Track.Source.Microphone}
              show={'muted'}
            ></TrackMutedIndicator>
            <ParticipantName />
          </div>
          <ConnectionQualityIndicator className="lk-participant-metadata-item" />
        </div>
      </ParticipantContextIfNeeded>
    </div>
  );
};
