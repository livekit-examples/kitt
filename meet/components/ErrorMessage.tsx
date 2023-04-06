import { Box } from "@chakra-ui/react";
import { useDataChannel } from "@livekit/components-react";
import { useEffect, useState } from "react";
import { ErrorPacket, Packet, PacketType } from "../lib/packet";

export const ErrorMessage = () => {
  const { message } = useDataChannel();
  const [visible, setVisible] = useState<boolean>(false);
  const [error, setError] = useState<string>('');

  useEffect(() => {
    if (!message) return;

    const decoder = new TextDecoder();
    const packet = JSON.parse(decoder.decode(message.payload)) as Packet;
    if (packet.type == PacketType.Error) {
      const errorPacket = packet.data as ErrorPacket;
      setError(errorPacket.message);
    }
  }, [message]);

  useEffect(() => {
    if (!error) return;

    setVisible(true);
    const timeout = setTimeout(() => {
      setVisible(false);
    }, 5000);

    return () => clearTimeout(timeout);
  }, [error]);

  return visible && error ? (
    <Box
      position="fixed"
      left="50%"
      textAlign="center"
      transform="translateX(-50%)"
      paddingX="4px"
      top="4rem"
      borderRadius="4px"
      bgColor="#A52A2A"
    >
      {error}
    </Box>
  ) : (
    <> </>
  );
};
