package main

import (
	"errors"
	"testing"
)

func TestResolve(t *testing.T) {
	tests := []struct {
		name string

		envIdentity  string
		envWorkspace string
		asFlag       string
		wsFlag       string

		want    Config
		wantErr error
	}{
		{
			name:         "environment supplies both",
			envIdentity:  "worker-1",
			envWorkspace: "alpha",
			want:         Config{Identity: "worker-1", Workspace: "alpha"},
		},
		{
			name:         "flags override the environment",
			envIdentity:  "worker-1",
			envWorkspace: "alpha",
			asFlag:       "worker-9",
			wsFlag:       "beta",
			want:         Config{Identity: "worker-9", Workspace: "beta"},
		},
		{
			name:         "flags win independently",
			envIdentity:  "worker-1",
			envWorkspace: "alpha",
			wsFlag:       "beta",
			want:         Config{Identity: "worker-1", Workspace: "beta"},
		},
		{
			name:   "flags alone are enough",
			asFlag: "worker-9",
			wsFlag: "beta",
			want:   Config{Identity: "worker-9", Workspace: "beta"},
		},
		{
			// The workspace is explicit. Nothing is inferred from git or cwd.
			name:        "no workspace anywhere is an error",
			envIdentity: "worker-1",
			wantErr:     ErrNoWorkspace,
		},
		{
			// Identity is not required to read a transcript; commands that
			// write a message check it themselves.
			name:         "identity may be empty",
			envWorkspace: "alpha",
			want:         Config{Identity: "", Workspace: "alpha"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("BRO_IDENTITY", tt.envIdentity)
			t.Setenv("BRO_WORKSPACE", tt.envWorkspace)

			got, err := Resolve(tt.asFlag, tt.wsFlag)

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Resolve() error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve() unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("Resolve() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
