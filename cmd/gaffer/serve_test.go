package main

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseServeArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    serveOpts
		wantErr bool
	}{
		{"defaults", nil, serveOpts{addr: "127.0.0.1:8080", origins: []string{"http://localhost:3001", "http://127.0.0.1:3001"}}, false},
		{"custom", []string{"-addr", "localhost:9000", "-origins", "http://localhost:4000, http://x:1"}, serveOpts{addr: "localhost:9000", origins: []string{"http://localhost:4000", "http://x:1"}}, false},
		{"ipv6 loopback", []string{"-addr", "[::1]:8080"}, serveOpts{addr: "[::1]:8080", origins: []string{"http://localhost:3001", "http://127.0.0.1:3001"}}, false},
		{"all interfaces", []string{"-addr", ":8080"}, serveOpts{}, true},
		{"0.0.0.0", []string{"-addr", "0.0.0.0:8080"}, serveOpts{}, true},
		{"a LAN address", []string{"-addr", "192.168.1.5:8080"}, serveOpts{}, true},
		{"no port", []string{"-addr", "localhost"}, serveOpts{}, true},
		{"stray argument", []string{"now"}, serveOpts{}, true},
		// Behind the login proxy in Kubernetes.
		{"public with hosts", []string{"-public", "-addr", ":8080", "-hosts", "gaffer.1-2-3-4.sslip.io, gaffer-api", "-origins", "https://gaffer.1-2-3-4.sslip.io"},
			serveOpts{addr: ":8080", origins: []string{"https://gaffer.1-2-3-4.sslip.io"}, public: true, hosts: []string{"gaffer.1-2-3-4.sslip.io", "gaffer-api"}}, false},
		{"public needs hosts", []string{"-public", "-addr", ":8080"}, serveOpts{}, true},
		{"hosts need public", []string{"-hosts", "x.example"}, serveOpts{}, true},
		{"public still needs a port", []string{"-public", "-addr", "0.0.0.0", "-hosts", "x"}, serveOpts{}, true},
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

func TestParseNewsRefresh(t *testing.T) {
	tests := []struct {
		in      string
		want    time.Duration
		wantErr string
	}{
		{"", 30 * time.Minute, ""},
		{"  ", 30 * time.Minute, ""},
		{"off", 0, ""},
		{"0", 0, ""},
		{"10m", 10 * time.Minute, ""},
		{"2h", 2 * time.Hour, ""},
		{"5m", 5 * time.Minute, ""},
		{"1m", 0, "at least 5m"},
		{"soon", 0, "want a duration"},
		{"30", 0, "want a duration"}, // no unit
	}
	for _, tt := range tests {
		got, err := parseNewsRefresh(tt.in)
		if tt.wantErr == "" && (err != nil || got != tt.want) {
			t.Errorf("%q = %v, %v; want %v", tt.in, got, err, tt.want)
		}
		if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
			t.Errorf("%q: err = %v, want it to mention %q", tt.in, err, tt.wantErr)
		}
	}
}
