import { useEffect, useRef, useState } from 'react';
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
  state: 'talking' | 'idle' | 'thinking' | 'activated';
  volume: number;
  config: ConfigType;
  participantCount?: number;
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

const ThinkingView = ({ config }: { config: ConfigType }) => {
  const [direction, setDirection] = useState<'left' | 'right'>('right');
  const [lowerBound, setLowerBound] = useState(config.thinkingStartRange.start);
  const [upperBound, setUpperBound] = useState(config.thinkingStartRange.end);

  const adjustBounds = (
    bound: number,
    targetBound: number,
    direction: 'left' | 'right',
    elapsed: number,
  ) => {
    const { thinkingSpeed } = config;
    const isAtBounds = direction === 'right' ? bound <= targetBound : bound >= targetBound;

    const adjustedSpeed = elapsed * thinkingSpeed;

    return isAtBounds ? bound + (direction === 'right' ? adjustedSpeed : -adjustedSpeed) : bound;
  };

  const lastFrameTime = useRef(performance.now());

  useEffect(() => {
    const frame = requestAnimationFrame(() => {
      const { thinkingStartRange, thinkingTargetRange } = config;

      const now = performance.now();
      const elapsed = now - lastFrameTime.current;
      lastFrameTime.current = now;

      const newLowerBound = adjustBounds(
        lowerBound,
        direction === 'right' ? thinkingTargetRange.start : thinkingStartRange.start,
        direction,
        elapsed,
      );
      const newUpperBound = adjustBounds(
        upperBound,
        direction === 'right' ? thinkingTargetRange.end : thinkingStartRange.end,
        direction,
        elapsed,
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
      gap={config.barGap}
      alignItems="center"
      transition="opacity 0.5s ease-out"
    >
      {config.barCounts.map((_, idx) => {
        const visible = idx <= upperBound && idx >= lowerBound;
        return (
          <Box
            key={'thinking-item' + idx}
            width={config.columnWidth}
            height={config.barHeight}
            backgroundColor={visible ? config.backgroundColor : 'transparent'}
            boxShadow={visible ? config.boxShadow : 'none'}
            transition="background-color 0.5s ease-out"
          />
        );
      })}
    </Flex>
  );
};

const PulsingLED = ({
  delay,
  duration,
  targetOpacity,
  config,
}: {
  delay: number;
  duration: number;
  targetOpacity: number;
  config: ConfigType;
}) => {
  const [isOn, setIsOn] = useState(false);
  const [hadFirstPulse, setHadFirstPulse] = useState(false);
  useEffect(() => {
    if (isOn) {
      return;
    }

    const timeout = setTimeout(
      () => {
        setIsOn(true);
      },
      hadFirstPulse ? duration * 1000 + 200 : 50,
    );

    return () => clearTimeout(timeout);
  }, [isOn, hadFirstPulse]);

  useEffect(() => {
    if (!isOn) {
      return;
    }

    const timeout = setTimeout(() => {
      setIsOn(false);
      setHadFirstPulse(true);
    }, duration * 1000 + 200);

    return () => {
      clearTimeout(timeout);
    };
  }, [isOn]);
  return (
    <div
      style={{
        width: config.columnWidth,
        height: config.barHeight,
        backgroundColor: config.backgroundColor,
        transition: `opacity ${duration}s ${delay}s ease-in-out, box-shadow ${duration}s ${delay}s ease-in-out`,
        opacity: isOn ? targetOpacity : 0.0,
        boxShadow: isOn ? config.boxShadow : 'none',
      }}
    ></div>
  );
};

type AnimationValuesAtIndex = (
  index: number,
  count: number,
) => { targetOpacity: number; delay: number };

const IdleAndActivatedView = ({
  config,
  animationValuesForIndex,
  duration,
}: {
  config: ConfigType;
  animationValuesForIndex: AnimationValuesAtIndex;
  duration: number;
}) => {
  return (
    <div style={{ display: 'flex', gap: config.barGap }}>
      {config.barCounts.map((key, idx) => {
        const values = animationValuesForIndex(idx, config.barCounts.length);
        return (
          <PulsingLED
            key={key}
            config={config}
            duration={duration}
            targetOpacity={values.targetOpacity}
            delay={values.delay}
          />
        );
      })}
    </div>
  );
};

const HelperView = () => {
  return (
    <Box
      position="absolute"
      py="1rem"
      fontSize="0.75rem"
      color="rgba(255, 255, 255, 0.8)"
      lineHeight="1.5em"
      top="100%"
      textAlign="center"
    >
      Say &ldquo;Hey KITT&rdquo; to ask me a question.
    </Box>
  );
};

export const AIVisualizer = (props: SpeakerViewProps) => {
  const [hasBeenActivated, setHasBeenActivated] = useState(false);
  const [showHelperView, setShowHelperView] = useState(false);
  const activatedAnimationValues: AnimationValuesAtIndex = (index, count) => {
    return {
      targetOpacity:
        1 - (Math.abs(index - Math.floor(count / 2)) / Math.floor(count / 2.0)) * 0.75 + 0.25,
      delay: Math.abs(index - Math.floor(count / 2)) * 0.1,
    };
  };

  const idleAnimationValues: AnimationValuesAtIndex = (index, count) => {
    const center = Math.floor(count / 2.0);
    return {
      targetOpacity: index === center ? 1 : 0.1,
      delay: 0,
    };
  };

  useEffect(() => {
    if (hasBeenActivated || !props.participantCount) {
      return;
    }

    // Show helper view when there are multiple participants
    // but KITT hasn't been activated
    if (props.state === 'idle' && props.participantCount > 2 && !hasBeenActivated) {
      setShowHelperView(true);
    }

    if (props.participantCount > 2 && props.state !== 'idle') {
      setHasBeenActivated(true);
      setShowHelperView(false);
    }
  }, [props.state, props.participantCount, hasBeenActivated, showHelperView]);

  let stateView;
  if (props.state === 'thinking') {
    stateView = <ThinkingView config={props.config} />;
  } else if (props.state === 'activated') {
    stateView = (
      <IdleAndActivatedView
        key="active-view"
        config={props.config}
        animationValuesForIndex={activatedAnimationValues}
        duration={0.5}
      />
    );
  } else if (props.state === 'idle') {
    stateView = (
      <IdleAndActivatedView
        key="active-view"
        config={props.config}
        animationValuesForIndex={idleAnimationValues}
        duration={1}
      />
    );
  }

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
        {stateView}
      </Box>
      {showHelperView ? <HelperView /> : null}
    </Box>
  );
};
