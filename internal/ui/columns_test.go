package ui

import (
	"reflect"
	"testing"
)

func TestColumnsForWidth(t *testing.T) {
	tests := []struct {
		name   string
		innerW int
		want   [][]int
	}{
		{"narrow falls back to single column", 0, [][]int{{0, 1, 2, 3, 4}}},
		{"just under the 2-col breakpoint", 79, [][]int{{0, 1, 2, 3, 4}}},
		{"exactly at 2-col breakpoint", 80, [][]int{{0}, {1, 2, 3, 4}}},
		{"middle of 2-col range", 100, [][]int{{0}, {1, 2, 3, 4}}},
		{"just under the 3-col breakpoint", 119, [][]int{{0}, {1, 2, 3, 4}}},
		{"exactly at 3-col breakpoint", 120, [][]int{{0}, {1}, {2, 3, 4}}},
		{"ultra-wide stays 3-col", 300, [][]int{{0}, {1}, {2, 3, 4}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := columnsForWidth(tt.innerW)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("columnsForWidth(%d) = %v, want %v", tt.innerW, got, tt.want)
			}
		})
	}
}

// Sanity: section count must always equal 5 (Skills, MCP, Commands, Agents,
// Hooks) — adding a sixth section without updating columnsForWidth would
// drop the new section silently.
func TestColumnsForWidth_DistributionCoversAllSections(t *testing.T) {
	for _, w := range []int{0, 50, 80, 120, 200} {
		seen := make(map[int]bool)
		for _, col := range columnsForWidth(w) {
			for _, idx := range col {
				if seen[idx] {
					t.Errorf("width %d: section %d appears in multiple columns", w, idx)
				}
				seen[idx] = true
			}
		}
		for i := 0; i < 5; i++ {
			if !seen[i] {
				t.Errorf("width %d: section %d missing from distribution", w, i)
			}
		}
	}
}
