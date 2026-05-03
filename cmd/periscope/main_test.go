package main

import "testing"

func TestParseWatchStreamsEnv(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want watchStreamsConfig
	}{
		{name: "empty", raw: "", want: watchStreamsConfig{}},
		{name: "whitespace", raw: "   ", want: watchStreamsConfig{}},
		{name: "pods", raw: "pods", want: watchStreamsConfig{pods: true}},
		{name: "all", raw: "all", want: watchStreamsConfig{pods: true}},
		{name: "with spaces", raw: " pods , events ", want: watchStreamsConfig{pods: true}},
		{name: "unknown only", raw: "events", want: watchStreamsConfig{}},
		{name: "unknown plus pods", raw: "events,pods", want: watchStreamsConfig{pods: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseWatchStreamsEnv(tt.raw)
			if got != tt.want {
				t.Errorf("parseWatchStreamsEnv(%q) = %+v, want %+v", tt.raw, got, tt.want)
			}
		})
	}
}
