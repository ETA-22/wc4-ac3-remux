#!/bin/sh
# Play a game with the HDMI output switched to stereo, then switch back.
# Steam launch options:  /path/to/stereo-while-playing.sh %command%
SINK=$(pactl get-default-sink)
case "$SINK" in alsa_output.*) ;; *) exec "$@" ;; esac          # not an ALSA output: just run
CARD=$(printf '%s\n' "$SINK" | sed 's/^alsa_output\.\(.*\)\.[^.]*$/alsa_card.\1/')
CUR=${SINK##*.}                                                  # e.g. hdmi-surround71
STEREO="output:$(printf '%s\n' "$CUR" | sed 's/surround71/stereo/; s/surround/stereo/')"
PREV=$(pactl list cards | sed -n "/Name: $CARD\$/,/Active Profile/s/^[[:space:]]*Active Profile: //p")
[ "output:$CUR" = "$STEREO" ] && exec "$@"                       # already stereo
pactl set-card-profile "$CARD" "$STEREO" || exec "$@"
"$@"; rc=$?
[ -n "$PREV" ] && pactl set-card-profile "$CARD" "$PREV"
exit $rc
