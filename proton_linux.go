//go:build linux

package main

// Proton (Linux / Steam Deck) only: install a patched dsound.dll into the game's
// prefix. Wine's dsound mishandles 5.1/7.1 output: any sound with a volume set loses
// centre/LFE/surrounds, mono at full volume goes to every speaker incl. LFE, and mono
// on 7.1 leaves channels with stale data. See proton/README.md for the patch.

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed proton/dsound.dll
var dsoundDLL []byte

const protonSupported = true

var protonAppIDs = []struct{ id, name string }{{"2008750", "Wing Commander IV"}, {"2008740", "Wing Commander III"}}

type protonPrefix struct{ id, name, compat string }

func (p protonPrefix) target() string {
	return filepath.Join(p.compat, "pfx", "drive_c", "windows", "syswow64", "dsound.dll")
}

func steamLibraries() []string {
	seen := map[string]bool{}
	var libs []string
	add := func(l string) {
		if r, err := filepath.EvalSymlinks(l); err == nil {
			l = r
		}
		if !seen[l] && isDir(l) {
			seen[l] = true
			libs = append(libs, l)
		}
	}
	for _, root := range steamRoots() {
		add(root)
		if b, err := os.ReadFile(filepath.Join(root, "steamapps", "libraryfolders.vdf")); err == nil {
			for _, m := range vdfPath.FindAllStringSubmatch(string(b), -1) {
				add(m[1])
			}
		}
	}
	sort.Strings(libs)
	return libs
}

func protonPrefixes() []protonPrefix {
	var r []protonPrefix
	for _, a := range protonAppIDs {
		for _, lib := range steamLibraries() {
			c := filepath.Join(lib, "steamapps", "compatdata", a.id)
			if isDir(filepath.Join(c, "pfx", "drive_c", "windows", "syswow64")) {
				r = append(r, protonPrefix{a.id, a.name, c})
			}
		}
	}
	return r
}

func protonAudioInstalled(p protonPrefix) bool {
	if fi, err := os.Lstat(p.target()); err != nil || fi.Mode()&os.ModeSymlink != 0 {
		return false
	}
	b, err := os.ReadFile(p.target())
	return err == nil && bytes.Equal(b, dsoundDLL)
}

func gameRunning() bool {
	procs, _ := filepath.Glob("/proc/[0-9]*/cmdline")
	for _, f := range procs {
		b, _ := os.ReadFile(f)
		s := strings.ToLower(string(b))
		if strings.Contains(s, "wc4dvd.exe") || strings.Contains(s, "wc4w.exe") || strings.Contains(s, "wc3w.exe") {
			return true
		}
	}
	return false
}

// setDllOverride adds or removes "<dll>"="native" in [Software\\Wine\\DllOverrides],
// right after the section's #time line, the way Wine writes it.
func setDllOverride(userReg, dll string, install bool) error {
	b, err := os.ReadFile(userReg)
	if err != nil {
		return err
	}
	lines := strings.Split(string(b), "\n")
	hdr := `[Software\\Wine\\DllOverrides]`
	h := -1
	for i, l := range lines {
		if strings.HasPrefix(l, hdr) {
			h = i
			break
		}
	}
	if h < 0 {
		if !install {
			return nil
		}
		lines = append(lines, "", hdr+" 0")
		h = len(lines) - 1
	}
	end := len(lines)
	for i := h + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "[") {
			end = i
			break
		}
	}
	prefix := `"` + dll + `"=`
	out := append([]string{}, lines[:h+1]...)
	for _, l := range lines[h+1 : end] {
		if !strings.HasPrefix(l, prefix) {
			out = append(out, l)
		}
	}
	out = append(out, lines[end:]...)
	if install {
		k := h + 1
		if k < len(out) && strings.HasPrefix(out[k], "#time=") {
			k++
		}
		out = append(out[:k], append([]string{prefix + `"native"`}, out[k:]...)...)
	}
	fi, _ := os.Stat(userReg)
	return os.WriteFile(userReg, []byte(strings.Join(out, "\n")), fi.Mode().Perm())
}

func installProtonAudio(p protonPrefix) error {
	reg := filepath.Join(p.compat, "pfx", "user.reg")
	bak := reg + ".bak-wc4-ac3-remux"
	if !isFile(bak) {
		if err := copyFile(reg, bak); err != nil {
			return err
		}
	}
	t := p.target()
	if fi, err := os.Lstat(t); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			link, _ := os.Readlink(t)
			if err := os.WriteFile(filepath.Join(p.compat, "dsound.dll.orig-link"), []byte(link+"\n"), 0o644); err != nil {
				return err
			}
		} else if !protonAudioInstalled(p) {
			if err := copyFile(t, filepath.Join(p.compat, "dsound.dll.orig")); err != nil {
				return err
			}
		}
		os.Remove(t)
	}
	if err := os.WriteFile(t, dsoundDLL, 0o644); err != nil {
		return err
	}
	return setDllOverride(reg, "dsound", true)
}

func removeProtonAudio(p protonPrefix) error {
	t := p.target()
	linkFile := filepath.Join(p.compat, "dsound.dll.orig-link")
	origFile := filepath.Join(p.compat, "dsound.dll.orig")
	if b, err := os.ReadFile(linkFile); err == nil {
		os.Remove(t)
		if err := os.Symlink(strings.TrimSpace(string(b)), t); err != nil {
			return err
		}
		os.Remove(linkFile)
	} else if isFile(origFile) {
		if err := os.Rename(origFile, t); err != nil {
			return err
		}
	}
	return setDllOverride(filepath.Join(p.compat, "pfx", "user.reg"), "dsound", false)
}

// protonAudio installs (install=true) or removes the fix in every WC III/IV prefix.
func protonAudio(install bool) int {
	ps := protonPrefixes()
	if len(ps) == 0 {
		fmt.Println("No Proton prefix found for Wing Commander III/IV. Launch the game once, quit, and run this again.")
		return 1
	}
	if gameRunning() {
		fmt.Println("Close Wing Commander III/IV first.")
		return 1
	}
	code := 0
	for _, p := range ps {
		var err error
		if install {
			err = installProtonAudio(p)
		} else {
			err = removeProtonAudio(p)
		}
		verb := map[bool]string{true: "installed", false: "removed"}[install]
		if err != nil {
			fmt.Printf("  %s: failed: %v\n", p.name, err)
			code = 1
		} else {
			fmt.Printf("  %s: in-game audio fix %s (%s)\n", p.name, verb, p.compat)
		}
	}
	if install && code == 0 {
		fmt.Println("  Re-run this after a Proton version change (it restores Proton's own dsound.dll).")
		fmt.Printf("  WC IV: speech/SFX are lifted ~2.5 dB (its 1996 mix has speech 5 dB under the music);\n  for the original mix add  WINE_DSOUND_VOLUME_SCALE=1 %s  to its launch options.\n", "%command%")
	}
	return code
}
