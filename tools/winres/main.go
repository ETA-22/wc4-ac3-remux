// Command winres writes rsrc_windows_amd64.syso: a COFF object with a
// VERSIONINFO resource and an application manifest, which the Go linker embeds
// in the Windows build. Standard library only.
//
//	go run ./tools/winres <version>      (run from the repo root; build.sh does it)
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"strconv"
	"strings"
	"unicode/utf16"
)

const (
	rtVersion  = 16
	rtManifest = 24
	langEnUS   = 0x0409
)

var strs = [][2]string{
	{"CompanyName", "ETA-22"},
	{"FileDescription", "Wing Commander IV audio fix: Dolby Digital 5.1 cutscenes"},
	{"FileVersion", ""}, // filled from the version argument
	{"InternalName", "wc4-ac3-remux"},
	{"LegalCopyright", "MIT License, ETA-22"},
	{"OriginalFilename", "wc4-ac3-remux.exe"},
	{"ProductName", "wc4-ac3-remux"},
	{"ProductVersion", ""},
	{"Comments", "https://github.com/ETA-22/wc4-ac3-remux"},
}

const manifest = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<assembly xmlns="urn:schemas-microsoft-com:asm.v1" manifestVersion="1.0">
  <assemblyIdentity type="win32" name="ETA-22.wc4-ac3-remux" version="%s.0" processorArchitecture="amd64"/>
  <description>Wing Commander IV audio fix</description>
  <trustInfo xmlns="urn:schemas-microsoft-com:asm.v3">
    <security><requestedPrivileges><requestedExecutionLevel level="asInvoker" uiAccess="false"/></requestedPrivileges></security>
  </trustInfo>
  <compatibility xmlns="urn:schemas-microsoft-com:compatibility.v1">
    <application><supportedOS Id="{8e0f7a12-bfb3-4fe8-b9a5-48fd50a15a9a}"/></application>
  </compatibility>
  <application xmlns="urn:schemas-microsoft-com:asm.v3">
    <windowsSettings><activeCodePage xmlns="http://schemas.microsoft.com/SMI/2019/WindowsSettings">UTF-8</activeCodePage></windowsSettings>
  </application>
