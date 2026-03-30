package service

import (
	"reflect"
	"sort"
	"testing"
)

// ── buildConsecutiveGroups ────────────────────────────────────────────────────

func TestBuildConsecutiveGroups(t *testing.T) {
	tests := []struct {
		name   string
		input  []int
		expect [][]int
	}{
		{
			name:   "empty",
			input:  nil,
			expect: nil,
		},
		{
			name:   "single element",
			input:  []int{5},
			expect: [][]int{{5}},
		},
		{
			name:   "all consecutive",
			input:  []int{1, 2, 3},
			expect: [][]int{{1, 2, 3}},
		},
		{
			name:   "all disjoint",
			input:  []int{1, 3, 5},
			expect: [][]int{{1}, {3}, {5}},
		},
		{
			name:   "example from spec: 1,7,8,9",
			input:  []int{1, 7, 8, 9},
			expect: [][]int{{1}, {7, 8, 9}},
		},
		{
			name:   "example from spec: 5,6,8,9",
			input:  []int{5, 6, 8, 9},
			expect: [][]int{{5, 6}, {8, 9}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := buildConsecutiveGroups(tc.input)
			if !reflect.DeepEqual(got, tc.expect) {
				t.Errorf("got %v, want %v", got, tc.expect)
			}
		})
	}
}

// ── selectCourtsToCancel ─────────────────────────────────────────────────────

func TestSelectCourtsToCancel(t *testing.T) {
	tests := []struct {
		name   string
		courts []int
		n      int
		want   []int // sorted for comparison
	}{
		{
			name:   "n=0 returns nothing",
			courts: []int{1, 2, 3},
			n:      0,
			want:   nil,
		},
		{
			name:   "cancel more than available returns all",
			courts: []int{5},
			n:      3,
			want:   []int{5},
		},
		{
			name:   "single group: cancel from end",
			courts: []int{7, 8, 9},
			n:      1,
			want:   []int{9},
		},
		{
			name:   "single group: cancel 2 from end",
			courts: []int{7, 8, 9},
			n:      2,
			want:   []int{8, 9},
		},
		// spec example: groups {5,6} and {8,9}, cancel from smaller-first-element group ({5,6}) from end
		{
			name:   "two equal-size groups: cancel from group with smallest first element",
			courts: []int{5, 6, 8, 9},
			n:      1,
			want:   []int{6},
		},
		// groups {1} and {7,8,9}: {1} is smaller (1 elem), cancel from it first
		{
			name:   "smaller group cancels first (spec: 1,7,8,9)",
			courts: []int{1, 7, 8, 9},
			n:      1,
			want:   []int{1},
		},
		// After canceling from {1} it becomes empty; next pick from {7,8,9}→ cancel 9
		{
			name:   "cancel 2: first from smallest group, then next smallest",
			courts: []int{1, 7, 8, 9},
			n:      2,
			want:   []int{1, 9},
		},
		{
			name:   "cancel all courts",
			courts: []int{3, 4, 5},
			n:      3,
			want:   []int{3, 4, 5},
		},
		// Three equal-length groups: pick by smallest first element
		{
			name:   "three equal groups: smallest first element wins",
			courts: []int{1, 3, 5},
			n:      1,
			want:   []int{1},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := selectCourtsToCancel(tc.courts, tc.n)
			// Sort for deterministic comparison.
			sort.Ints(got)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("selectCourtsToCancel(%v, %d) = %v, want %v",
					tc.courts, tc.n, got, tc.want)
			}
		})
	}
}

// ── removeCanceledFromGameCourts ──────────────────────────────────────────────

func TestRemoveCanceledFromGameCourts(t *testing.T) {
	t.Run("remove last court", func(t *testing.T) {
		got, err := removeCanceledFromGameCourts(
			[]string{"Court1", "Court2", "Court3"},
			[]int{300},
			[]int{100, 200, 300},
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []string{"Court1", "Court2"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("remove first court", func(t *testing.T) {
		got, err := removeCanceledFromGameCourts(
			[]string{"A", "B", "C"},
			[]int{10},
			[]int{10, 20, 30},
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []string{"B", "C"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("remove all courts", func(t *testing.T) {
		got, err := removeCanceledFromGameCourts(
			[]string{"X", "Y"},
			[]int{1, 2},
			[]int{1, 2},
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})

	t.Run("mismatched lengths returns error", func(t *testing.T) {
		_, err := removeCanceledFromGameCourts(
			[]string{"A", "B", "C", "D"},
			[]int{999},
			[]int{1, 2, 3}, // 3 booked vs 4 game courts
		)
		if err == nil {
			t.Error("expected error for mismatched lengths, got nil")
		}
	})
}

// ── formatCanceledCourts ──────────────────────────────────────────────────────

func TestFormatCanceledCourts(t *testing.T) {
	tests := []struct {
		input []int
		want  string
	}{
		{nil, ""},
		{[]int{5}, "5"},
		{[]int{1, 3, 9}, "1, 3, 9"},
	}
	for _, tc := range tests {
		got := formatCanceledCourts(tc.input)
		if got != tc.want {
			t.Errorf("formatCanceledCourts(%v) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// ── splitCourts ───────────────────────────────────────────────────────────────

func TestSplitCourts(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"", nil},
		{"1,2,3", []string{"1", "2", "3"}},
		{" 1 , 2 , 3 ", []string{"1", "2", "3"}},
		{"Court 1,Court 2", []string{"Court 1", "Court 2"}},
	}
	for _, tc := range tests {
		got := splitCourts(tc.input)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("splitCourts(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}
