// wc4-ac3-remux: put the DVD's discrete 5.1 AC-3 audio onto the upscaled 1080p
// FMVs of Wing Commander IV: The Price of Freedom (30th Anniversary edition with
// Matt Wells' wc4w_en Enhancement Patch).
//
// The Enhanced build plays WC4_Enhanced/Master_FMVs_Upscaled/*.mp4 (stereo AAC).
// The bundled DVD build (WC4_DVD/vob/*.vob) has the original Dolby Digital 5.1
// mix. This tool stream-copies (no re-encode) the upscaled H.264 video with the
// DVD AC-3 track, applying a measured per-clip sync offset.
//
// The DVD mix is ~15 dB quieter than the (heavily limited) stereo masters, so
// by default it is raised by 6 dB with a -1 dB peak limiter and re-encoded as
// 640 kbps AC-3 (source is 384 kbps). The same gain is applied to every clip so
// the balance between scenes is unchanged. --gain 0 = pure stream copy.
//
// Default: 5.1-capable clips are replaced in place, so the stock "Master FMVs
// Upscaled" launch option plays them. Undo with Steam's "Verify integrity of
// game files", or keep the originals: --keep-originals moves each original clip
// into <folder>.orig before replacing it, and --restore moves them back.
//
// ffmpeg/ffprobe: used from next to this program (the release zips include
// them), else from PATH. Nothing is ever downloaded.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

const version = "1.0.0"

// The anniversary release has three FMV sets with the same clip names. Each
// gets the DVD's AC-3 5.1 track; offsets were measured by cross-correlating the
// set's stereo track against a downmix of the DVD AC-3 (30 s windows; no drift
// across the 9-minute intro). Positive = DVD audio is delayed by that much.
type fmvSet struct {
	key, folder, label string
	defOffset          float64
	offsets            map[string]float64
	skip               map[string]string
	note               string // shown when the set's table wasn't verified on real files
}

// The DVD's audio is silent for these clips; every set keeps its own audio.
var silentOnDVD = map[string]string{
	"SC_2470C": "DVD audio track is silent (dialogue missing on the 1996 DVD)",
	"SC_2900D": "DVD audio track is silent",
	"SC_2900E": "DVD audio track is silent",
	"SC_5150A": "DVD audio track is silent",
	"SC_5150B": "DVD audio track is silent",
	"SC_5150C": "DVD audio track is silent",
	"SC_5150D": "DVD audio track is silent",
	"SC_5150E": "DVD audio track is silent",
	"SC_5150F": "DVD audio track is silent",
	"SC_5150G": "DVD audio track is silent",
}

// The 1080p masters (Upscaled and Source share bit-identical audio).
var masterOffsets = map[string]float64{
	"SC_0010A": 0.122,
	"SC_2170A": 0.089,
	"SC_3240A": 0.001,
	"SC_3250A": 0.001,
}

var masterSkip = merge(silentOnDVD, map[string]string{
	"SC_2570A": "master is a 0.04 s stub (upstream issue)",
	"SC_3460C": "different edits (master 9 s, DVD 29 s)",
	"SC_2170C": "different edits (master 55 s, DVD 14 s)",
	"SC_3630A": "different edits (master 147 s, DVD 102 s)",
	"SC_8000A": "different edits (master 10.0 s, DVD 10.6 s)",
	"SC_8001A": "different edits (master 12.2 s, DVD 13.3 s)",
})

// A further 24 clips have only 2.0 AC-3 on the DVD; they are detected at runtime.
var sets = []fmvSet{
	{key: "upscaled", folder: "Master_FMVs_Upscaled", label: "WC4 Enhanced - Master FMVs Upscaled",
		defOffset: 0.022, offsets: masterOffsets, skip: masterSkip},
	{key: "source", folder: "Master_FMVs_Source", label: "WC4 Enhanced - Master FMVs Source",
		defOffset: 0.022, offsets: masterOffsets, skip: masterSkip,
		note: "uses the Upscaled timing table (identical audio); not verified on real files"},
	{key: "dvd", folder: "vob", label: "WC4 Enhanced - DVD FMVs",
		defOffset: 0, offsets: map[string]float64{}, skip: silentOnDVD},
}