</assembly>
`

func le(b *bytes.Buffer, v any) { binary.Write(b, binary.LittleEndian, v) }

func pad4(b *bytes.Buffer) {
	for b.Len()%4 != 0 {
		b.WriteByte(0)
	}
}

func wstr(b *bytes.Buffer, s string) {
	for _, u := range utf16.Encode([]rune(s)) {
		le(b, u)
	}
	le(b, uint16(0))
}

// block writes a version-info node: wLength, wValueLength, wType, key, value, children.
func block(key string, valueLen uint16, typ uint16, value func(*bytes.Buffer), children func(*bytes.Buffer)) []byte {
	var b bytes.Buffer
	le(&b, uint16(0)) // wLength, patched below
	le(&b, valueLen)
	le(&b, typ)
	wstr(&b, key)
	pad4(&b)
	if value != nil {
		value(&b)
		pad4(&b)
	}
	if children != nil {
		children(&b)
	}
	out := b.Bytes()
	binary.LittleEndian.PutUint16(out, uint16(len(out)))
	return out
}

func versionInfo(ver string) []byte {
	parts := strings.Split(ver, ".")
	n := [4]uint32{}
	for i := 0; i < len(parts) && i < 4; i++ {
		v, _ := strconv.Atoi(parts[i])
		n[i] = uint32(v)
	}
	ms, ls := n[0]<<16|n[1], n[2]<<16|n[3]
	fixed := func(b *bytes.Buffer) {
		for _, v := range []uint32{
			0xFEEF04BD, 0x00010000, // signature, struct version
			ms, ls, ms, ls, // file version, product version
			0x3F, 0, // flags mask, flags
			0x00040004, 1, 0, // VOS_NT_WINDOWS32, VFT_APP, subtype
			0, 0, // date
		} {
			le(b, v)
		}
	}
	strTable := func(b *bytes.Buffer) {
		b.Write(block("040904b0", 0, 1, nil, func(b *bytes.Buffer) {
			for _, kv := range strs {
				val := kv[1]
				if val == "" {
					val = ver
				}
				b.Write(block(kv[0], uint16(len(utf16.Encode([]rune(val)))+1), 1,
					func(b *bytes.Buffer) { wstr(b, val) }, nil))
				pad4(b)
			}
		}))
	}
	return block("VS_VERSION_INFO", 52, 0, fixed, func(b *bytes.Buffer) {
		b.Write(block("StringFileInfo", 0, 1, nil, strTable))
		pad4(b)
		b.Write(block("VarFileInfo", 0, 1, nil, func(b *bytes.Buffer) {
			b.Write(block("Translation", 4, 0, func(b *bytes.Buffer) { le(b, uint32(0x04B00409)) }, nil))
		}))
	})
}

type res struct {
	typ  uint32
	data []byte
}

// rsrcSection lays out a resource directory tree (type -> ID 1 -> en-US) and returns
// the section bytes plus the offsets of the data entries' OffsetToData fields, which
// need IMAGE_REL_AMD64_ADDR32NB relocations against the section.
func rsrcSection(rs []res) ([]byte, []uint32) {
	n := len(rs)
	dirSize := func(entries int) int { return 16 + 8*entries }
	rootOff := 0
	typeOff := rootOff + dirSize(n)
	langOff := typeOff + n*dirSize(1)
	dataEntOff := langOff + n*dirSize(1)
	blobOff := dataEntOff + n*16
	var blobs bytes.Buffer
	blobPos := make([]int, n)
	for i, r := range rs {
		for (blobOff+blobs.Len())%8 != 0 {
			blobs.WriteByte(0)
		}
		blobPos[i] = blobOff + blobs.Len()
		blobs.Write(r.data)
	}

	var b bytes.Buffer
	dir := func(ids int) {
		le(&b, uint32(0))
		le(&b, uint32(0))
		le(&b, uint16(0))
		le(&b, uint16(0))
		le(&b, uint16(0))
		le(&b, uint16(ids))
	}
	dir(n)
	for i, r := range rs {
		le(&b, r.typ)
		le(&b, uint32(0x80000000|(typeOff+i*dirSize(1))))
	}
	for i := range rs {
		dir(1)
		le(&b, uint32(1))
		le(&b, uint32(0x80000000|(langOff+i*dirSize(1))))
	}
	for i := range rs {
		dir(1)
		le(&b, uint32(langEnUS))
		le(&b, uint32(dataEntOff+i*16))
	}
	var relocs []uint32
	for i, r := range rs {
		relocs = append(relocs, uint32(b.Len()))
		le(&b, uint32(blobPos[i])) // section-relative; the reloc adds the section RVA
		le(&b, uint32(len(r.data)))
		le(&b, uint32(0))
		le(&b, uint32(0))
	}
	b.Write(blobs.Bytes())
	return b.Bytes(), relocs
}

func coff(section []byte, relocs []uint32) []byte {
	const hdr, secHdr = 20, 40
	rawOff := hdr + secHdr
	relOff := rawOff + len(section)
	symOff := relOff + 10*len(relocs)
	var b bytes.Buffer
	le(&b, uint16(0x8664)) // AMD64
	le(&b, uint16(1))      // sections
	le(&b, uint32(0))      // timestamp (reproducible)
	le(&b, uint32(symOff))
	le(&b, uint32(1)) // symbols
	le(&b, uint16(0)) // optional header size
	le(&b, uint16(0)) // characteristics
	name := [8]byte{'.', 'r', 's', 'r', 'c'}
	b.Write(name[:])
	le(&b, uint32(0)) // virtual size
	le(&b, uint32(0)) // virtual address
	le(&b, uint32(len(section)))
	le(&b, uint32(rawOff))
	le(&b, uint32(relOff))
	le(&b, uint32(0)) // line numbers
	le(&b, uint16(len(relocs)))
	le(&b, uint16(0))
	le(&b, uint32(0x40000040)) // initialized data, readable
	b.Write(section)
	for _, r := range relocs {
		le(&b, r)
		le(&b, uint32(0)) // symbol 0: the section
		le(&b, uint16(3)) // IMAGE_REL_AMD64_ADDR32NB
	}
	b.Write(name[:])
	le(&b, uint32(0)) // value
	le(&b, int16(1))  // section number
	le(&b, uint16(0)) // type
	b.WriteByte(3)    // IMAGE_SYM_CLASS_STATIC
	b.WriteByte(0)    // aux symbols
	le(&b, uint32(4)) // empty string table
	return b.Bytes()
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: go run ./tools/winres <version>")
		os.Exit(2)
	}
	ver := os.Args[1]
	sec, rel := rsrcSection([]res{
		{rtVersion, versionInfo(ver)},
		{rtManifest, []byte(fmt.Sprintf(manifest, ver))},
	})
	if err := os.WriteFile("rsrc_windows_amd64.syso", coff(sec, rel), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("wrote rsrc_windows_amd64.syso for", ver)
}
