// Package emojidata provides the offline Unicode 18.0 emoji keyboard catalog.
// The generated data is distributed under the Unicode license in LICENSE.
package emojidata

import (
	_ "embed"
	"strings"
)

//go:embed emoji.tsv
var data string

type Emoji struct {
	Unicode, Group, Name string
	search               string
	tones                uint8
	base                 string
}

var Catalog = parse(data)
var toneBases = func() map[string]bool {
	bases := make(map[string]bool)
	for _, e := range Catalog {
		if e.tones != 0 {
			bases[e.base] = true
		}
	}
	return bases
}()

func parse(data string) []Emoji {
	var entries []Emoji
	for line := range strings.SplitSeq(data, "\n") {
		if line == "" || strings.HasPrefix(line, "# ") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 4 {
			panic("invalid bundled emoji catalog")
		}
		e := Emoji{Unicode: fields[0], Group: fields[1], Name: fields[3], search: normalize(fields[3] + " " + fields[2])}
		var base strings.Builder
		for _, r := range e.Unicode {
			if r >= 0x1F3FB && r <= 0x1F3FF {
				e.tones |= 1 << (r - 0x1F3FB)
			} else if r != 0xFE0F { // Presentation selectors vary between neutral and toned forms.
				base.WriteRune(r)
			}
		}
		e.base = base.String()
		entries = append(entries, e)
	}
	return entries
}

func normalize(s string) string {
	return strings.ToLower(strings.NewReplacer("-", " ", "_", " ", ":", " ").Replace(s))
}

func (e Emoji) Matches(query string) bool {
	if strings.Contains(e.Unicode, query) {
		return true
	}
	for _, word := range strings.Fields(normalize(query)) {
		if !strings.Contains(e.search, word) {
			return false
		}
	}
	return true
}

// MatchesTone shows neutral emoji by default, substitutes the selected tone
// where available, and keeps unmodifiable emoji. All tones exposes mixed-tone
// sequences too, without flooding the normal people category with variants.
func (e Emoji) MatchesTone(tone string) bool {
	if tone == "All tones" {
		return true
	}
	var mask uint8
	switch tone {
	case "Light":
		mask = 1
	case "Medium-light":
		mask = 2
	case "Medium":
		mask = 4
	case "Medium-dark":
		mask = 8
	case "Dark":
		mask = 16
	default:
		return e.tones == 0
	}
	return e.tones == mask || (e.tones == 0 && !toneBases[e.base])
}
