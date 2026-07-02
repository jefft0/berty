import { createAudioPlayer, setAudioModeAsync } from "expo-audio";

import { SoundKey, soundsMap } from "./sound.types";

export const playSound = async (name: SoundKey) => {
	const sound = soundsMap[name];

	if (!sound) {
		console.warn(`Tried to play unknown sound "${name}"`);
		return;
	}

	const player = createAudioPlayer(sound);

	// createAudioPlayer requires manual cleanup, so release the player once
	// playback finishes to avoid leaking native resources.
	player.addListener("playbackStatusUpdate", status => {
		if (status.didJustFinish) {
			player.remove();
		}
	});

	await setAudioModeAsync({
		interruptionMode: "mixWithOthers",
	});

	player.play();
};
