package main

import (
	"reflect"
	"testing"
)

func TestParseServeArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    serveOpts
		wantErr bool
	}{
		{"defaults", nil, serveOpts{"127.0.0.1:8080", []string{"http://localhost:3001", "http://127.0.0.1:3001"}}, false},
		{"custom", []string{"-addr", "localhost:9000", "-origins", "http://localhost:4000, http://x:1"}, serveOpts{"localhost:9000", []string{"http://localhost:4000", "http://x:1"}}, false},
		{"ipv6 loopback", []string{"-addr", "[::1]:8080"}, serveOpts{"[::1]:8080", []string{"http://localhost:3001", "http://127.0.0.1:3001"}}, false},
		{"all interfaces", []string{"-addr", ":8080"}, serveOpts{}, true},
		{"0.0.0.0", []string{"-addr", "0.0.0.0:8080"}, serveOpts{}, true},
		{"a LAN address", []string{"-addr", "192.168.1.5:8080"}, serveOpts{}, true},
		{"no port", []string{"-addr", "localhost"}, serveOpts{}, true},
		{"stray argument", []string{"now"}, serveOpts{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseServeArgs(tt.args)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v", err)
			}
			if !tt.wantErr && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}
