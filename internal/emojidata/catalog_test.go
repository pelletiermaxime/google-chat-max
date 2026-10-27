package emojidata

import "testing"

func TestCompleteQualifiedCatalogAndCategories(t *testing.T) {
	if len(Catalog) != 3963 {
		t.Fatalf("catalog has %d emoji", len(Catalog))
	}
	groups := map[string]int{}
	seen := map[string]bool{}
	for _, e := range Catalog {
		if seen[e.Unicode] || e.Name == "" {
			t.Fatalf("duplicate or unnamed emoji: %q", e.Unicode)
		}
		seen[e.Unicode] = true
		groups[e.Group]++
	}
	if len(groups) != 9 {
		t.Fatalf("unexpected categories: %v", groups)
	}
	for _, emoji := range []string{"🫩", "🦥", "🪈", "🇨🇦", "👩🏽‍💻", "🧑🏿‍🤝‍🧑🏻"} {
		if !seen[emoji] {
			t.Errorf("catalog missing %s", emoji)
		}
	}
}

func TestNameSubgroupAndUnicodeSearch(t *testing.T) {
	for _, tc := range []struct{ emoji, query string }{{"🦥", "sloth"}, {"🇨🇦", "canada"}, {"👩‍💻", "woman technologist"}, {"🧋", "bubble_tea"}, {"😀", "face-smiling"}, {"😀", "😀"}} {
		found := false
		for _, e := range Catalog {
			if e.Unicode == tc.emoji && e.Matches(tc.query) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%q did not match %s", tc.query, tc.emoji)
		}
	}
}

func TestSkinToneReplacementAndMixedVariants(t *testing.T) {
	for _, tc := range []struct {
		emoji, tone string
		want        bool
	}{
		{"👍", "Default", true}, {"👍🏽", "Default", false}, {"👍", "Medium", false},
		{"👍🏽", "Medium", true}, {"👍🏻", "Medium", false}, {"🐸", "Medium", true},
		{"☝️", "Medium", false}, {"☝🏽", "Medium", true},
		{"🧑🏿‍🤝‍🧑🏻", "All tones", true}, {"🧑🏿‍🤝‍🧑🏻", "Dark", false},
	} {
		found := false
		for _, e := range Catalog {
			if e.Unicode == tc.emoji {
				found = true
				if got := e.MatchesTone(tc.tone); got != tc.want {
					t.Errorf("%s in %s: %v", tc.emoji, tc.tone, got)
				}
				break
			}
		}
		if !found {
			t.Errorf("missing test emoji %s", tc.emoji)
		}
	}
}
