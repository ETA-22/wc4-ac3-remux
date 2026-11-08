#!/bin/sh
# Usage: docker run --rm -v "$PWD/proton:/src" debian:trixie sh /src/build.sh   (writes proton/dsound.dll)
# Build Proton 11 i386 dsound.dll with dsound-proton_11.0-e60892c.patch (ValveSoftware/wine proton_11.0 @ e60892c).
set -e
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq binutils-mingw-w64-i686 git gcc gcc-multilib gcc-mingw-w64-i686 gcc-mingw-w64-x86-64 make flex bison autoconf perl python3 >/dev/null
cd /src
if [ ! -d wine/.git ]; then
  git init -q wine && cd wine && git remote add origin https://github.com/ValveSoftware/wine.git
  git fetch -q --depth 1 origin e60892c 2>/dev/null || git fetch -q --depth 1 origin proton_11.0
  git checkout -q FETCH_HEAD && cd ..
fi
cd wine && git log -1 --format='%h %s' && git checkout -q -- dlls/dsound && git apply ../dsound-proton_11.0-e60892c.patch
autoreconf -f >/dev/null 2>&1
python3 dlls/winevulkan/make_vulkan >/dev/null 2>&1 || true
tools/make_requests >/dev/null 2>&1 || true
tools/make_specfiles >/dev/null 2>&1 || true
mkdir -p ../b && cd ../b
../wine/configure -q --enable-archs=x86_64,i386 --without-x --without-wayland --without-vulkan --without-opengl --without-gstreamer --without-pulse --without-alsa --without-oss --without-cups --without-sane --without-krb5 --without-netapi --without-v4l2 --without-pcap --without-usb --without-dbus --without-capi --without-osmesa --without-unwind --without-freetype --without-gnutls --without-sdl --without-udev --without-ffmpeg >/dev/null 2>&1
echo CONFIGURED
make -j$(nproc) dlls/dsound/i386-windows/dsound.dll >/src/make.log 2>&1 || { tail -40 /src/make.log; exit 1; }
cp dlls/dsound/i386-windows/dsound.dll /src/dsound.dll
i686-w64-mingw32-strip --strip-debug /src/dsound.dll
# mark it so Wine loads it as a native override
python3 - /src/dsound.dll <<"PY"
import sys
p = sys.argv[1]; d = bytearray(open(p, "rb").read()); i = d.find(b"Wine builtin DLL")
assert 0 < i < 0x100; d[i:i + 16] = b"Wine patched DLL"; open(p, "wb").write(d)
PY
sha256sum /src/dsound.dll && echo BUILT
