package dag

import (
	"testing"
)

type mockNode struct {
	id   string
	deps []string
}

func (m mockNode) ID() string             { return m.id }
func (m mockNode) Dependencies() []string { return m.deps }

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		nodes   []Node
		wantErr bool
	}{
		{
			name: "linear dependencies",
			nodes: []Node{
				mockNode{"A", []string{"B"}},
				mockNode{"B", []string{"C"}},
				mockNode{"C", nil},
			},
			wantErr: false,
		},
		{
			name: "parallel dependencies",
			nodes: []Node{
				mockNode{"A", []string{"B"}},
				mockNode{"C", []string{"B"}},
				mockNode{"B", nil},
			},
			wantErr: false,
		},
		{
			name: "self loop",
			nodes: []Node{
				mockNode{"A", []string{"A"}},
			},
			wantErr: true,
		},
		{
			name: "simple cycle",
			nodes: []Node{
				mockNode{"A", []string{"B"}},
				mockNode{"B", []string{"A"}},
			},
			wantErr: true,
		},
		{
			name: "complex cycle",
			nodes: []Node{
				mockNode{"A", []string{"B"}},
				mockNode{"B", []string{"C"}},
				mockNode{"C", []string{"A"}},
			},
			wantErr: true,
		},
		{
			name: "disconnected graphs",
			nodes: []Node{
				mockNode{"A", []string{"B"}},
				mockNode{"B", nil},
				mockNode{"C", []string{"D"}},
				mockNode{"D", nil},
			},
			wantErr: false,
		},
		{
			name: "missing dependency",
			nodes: []Node{
				mockNode{"A", []string{"B"}},
			},
			wantErr: true,
		},
		{
			name: "missing dependency in larger graph",
			nodes: []Node{
				mockNode{"A", []string{"B"}},
				mockNode{"B", []string{"X"}},
				mockNode{"C", nil},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := Validate(tt.nodes); (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
