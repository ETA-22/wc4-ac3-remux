**Where:** gitlab.winehq.org/wine/wine. Fork the repo, push a branch with the commits in `proton/upstream/`, and open a
merge request. Mention the `dsound` maintainer listed in `MAINTAINERS`. Commit 3 changes behaviour rather than fixing a
bug, so it may be better as a separate MR, or dropped if the maintainer prefers the current mapping.

**Title:** dsound: Fix volume and mono handling on 5.1/7.1 output.

---

Found with Wing Commander IV (Steam appid 2008750, the 30th anniversary release) on a 5.1 HDMI output under Proton.
Every sound got noticeably quieter on one menu screen. A capture of the game's audio stream showed:

- on most screens, mono buffers on **all six channels** at the same level (LFE included);
- on that screen, audio on **front left/right only**, with centre, LFE and surrounds digitally silent.

A `+dsound` trace showed the game setting `SetVolume(-512)` on its speech and UI buffers there.

1. **`DSOUND_RecalcVolPan()` only fills `dwTotalAmpFactor[0]` and `[1]`.** `DSOUND_MixerVol()` (and `put_samples()`)
   scale every output channel by its factor, so on 5.1/7.1 output any buffer with a volume or pan set loses
   channels 2 and up. Buffers at 0 dB skip the volume step and keep all their channels, so a game's quieter sounds
   drop out of most speakers. The first commit gives every channel past the first two the buffer volume (the pan
   still only applies to left and right).

2. **Mono to 7.1 is unreachable.** The `ichannels == 1` branch of `DSOUND_RecalcFormat()` handles 2, 4 and 6 output
   channels, so the later `ichannels == 1 && ochannels == 8` branch never runs, and a mono buffer on 7.1 output is
   left without an `out_channel_map` for its format (`put_samples()` asserts on it). The second commit moves the
   7.1 case into the mono branch. This was found by reading the code, not by hitting the assert; in Proton 11's older
   mixer the same unreachable branch leaves channels 1–7 with stale data, which plays as a loud buzz in Wing Commander
   III.

3. **Mono and stereo are mapped inconsistently** (RFC). Mono goes to all six channels of 5.1 at full gain but only to
   the centre on 7.1; stereo goes to the front and back pairs on 5.1 but only to the front pair on 7.1. The same
   game therefore sounds different, and on 5.1 much louder, depending on the speaker configuration. The third
   commit maps both to the front pair, which matches stereo output and the existing stereo→7.1 map. I don't have a
   Windows reference capture for how DirectSound itself places mono buffers on multichannel output; if someone knows,
   I'm happy to match that instead.

Testing: compile-tested on master (`c42c153`), including the dsound tests. The behaviour was tested through an
equivalent change to Proton 11's `dlls/dsound` (`proton/` in this repo) on SteamOS with a 5.1
HDMI output: after it, the game's stream carries audio on front left/right only, the other channels are exact zeros,
and the level no longer drops on the affected screen. I haven't written a conformance test, since the result is only
observable in the mixed output; suggestions welcome.
