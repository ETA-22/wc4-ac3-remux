**Repo:** ValveSoftware/Proton. A separate issue from the controller one; Wing Commander IV is where it was traced.

**Title:** Wing Commander IV (2008750): DirectSound levels and channels wrong on 5.1/7.1 output

---

# Compatibility Report
- Name of the game with compatibility issues: Wing Commander IV: The Price of Freedom
- Steam AppID of the game: 2008750 (Wing Commander III, 2008740, is affected on 7.1)

## System Information
- Device: Steam Machine, SteamOS 3.9.5, HDMI to an AV receiver
- Proton version: proton-cachyos 11.0 (source checked: Valve `proton_11.0` e60892c)
- Audio: PipeWire, card profile 5.1 (`output:hdmi-surround`) and 7.1 (`output:hdmi-surround71`)
- Log: <attach `steam-2008750.log` captured with `PROTON_LOG=1 WINEDEBUG=+dsound`>

## Symptoms
On 5.1 output, every sound gets much quieter on the loadout screen. On 7.1 output, Wing Commander III's sound effects
play from the left speaker only, or as a loud buzz on the other channels. Stereo output is fine.

Recording the game's PipeWire stream (`pw-record --target <serial>`) shows mono buffers on all six channels at full
gain (LFE included) on most screens, and front left/right only, with the other four channels digitally silent, on
the loadout screen, where the trace shows the game calling `SetVolume(-512)` on its speech and UI buffers.

Causes in `dlls/dsound`:

1. `DSOUND_RecalcVolPan` fills `dwTotalAmpFactor[0..1]` only; `DSOUND_MixerVol` scales every output channel by its
   factor, so a buffer with a volume or pan set loses centre, LFE and surrounds.
2. `put_mono2surround51` copies mono to all six channels at full gain, while stereo→7.1 and mono→7.1 leave most
   channels silent: levels and placement depend on the speaker setup.
3. The `ichannels == 1` branch of `DSOUND_RecalcFormat` doesn't handle 8 output channels, so the later
   `ichannels == 1 && ochannels == 8` branch is unreachable. The default put function writes channel 0 only, and
   since the temporary buffer is only cleared for `putieee32_sum`, channels 1–7 carry stale data.

## Fix
Upstream Wine MR: <link> (volume on all channels; mono→7.1 in the mono branch; RFC: mono and stereo to the front
pair). The same changes on top of `proton_11.0` (e60892c), with explicit zeros for every muted channel, fix it on
SteamOS: the stream carries audio on front left/right only, the other channels are exact zeros, and the level no
longer drops. Workaround for users in the meantime: <link to repo>.

## Reproduction
1. Set the audio output to 5.1 (or 7.1 for Wing Commander III).
2. Start a new game and go to the loadout screen (WC IV), or fly a mission (WC III).
