// SPDX-FileCopyrightText: 2026 The bink Authors
// SPDX-License-Identifier: Apache-2.0

package node

import (
	"testing"
)

func TestParseLabels(t *testing.T) {
	tests := []struct {
		name    string
		input   []string
		want    map[string]string
		wantErr bool
	}{
		{
			name:  "valid single label",
			input: []string{"env=test"},
			want:  map[string]string{"env": "test"},
		},
		{
			name:  "valid multiple labels",
			input: []string{"env=test", "tier=frontend"},
			want:  map[string]string{"env": "test", "tier": "frontend"},
		},
		{
			name:  "empty value is valid",
			input: []string{"env="},
			want:  map[string]string{"env": ""},
		},
		{
			name:  "value with equals sign",
			input: []string{"config=a=b"},
			want:  map[string]string{"config": "a=b"},
		},
		{
			name:  "whitespace trimmed",
			input: []string{" env = test "},
			want:  map[string]string{"env": "test"},
		},
		{
			name:    "missing equals sign",
			input:   []string{"foo"},
			wantErr: true,
		},
		{
			name:    "empty key",
			input:   []string{"=bar"},
			wantErr: true,
		},
		{
			name:    "whitespace-only key",
			input:   []string{" =bar"},
			wantErr: true,
		},
		{
			name:    "duplicate key",
			input:   []string{"env=test", "env=prod"},
			wantErr: true,
		},
		{
			name:  "empty input",
			input: []string{},
			want:  map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseLabels(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("parseLabels(%v) expected error, got nil", tt.input)
				}
				return
			}
			if err != nil {
				t.Errorf("parseLabels(%v) unexpected error: %v", tt.input, err)
				return
			}
			if len(got) != len(tt.want) {
				t.Errorf("parseLabels(%v) = %v, want %v", tt.input, got, tt.want)
				return
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("parseLabels(%v)[%q] = %q, want %q", tt.input, k, got[k], v)
				}
			}
		})
	}
}
