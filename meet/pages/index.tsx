import { useRouter } from 'next/router';
import React from 'react';

const Home = () => {
  const router = useRouter();

  React.useEffect(() => {
    // Not the best way to do it..
    // We got redirected here when coming from livekit.io/kitt
    // This repo should work without the livekit site
    router.push(`/rooms/${generateRoomId()}`);
  });

  return <></>;
};

export default Home;

function generateRoomId(): string {
  return `${randomString(4)}-${randomString(4)}`;
}

function randomString(length: number): string {
  let result = '';
  const characters = 'abcdefghijklmnopqrstuvwxyz0123456789';
  const charactersLength = characters.length;
  for (let i = 0; i < length; i++) {
    result += characters.charAt(Math.floor(Math.random() * charactersLength));
  }
  return result;
}
