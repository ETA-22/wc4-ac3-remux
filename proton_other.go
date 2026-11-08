//go:build !linux

package main

// The in-game audio fix is for Proton (Wine) only; Windows doesn't have the bug.

const protonSupported = false

type protonPrefix struct{ id, name, compat string }

func protonPrefixes() []protonPrefix           { return nil }
func protonAudioInstalled(p protonPrefix) bool { return false }
func protonAudio(install bool) int             { return 0 }