func merge(a, b map[string]string) map[string]string {
	m := map[string]string{}
	for k, v := range a {
		m[k] = v
	}
	for k, v := range b {
		m[k] = v
	}
	return m
}

const durTolerance = 0.5 // seconds

var ffmpeg, ffprobe string

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

func selfDir() string {
	p, err := os.Executable()
	if err != nil {
		return "."
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return filepath.Dir(p)
}

func isFile(p string) bool { st, err := os.Stat(p); return err == nil && !st.IsDir() }
func isDir(p string) bool  { st, err := os.Stat(p); return err == nil && st.IsDir() }

func findTool(name string) (string, error) {
	if p := filepath.Join(selfDir(), name+exeSuffix()); isFile(p) {
		return p, nil
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	hint := "install ffmpeg with your package manager (it includes ffprobe)"
	if runtime.GOOS == "windows" {
		hint = "put ffmpeg.exe and ffprobe.exe next to this program, or run: winget install Gyan.FFmpeg"
	}
	return "", fmt.Errorf("%s not found. The release zip includes it; otherwise %s", name, hint)
}

// --- install discovery -----------------------------------------------------

var vdfPath = regexp.MustCompile(`"path"\s+"([^"]+)"`)

func steamRoots() []string {
	var roots []string
	home, _ := os.UserHomeDir()
	if runtime.GOOS == "windows" {
		for _, env := range []string{"ProgramFiles(x86)", "ProgramFiles"} {
			if v := os.Getenv(env); v != "" {
				roots = append(roots, filepath.Join(v, "Steam"))
			}
		}
		roots = append(roots, `C:\Steam`, `D:\Steam`, `D:\SteamLibrary`)
	} else {
		roots = append(roots,
			filepath.Join(home, ".local/share/Steam"),
			filepath.Join(home, ".steam/steam"),
			filepath.Join(home, ".var/app/com.valvesoftware.Steam/.local/share/Steam"))
	}
	return roots
}

func findGame() []string {
	seen := map[string]bool{}
	var found []string
	add := func(lib string) {
		g := filepath.Join(lib, "steamapps", "common", "Wing Commander IV")
		if abs, err := filepath.Abs(g); err == nil {
			g = abs
		}
		if r, err := filepath.EvalSymlinks(g); err == nil {
			g = r
		}
		if !seen[g] && isDir(filepath.Join(g, "WC4_Enhanced")) {
			seen[g] = true
			found = append(found, g)
		}
	}
	for _, root := range steamRoots() {
		add(root)
		b, err := os.ReadFile(filepath.Join(root, "steamapps", "libraryfolders.vdf"))
		if err != nil {
			continue
		}
		for _, m := range vdfPath.FindAllStringSubmatch(string(b), -1) {
			add(strings.ReplaceAll(m[1], `\\`, `\`))
		}
	}
	return found
}

// --- media helpers -------------------------------------------------------------

func probe(path, entries, stream string) []string {
	args := []string{"-v", "error"}
	if stream != "" {
		args = append(args, "-select_streams", stream)
	}
	args = append(args, "-show_entries", entries, "-of", "default=nw=1:nk=1", path)
	out, _ := exec.Command(ffprobe, args...).Output()
	var r []string
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			r = append(r, l)
		}
	}
	return r
}

func duration(path string) (float64, bool) {
	v := probe(path, "format=duration", "")
	if len(v) == 0 {
		return 0, false
	}
	f, err := strconv.ParseFloat(v[0], 64)
	return f, err == nil
}

func is51(path string) bool {
	v := probe(path, "stream=codec_name,channels", "a")
	return len(v) == 2 && v[0] == "ac3" && v[1] == "6"
}

// trackName marks the gain used, so a rerun with a different --gain rebuilds.
func trackName(gain float64) string {
	if gain == 0 {
		return "Surround 5.1"
	}
	return fmt.Sprintf("Surround 5.1 (%+g dB)", gain)
}

func done51(path string, gain float64) bool {
	if !is51(path) {
		return false
	}
	h := probe(path, "stream_tags=handler_name", "a")
	return len(h) == 1 && h[0] == trackName(gain)
}

var reI = regexp.MustCompile(`(?s)Summary:.*?I:\s*(-?[0-9.]+|-inf) LUFS`)

// loudness returns integrated EBU R128 loudness (LUFS) of the first audio stream.
func loudness(path string) (float64, bool) {
	out, _ := exec.Command(ffmpeg, "-nostats", "-i", path, "-map", "0:a:0", "-af", "ebur128", "-f", "null", "-").CombinedOutput()
	m := reI.FindStringSubmatch(string(out))
	if m == nil || m[1] == "-inf" {
		return 0, false
	}
	v, err := strconv.ParseFloat(m[1], 64)
	return v, err == nil && v > -60
}

const matchTolerance = 0.5 // dB

// stereoCeiling caps re-levelled stereo clips (before gain) at the level of a
// typical 5.1 dialogue scene: -17 LUFS at +6 dB. The stereo-only clips are mostly
// short in-flight events (e.g. SC_8011A) mixed hot even on the 1996 DVD and
// heavily limited in the masters, so they have almost no dynamics; capped at
// action-scene level (-12) they still felt much louder than everything else.
const stereoCeiling = -23.0

// The tag records gain and ceiling, so changing either re-levels on the next run.
func stereoTag(gain float64) string {
	return fmt.Sprintf("Stereo (matched for %+g dB, max %g LUFS)", gain, stereoCeiling+gain)
}

// matchStereo re-levels a clip that keeps its stereo track so it sits where the
// 5.1 clips do: at the DVD mix's loudness plus the chosen gain. The 1080p masters
// were normalised clip by clip (to about -8 LUFS) and would otherwise play
// 4-14 dB louder than the 5.1 clips around them. Capped at stereoCeiling+gain.
// The track name records the gain it was matched for, so reruns leave it alone.
// Returns a short description, or "" if nothing changed.
// processedByUs reports whether a clip's audio track was written by this tool.
func processedByUs(p string) bool {
	h := probe(p, "stream_tags=handler_name", "a")
	return len(h) == 1 && (strings.HasPrefix(h[0], "Surround 5.1") || strings.HasPrefix(h[0], "Stereo (matched"))
}

// noOriginal counts clips that should have been kept but were already modified
// by an earlier run without --keep-originals.
var noOriginal int

// commit replaces dst with tmp. With keepDir set, the current dst is first moved
// (renamed, no copy) into keepDir, unless keepDir already has it or dst is
// already a modified clip, which must not be stored as an "original".
func commit(tmp, dst, keepDir string) error {
	if keepDir != "" {
		orig := filepath.Join(keepDir, filepath.Base(dst))
		switch {
		case isFile(orig):
		case processedByUs(dst):
			noOriginal++
		default:
			if err := os.MkdirAll(keepDir, 0o755); err != nil {
				return err
			}
			if err := os.Rename(dst, orig); err != nil {
				return err
			}
		}
	}
	return os.Rename(tmp, dst)
}

func matchStereo(dst, vob string, gain float64, keepDir string) (string, error) {
	if vob == "" {
		return "", nil
	}
	if h := probe(dst, "stream_tags=handler_name", "a"); len(h) == 1 && h[0] == stereoTag(gain) {
		return "", nil
	}
	ref, ok := loudness(vob)
	if !ok {
		return "", nil // DVD audio silent: no reference
	}
	cur, ok := loudness(dst)
	if !ok {
		return "", nil
	}
	target := math.Min(ref, stereoCeiling) + gain
	delta := target - cur
	if math.Abs(delta) < matchTolerance {
		return "", nil
	}
	tmp := strings.TrimSuffix(dst, ".mp4") + ".tmp.mp4"
	// always limit: the masters are clipped (true peaks up to about +5 dBTP)
	af := fmt.Sprintf("volume=%.1fdB,alimiter=limit=0.794328:level=0:latency=1", delta) // -2 dB, AAC overshoot headroom
	cmd := exec.Command(ffmpeg, "-v", "error", "-nostdin", "-y", "-i", dst,
		"-map", "0:v:0", "-map", "0:a:0", "-c:v", "copy", "-af", af, "-c:a", "aac", "-b:a", "256k",
		"-metadata:s:a:0", "language="+audioLang(dst), "-metadata:s:a:0", "handler_name="+stereoTag(gain),
		"-movflags", "+faststart", tmp)
	if msg, err := cmd.CombinedOutput(); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("%v %s", err, strings.TrimSpace(string(msg)))
	}
	if err := commit(tmp, dst, keepDir); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return fmt.Sprintf("stereo level-matched %+.1f dB (to %.1f LUFS)", delta, target), nil
}

// audioLang returns the clip's audio language tag. The patch picks the FMV audio
// track by the game's language, so the new track must keep the original's tag.
func audioLang(path string) string {
	if v := probe(path, "stream_tags=language", "a"); len(v) == 1 && v[0] != "" && v[0] != "und" {
		return v[0]
	}
	return "eng"
}

var warnedLang = map[string]bool{}

func ciFind(dir, stem, ext string) string {
	want := strings.ToLower(stem + ext)
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.ToLower(e.Name()) == want {
			return filepath.Join(dir, e.Name())
		}
	}
	return ""
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// --- main ------------------------------------------------------------------------

func main() {
	setList := flag.String("sets", "", "FMV sets to convert: upscaled,source,dvd (default: all installed)")
	keepOrig := flag.Bool("keep-originals", false, "move each original clip into <folder>.orig before replacing it (restore with --restore)")
	restore := flag.Bool("restore", false, "move the kept original clips back, undoing the conversion")
	force := flag.Bool("force", false, "rebuild clips that are already done")
	gain := flag.Float64("gain", 6, "dB added to the DVD 5.1 audio (0 = stream copy, no re-encode)")
	noPause := flag.Bool("no-pause", false, "don't wait for Enter before exiting")
	showVer := flag.Bool("version", false, "print version")
	var protonOn, protonOff *bool
	if protonSupported {
		protonOn = flag.Bool("proton-audio", false, "only install the in-game audio fix (patched dsound.dll) into the WC III/IV Proton prefixes")
		protonOff = flag.Bool("remove-proton-audio", false, "only remove the in-game audio fix")
	}
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "wc4-ac3-remux %s\nUsage: wc4-ac3-remux [options] [\"<...>/Wing Commander IV\"]\n"+
			"Run with no options for a guided setup. Any option skips the questions.\n\n", version)
		flag.PrintDefaults()
	}
	flag.Parse()
	if *showVer {
		fmt.Println(version)
		return
	}
	fmt.Printf("wc4-ac3-remux %s - Wing Commander IV audio: Dolby Digital 5.1 cutscenes", version)
	if protonSupported {
		fmt.Print(", Proton in-game audio fix")
	}
	fmt.Print("\n\n")
	if protonSupported && (*protonOn || *protonOff) {
		os.Exit(protonAudio(*protonOn))
	}
	cfg := config{keepOriginals: *keepOrig, restore: *restore, force: *force, gain: *gain}
	if *setList != "" {
		for _, k := range strings.Split(*setList, ",") {
			k = strings.ToLower(strings.TrimSpace(k))
			found := false
			for _, s := range sets {
				if s.key == k {
					cfg.sets, found = append(cfg.sets, s.key), true
				}
			}
			if !found {
				fmt.Printf("unknown set %q (use upscaled, source, dvd)\n", k)
				os.Exit(1)
			}
		}
	}
	if len(flag.Args()) > 0 {
		cfg.root = normalizeRoot(flag.Args()[0])
	}
	interactive := flag.NFlag() == 0 // double-click, or a folder dropped on the program
	code := 0
	if interactive {
		code = wizard(&cfg)
	}
	if code == 0 && cfg.protonAudio != "" {
		fmt.Println("In-game audio (Proton):")
		code = protonAudio(cfg.protonAudio == "install")
		fmt.Println()
	}
	if code == 0 && cfg.restore {
		code = restoreOriginals(cfg)
	} else if code == 0 {
		code = run(cfg)
	}
	if interactive && !*noPause {
		fmt.Print("\nPress Enter to exit...")
		stdin.ReadString('\n')
	}
	os.Exit(code)
}

type config struct {
	root          string
	sets          []string // keys; empty = all installed
	keepOriginals bool
	restore       bool
	force         bool
	gain          float64
	protonAudio   string // "", "install" or "remove" (wizard, Linux only)
}

func installedSets(root string) []fmvSet {
	var r []fmvSet
	for _, s := range sets {
		if isDir(filepath.Join(root, "WC4_Enhanced", s.folder)) {
			r = append(r, s)
		}
	}
	return r
}

var stdin = bufio.NewReader(os.Stdin)

func ask(prompt string) string {
	fmt.Print(prompt)
	s, _ := stdin.ReadString('\n')
	return strings.TrimSpace(strings.Trim(strings.TrimSpace(s), `"'`))
}

// choose shows numbered options; Enter picks the first (recommended) one.
func choose(question string, options []string) int {
	fmt.Println(question)
	for i, o := range options {
		fmt.Printf("  %d) %s\n", i+1, o)
	}
	for {
		s := ask(fmt.Sprintf("Choice [1-%d, Enter = 1]: ", len(options)))
		if s == "" {
			fmt.Println()
			return 0
		}
		if n, err := strconv.Atoi(s); err == nil && n >= 1 && n <= len(options) {
			fmt.Println()
			return n - 1
		}
	}
}

func normalizeRoot(p string) string {
	p = filepath.Clean(p)
	if b := strings.ToLower(filepath.Base(p)); b == "wc4_enhanced" || b == "wc4_dvd" {
		p = filepath.Dir(p)
	}
	return p
}

func validRoot(p string) bool {
	return len(installedSets(p)) > 0
}

func wizard(cfg *config) int {
	// 1. game folder
	if cfg.root == "" || !validRoot(cfg.root) {
		games := findGame()
		if len(games) > 0 {
			opts := append(append([]string{}, games...), "Somewhere else (type the path)")
			if i := choose("Which Wing Commander IV install?", opts); i < len(games) {
				cfg.root = games[i]
			} else {
				cfg.root = ""
			}
		}
		for cfg.root == "" || !validRoot(cfg.root) {
			if cfg.root != "" {
				fmt.Println("That folder doesn't contain any WC4_Enhanced FMV folders.")
			}
			s := ask("Path to the \"Wing Commander IV\" folder (or Enter to quit): ")
			if s == "" {
				return 1
			}
			cfg.root = normalizeRoot(s)
		}
		fmt.Println()
	}
	// convert or restore, if originals were kept by an earlier run
	if len(keptSets(cfg.root)) > 0 {
		if choose("Original clips from an earlier run are kept. What do you want to do?", []string{
			"Convert / update the cutscenes",
			"Restore the original cutscenes",
		}) == 1 {
			cfg.restore = true
			return 0
		}
	}

	// 2. which FMV sets
	have := installedSets(cfg.root)
	if len(have) > 1 {
		var labels []string
		for _, s := range have {
			labels = append(labels, s.label)
		}
		opts := []string{"All of them: " + strings.Join(labels, ", ")}
		opts = append(opts, labels...)
		i := choose("Which FMV sets should get 5.1? (named after the Steam launch options)", opts)
		if i > 0 {
			cfg.sets = []string{have[i-1].key}
		}
	}

	// 3. keep the originals?
	cfg.keepOriginals = choose(fmt.Sprintf("Keep the original clips so you can restore them later?\n"+
		"(They're moved, not copied: up to %.1f GB more disk space.)", float64(clipBytes(pickSets(*cfg), cfg.root))/1e9), []string{
		"Yes, move them to <folder>.orig",
		"No, restore with Steam's \"Verify integrity of game files\" if needed",
	}) == 0

	// 4. loudness
	switch choose("The DVD 5.1 mix is about 15 dB quieter than the stereo masters. How much louder?", []string{
		"+6 dB (recommended; sits well against the in-game audio)",
		"+10 dB (louder; a peak limiter catches the loudest moments)",
		"+15 dB (as loud as the stereo masters; heavy limiting)",
		"No change (exact DVD audio, no re-encode)",
		"Custom",
	}) {
	case 0:
		cfg.gain = 6
	case 1:
		cfg.gain = 10
	case 2:
		cfg.gain = 15
	case 3:
		cfg.gain = 0
	case 4:
		for {
			if g, err := strconv.ParseFloat(ask("Gain in dB (0-20): "), 64); err == nil && g >= 0 && g <= 20 {
				cfg.gain = g
				break
			}
		}
		fmt.Println()
	}

	// 5. in-game audio fix (Proton only)
	if ps := protonPrefixes(); len(ps) > 0 {
		installed := true
		for _, p := range ps {
			installed = installed && protonAudioInstalled(p)
		}
		q := "In-game audio: Wine (Proton) mishandles 5.1/7.1 output, so sounds jump between speakers\n" +
			"and some screens (e.g. the loadout) get much quieter. Install the fix (a patched dsound.dll\n" +
			"in the game's Proton prefix)? Only matters if your audio output is 5.1 or 7.1."
		if installed {
			switch choose(q+"\n(It's already installed.)", []string{"Keep it", "Update / reinstall it", "Remove it"}) {
			case 1:
				cfg.protonAudio = "install"
			case 2:
				cfg.protonAudio = "remove"
			}
		} else if choose(q, []string{"Yes, install it", "No"}) == 0 {
			cfg.protonAudio = "install"
		}
	}

	// 6. summary
	var names []string
	for _, s := range pickSets(*cfg) {
		names = append(names, s.folder)
	}
	orig := "not kept (Steam's Verify integrity restores them)"
	if cfg.keepOriginals {
		orig = "kept in <folder>.orig"
	}
	fmt.Printf("Ready:\n  game:      %s\n  sets:      %s\n  originals: %s\n  gain:      %+g dB\n",
		cfg.root, strings.Join(names, ", "), orig, cfg.gain)
	if cfg.protonAudio != "" {
		fmt.Printf("  in-game audio fix: %s\n", cfg.protonAudio)
	}
	fmt.Println()
	if choose("Start?", []string{"Yes", "No, quit"}) != 0 {
		return 1
	}
	return 0
}

func pickSets(cfg config) []fmvSet {
	have := installedSets(cfg.root)
	if len(cfg.sets) == 0 {
		return have
	}
	var r []fmvSet
	for _, s := range have {
		for _, k := range cfg.sets {
			if s.key == k {
				r = append(r, s)
			}
		}
	}
	return r
}

func run(cfg config) int {
	if cfg.root == "" {
		games := findGame()
		if len(games) == 0 {
			fmt.Println("Could not find Wing Commander IV in your Steam libraries.")
			fmt.Println(`Run again with the install folder, e.g.  wc4-ac3-remux "D:\SteamLibrary\steamapps\common\Wing Commander IV"`)
			return 1
		}
		cfg.root = games[0]
	}
	vobDir := filepath.Join(cfg.root, "WC4_DVD", "vob")
	if !isDir(vobDir) {
		fmt.Printf("error: %s not found.\n(If you deleted WC4_DVD, restore it with Steam's \"Verify integrity of game files\".)\n", vobDir)
		return 1
	}
	todo := pickSets(cfg)
	if len(todo) == 0 {
		fmt.Println("error: none of the requested FMV sets are installed in", filepath.Join(cfg.root, "WC4_Enhanced"))
		return 1
	}
	fmt.Println("Game folder:", cfg.root)

	var err error
	if ffmpeg, err = findTool("ffmpeg"); err != nil {
		fmt.Println("error:", err)
		return 1
	}
	if ffprobe, err = findTool("ffprobe"); err != nil {
		fmt.Println("error:", err)
		return 1
	}
	fmt.Println("ffmpeg:", ffmpeg)

	code := 0
	for _, s := range todo {
		if c := processSet(cfg, s, vobDir); c != 0 {
			code = c
		}
	}
	fmt.Println()
	fmt.Println("The normal launch options now play 5.1 where available.")
	if cfg.keepOriginals {
		var dirs []string
		for _, s := range todo {
			if isDir(filepath.Join(cfg.root, "WC4_Enhanced", s.folder+".orig")) {
				dirs = append(dirs, "WC4_Enhanced/"+s.folder+".orig")
			}
		}
		if len(dirs) > 0 {
			fmt.Printf("Originals kept in %s; run the tool again and choose Restore (or --restore) to put them back.\n", strings.Join(dirs, ", "))
		}
		if noOriginal > 0 {
			fmt.Printf("%d clips were already converted by an earlier run that didn't keep originals;\n"+
				"for those, Steam's Verify integrity is the only way back.\n", noOriginal)
		}
	} else {
		fmt.Println("Undo: Steam > Properties > Installed Files > Verify integrity.")
	}
	fmt.Println("Set your audio output to 5.1 or 7.1 to hear it.")
	return code
}

func processSet(cfg config, set fmvSet, vobDir string) int {
	gain, force := cfg.gain, cfg.force
	srcDir := filepath.Join(cfg.root, "WC4_Enhanced", set.folder)
	keepDir := ""
	if cfg.keepOriginals {
		keepDir = srcDir + ".orig"
	}

	ents, _ := os.ReadDir(srcDir)
	var stems []string
	for _, e := range ents {
		if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".mp4") {
			stems = append(stems, strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())))
		}
	}
	sort.Strings(stems)
	fmt.Printf("\n== %s (%s): %d clips\n", set.label, set.folder, len(stems))
	if set.note != "" {
		fmt.Println("   note:", set.note)
	}

	matched := 0
	keep := func(src, dst, vob, tag, why string) {
		note, err := matchStereo(dst, vob, gain, keepDir)
		switch {
		case err != nil:
			fmt.Printf("%s: kept stereo - %s (level match failed: %v)\n", tag, why, err)
		case note != "":
			matched++
			fmt.Printf("%s: kept stereo - %s; %s\n", tag, why, note)
		default:
			fmt.Printf("%s: kept stereo - %s\n", tag, why)
		}
	}
	n51, kept, existing, fallback := 0, 0, 0, 0
	for i, s := range stems {
		src := filepath.Join(srcDir, s+".mp4")
		dst := src
		key := strings.ToUpper(s)
		tag := fmt.Sprintf("[%d/%d] %s", i+1, len(stems), s)
		if isFile(dst) && done51(dst, gain) && !force {
			existing++
			continue
		}
		vob := ciFind(vobDir, s, ".vob")
		if why, ok := set.skip[key]; ok {
			keep(src, dst, vob, tag, why)
			kept++
			continue
		}
		du, okU := duration(src)
		var dv float64
		okV := false
		if vob != "" {
			dv, okV = duration(vob)
		}
		if vob == "" || !okU || !okV || math.Abs(du-dv) > durTolerance {
			keep(src, dst, vob, tag, fmt.Sprintf("no matching DVD clip (%.1f s / %.1f s)", du, dv))
			fallback++
			continue
		}
		if ch := probe(vob, "stream=channels", "a"); len(ch) == 0 || ch[0] != "6" {
			c := "no"
			if len(ch) > 0 {
				c = ch[0] + "ch"
			}
			keep(src, dst, vob, tag, "DVD audio is "+c+", not 5.1")
			kept++
			continue
		}
		off, ok := set.offsets[key]
		if !ok {
			off = set.defOffset
		}
		tmp := strings.TrimSuffix(dst, ".mp4") + ".tmp.mp4"
		a := []string{"-v", "error", "-nostdin", "-y",
			"-i", src, "-itsoffset", fmt.Sprintf("%.3f", off), "-i", vob,
			"-map", "0:v:0", "-map", "1:a:0", "-c:v", "copy"}
		if gain == 0 {
			a = append(a, "-c:a", "copy")
		} else {
			// latency=1 compensates the limiter's lookahead so sync is unchanged
			a = append(a, "-af", fmt.Sprintf("volume=%gdB,alimiter=limit=0.891251:level=0:latency=1", gain),
				"-c:a", "ac3", "-b:a", "640k")
		}
		// the patch selects the audio track by language, so keep the original tag
		lang := audioLang(src)
		if lang != "eng" && !warnedLang[lang] {
			warnedLang[lang] = true
			fmt.Printf("note: audio language is %q; the sync offsets were measured on the English release and are unverified for other languages.\n", lang)
		}
		a = append(a, "-metadata:s:a:0", "language="+lang, "-metadata:s:a:0", "handler_name="+trackName(gain),
			"-movflags", "+faststart", tmp)
		msg, err := exec.Command(ffmpeg, a...).CombinedOutput()
		if err == nil && is51(tmp) {
			if v := probe(tmp, "stream=codec_name", "v"); len(v) == 1 && v[0] == "h264" {
				if err = commit(tmp, dst, keepDir); err == nil {
					n51++
					fmt.Printf("%s: 5.1 (%+.0f ms, %+g dB)\n", tag, off*1000, gain)
					continue
				}
			} else {
				err = errors.New("video stream check failed")
			}
		} else if err == nil {
			err = errors.New("output is not 6-channel AC-3")
		}
		os.Remove(tmp)
		keep(src, dst, vob, tag, fmt.Sprintf("mux FAILED: %v %s", err, strings.TrimSpace(string(msg))))
		fallback++
	}
	fmt.Printf("%s: %d clips with 5.1, %d kept stereo (expected; %d level-matched), %d fell back to stereo, %d already done.\n",
		set.folder, n51, kept, matched, fallback, existing)
	if fallback > 0 {
		return 2
	}
	return 0
}

func keptSets(root string) []fmvSet {
	var r []fmvSet
	for _, s := range sets {
		if isDir(filepath.Join(root, "WC4_Enhanced", s.folder+".orig")) {
			r = append(r, s)
		}
	}
	return r
}

// clipBytes is the total size of the clips in the given sets (an upper bound for
// the space kept originals need).
func clipBytes(todo []fmvSet, root string) int64 {
	var n int64
	for _, s := range todo {
		ents, _ := os.ReadDir(filepath.Join(root, "WC4_Enhanced", s.folder))
		for _, e := range ents {
			if fi, err := e.Info(); err == nil && !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".mp4") {
				n += fi.Size()
			}
		}
	}
	return n
}

// restoreOriginals moves the kept original clips back and removes the .orig folders.
func restoreOriginals(cfg config) int {
	if cfg.root == "" {
		if games := findGame(); len(games) > 0 {
			cfg.root = games[0]
		}
	}
	kept := keptSets(cfg.root)
	if len(kept) == 0 {
		fmt.Println("No kept originals found (no <folder>.orig in WC4_Enhanced). Use Steam's Verify integrity instead.")
		return 1
	}
	code := 0
	for _, s := range kept {
		dir := filepath.Join(cfg.root, "WC4_Enhanced", s.folder)
		od := dir + ".orig"
		ents, _ := os.ReadDir(od)
		n := 0
		for _, e := range ents {
			if e.IsDir() {
				continue
			}
			if err := os.Rename(filepath.Join(od, e.Name()), filepath.Join(dir, e.Name())); err != nil {
				fmt.Printf("  %s: %v\n", e.Name(), err)
				code = 1
				continue
			}
			n++
		}
		os.Remove(od) // only succeeds when empty
		fmt.Printf("%s: restored %d original clips\n", s.label, n)
	}
	return code
}
