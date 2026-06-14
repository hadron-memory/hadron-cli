package spec

import "testing"

func TestChildNumbersAt(t *testing.T) {
	locs := []string{"msg", "msg:010", "msg:010:00", "msg:010:01", "msg:010:02", "msg:020", "mat:010"}
	if got := childNumbersAt(Citation{Module: "msg"}, locs); !equalIntsUnordered(got, []int{10, 20}) {
		t.Errorf("features=%v", got)
	}
	if got := childNumbersAt(Citation{Module: "msg", Feature: "010"}, locs); !equalIntsUnordered(got, []int{0, 1, 2}) {
		t.Errorf("rules=%v", got)
	}
}

func TestAllocateChild(t *testing.T) {
	tests := []struct {
		name          string
		parent        Citation
		used, retired []int
		after         int
		want          string
	}{
		{"first feature is 010", Citation{Module: "msg"}, nil, nil, 0, "010"},
		{"features step by tens", Citation{Module: "msg"}, []int{10}, nil, 0, "020"},
		{"first rule skips 00", Citation{Module: "msg", Feature: "010"}, nil, nil, 0, "01"},
		{"only contract present -> 01", Citation{Module: "msg", Feature: "010"}, []int{0}, nil, 0, "01"},
		{"next rule", Citation{Module: "msg", Feature: "010"}, []int{0, 1, 2, 3}, nil, 0, "04"},
		{"rule-after raises floor", Citation{Module: "msg", Feature: "010"}, []int{1}, nil, 7, "08"},
		{"rule skips retired above max", Citation{Module: "msg", Feature: "010"}, []int{1, 2, 3}, []int{4}, 0, "05"},
		{"next flow", Citation{Module: "msg", Feature: "010", Rule: "02"}, []int{1, 2, 3, 4, 5}, nil, 0, "06"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := allocateChild(tt.parent, tt.used, tt.retired, tt.after)
			if err != nil {
				t.Fatalf("allocateChild: %v", err)
			}
			var seg string
			switch tt.parent.Level() + 1 {
			case 2:
				seg = got.Feature
			case 3:
				seg = got.Rule
			case 4:
				seg = got.Flow
			}
			if seg != tt.want {
				t.Errorf("got %q, want %q", seg, tt.want)
			}
		})
	}
}

func TestAllocateChildExhausted(t *testing.T) {
	// Fill every rule slot 1..99 so allocation has nowhere to go.
	used := make([]int, 0, 100)
	for i := 0; i <= 99; i++ {
		used = append(used, i)
	}
	if _, err := allocateChild(Citation{Module: "msg", Feature: "010"}, used, nil, 0); err == nil {
		t.Error("expected exhaustion error when all rule numbers are used")
	}
}
