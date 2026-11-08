#!/usr/bin/env python3
"""Build the release zips: tool + bundled ffmpeg/ffprobe (eugeneware/ffmpeg-static b6.1.1).
Run ./build.sh first. Downloads the ffmpeg files once into .pkg/, verifies every SHA-256
against the digests GitHub publishes for that release, then writes dist/*.zip + SHA256SUMS."""
import gzip, hashlib, os, urllib.request, zipfile

BASE = "https://github.com/eugeneware/ffmpeg-static/releases/download/b6.1.1/"
FILES = {
    "ffmpeg-linux-x64.gz": "bfe8a8fc511530457b528c48d77b5737527b504a3797a9bc4866aeca69c2dffa",
    "ffprobe-linux-x64.gz": "25d9b6ccb05e3d9de9e04e31e2506d8dd7f9f0418981965ac6df12e8d3afd067",
    "ffmpeg-win32-x64.gz": "8883a3dffbd0a16cf4ef95206ea05283f78908dbfb118f73c83f4951dcc06d77",
    "ffprobe-win32-x64.gz": "f309e6223ad89d2fe54bccd420a7709b66fd27540674e92309578ed491a43c8d",
    "linux-x64.LICENSE": "8ceb4b9ee5adedde47b31e975c1d90c73ad27b6b165a1dcd80c7c545eb65b903",
    "linux-x64.README": "72f4b1b06d419d22ace6e7cc75f06826f90737345aa0b1736158929f4aacc537",
    "win32-x64.LICENSE": "8ceb4b9ee5adedde47b31e975c1d90c73ad27b6b165a1dcd80c7c545eb65b903",
    "win32-x64.README": "a636a7183c58006351acbaf35303c0ed85c6e1320fd4e80de453ba6157de6311",
}
README = """wc4-ac3-remux - Dolby Digital 5.1 for the Wing Commander IV FMVs
https://github.com/ETA-22/wc4-ac3-remux

{run}

The tool is MIT licensed (LICENSE). ffmpeg/ffprobe are bundled unmodified from
github.com/eugeneware/ffmpeg-static release b6.1.1 ({ffver}); they are GPL v3,
see FFMPEG-LICENSE.txt and FFMPEG-README.txt for the build and source details.
"""
PKGS = {
    "windows-x64": dict(tool="wc4-ac3-remux-windows-x64.exe", name="wc4-ac3-remux.exe", os="win32", ext=".exe", nl="\r\n",
                        ffver="ffmpeg 6.1.1, gyan.dev build",
                        run="Run: double-click wc4-ac3-remux.exe and follow the questions.\nKeep ffmpeg.exe and ffprobe.exe in the same folder."),
    "linux-x64": dict(tool="wc4-ac3-remux-linux-x64", name="wc4-ac3-remux", os="linux", ext="", nl="\n",
                      ffver="ffmpeg 7.0.2, johnvansickle.com build",
                      run="Run in a terminal:  ./wc4-ac3-remux   and follow the questions.\nKeep ffmpeg and ffprobe in the same folder.\nIn-game audio fix for 5.1/7.1 output (Proton):  ./wc4-ac3-remux --proton-audio"),
}

def sha(b): return hashlib.sha256(b).hexdigest()

def fetch(name):
    os.makedirs(".pkg", exist_ok=True)
    p = os.path.join(".pkg", name)
    if not os.path.exists(p):
        urllib.request.urlretrieve(BASE + name, p)
    data = open(p, "rb").read()
    if sha(data) != FILES[name]:
        os.remove(p)
        raise SystemExit(f"SHA-256 mismatch for {name}")
    return data

def main():
    sums = []
    for plat, c in PKGS.items():
        top = f"wc4-ac3-remux-{plat}"
        out = f"dist/{top}.zip"
        with zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED, compresslevel=9) as z:
            def add(arc, data, exe=False):
                zi = zipfile.ZipInfo(f"{top}/{arc}", date_time=(2026, 11, 7, 0, 0, 0))
                zi.compress_type, zi.create_system = zipfile.ZIP_DEFLATED, 3
                zi.external_attr = (0o100755 if exe else 0o100644) << 16  # keeps +x for unzip/Ark
                z.writestr(zi, data)
            add(c["name"], open(f"dist/{c['tool']}", "rb").read(), exe=True)
            for tool in ("ffmpeg", "ffprobe"):
                add(tool + c["ext"], gzip.decompress(fetch(f"{tool}-{c['os']}-x64.gz")), exe=True)
            add("FFMPEG-LICENSE.txt", fetch(f"{c['os']}-x64.LICENSE"))
            add("FFMPEG-README.txt", fetch(f"{c['os']}-x64.README"))
            add("LICENSE", open("LICENSE", "rb").read())
            add("README.txt", README.format(run=c["run"], ffver=c["ffver"]).replace("\n", c["nl"]).encode())
        sums.append(f"{sha(open(out, 'rb').read())}  {os.path.basename(out)}")
        print(out)
    open("dist/SHA256SUMS", "w").write("\n".join(sums) + "\n")
    print("\n".join(sums))

if __name__ == "__main__":
    main()
