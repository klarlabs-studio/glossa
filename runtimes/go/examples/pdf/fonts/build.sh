#!/bin/sh
# Rebuilds the subset Noto fonts in this directory from google/fonts at a
# pinned revision, verifying each download's SHA-256 (the reasons are in
# ../doc.go, "Fonts"). Run it after changing the ja fixture text, which
# decides the kanji in Noto Sans JP; TestFontsCoverDocuments fails until
# then. Needs curl and fonttools 4.65.0 (pip install fonttools==4.65.0).
set -eu
cd "$(dirname "$0")"

REV=f2bd09badbc763d8757951d52deec29da27e85fb
BASE=https://raw.githubusercontent.com/google/fonts/$REV/ofl
DOCS=../../../testdata/documents

# Reproducible output: fonttools stamps head.modified with this time.
SOURCE_DATE_EPOCH=1767225600 # 2026-01-01T00:00:00Z
export SOURCE_DATE_EPOCH

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

sha256() {
	if command -v sha256sum >/dev/null; then sha256sum "$1"; else shasum -a 256 "$1"; fi | cut -d' ' -f1
}

# fetch <path under ofl/> <sha256> <local name>
fetch() {
	curl -fsSL -o "$tmp/$3" "$BASE/$1"
	got=$(sha256 "$tmp/$3")
	if [ "$got" != "$2" ]; then
		echo "build.sh: $1: sha256 $got, want $2" >&2
		exit 1
	fi
}

fetch 'notosans/NotoSans%5Bwdth,wght%5D.ttf' bfb7bb691513f12e734dc346c03a03f784912432d7e3fa8e56efcf906fe86b3d NotoSans-VF.ttf
fetch 'notosans/NotoSans-Italic%5Bwdth,wght%5D.ttf' 58e6e0ebd1931b29a365aa2d3e2ee9a9e831a3af7cf3ad1462d4e72154f0b291 NotoSans-Italic-VF.ttf
fetch 'notosansjp/NotoSansJP%5Bwght%5D.ttf' c2f3b4d463500a2ddcd3849cded1fceeb9fd6d1c32e6cbecd568453ba50fc68f NotoSansJP-VF.ttf
fetch notosans/OFL.txt cee9892f9f0cc8fe882c9e9537ee6a89621d86ee7ceaf70b02e2b2b1c25c061a OFL-NotoSans.txt
fetch notosansjp/OFL.txt 1c05c68c34f9708415aada51f17e1b0092d2cea709bf4a94cd38114f9e73d7d9 OFL-NotoSansJP.txt
cp "$tmp/OFL-NotoSans.txt" "$tmp/OFL-NotoSansJP.txt" .

# Latin for de, en, es and fr documents in general, not just the fixture:
# Latin-1, Latin Extended-A, general punctuation (U+00A0, U+202F, dashes,
# quotes), currency symbols, letterlike symbols and the minus sign.
LATIN=U+0020-007E,U+00A0-017F,U+0192,U+02C6-02DC,U+2000-206F,U+20A0-20CF,U+2100-2122,U+2212
# Japanese: the same Latin basics, CJK punctuation, kana and full-width
# forms, plus the kanji of the ja fixture (its messages and the rendered
# goldens, which hold CLDR's 年, 月 and 日). A full JIS kanji set would
# be megabytes for an example.
JAPANESE=U+0020-007E,U+00A0-00FF,U+2000-206F,U+20A0-20CF,U+2212,U+3000-30FF,U+FF00-FFEF
cat "$DOCS/messages/ja.json" "$DOCS"/golden/*.ja.html "$DOCS/golden/runs.ja.json" >"$tmp/ja.txt"

# build <variable font> <axes> <unicodes> <output> [subset options]
build() {
	src=$1 axes=$2 unicodes=$3 out=$4
	shift 4
	# shellcheck disable=SC2086 # axes are separate arguments
	python3 -m fontTools.varLib.instancer "$tmp/$src" $axes --quiet -o "$tmp/$out"
	python3 -m fontTools.subset "$tmp/$out" --unicodes="$unicodes" "$@" \
		--layout-features= --no-hinting --desubroutinize \
		--name-IDs='*' --name-languages='*' --name-legacy --output-file="$out"
}

build NotoSans-VF.ttf 'wght=400 wdth=100' "$LATIN" NotoSans-Regular.ttf
build NotoSans-VF.ttf 'wght=700 wdth=100' "$LATIN" NotoSans-Bold.ttf
build NotoSans-Italic-VF.ttf 'wght=400 wdth=100' "$LATIN" NotoSans-Italic.ttf
build NotoSans-Italic-VF.ttf 'wght=700 wdth=100' "$LATIN" NotoSans-BoldItalic.ttf
build NotoSansJP-VF.ttf 'wght=400' "$JAPANESE" NotoSansJP-Regular.ttf --text-file="$tmp/ja.txt"
build NotoSansJP-VF.ttf 'wght=700' "$JAPANESE" NotoSansJP-Bold.ttf --text-file="$tmp/ja.txt"
