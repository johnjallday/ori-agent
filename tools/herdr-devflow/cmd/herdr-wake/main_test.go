package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRunAcceptsOnlyTypedDaemonAndLifecycleCommands(t *testing.T) {
	t.Parallel()
	digest := strings.Repeat("a", 64)
	tests := []struct {
		name        string
		args        []string
		wantExit    int
		wantServe   int
		wantInstall int
		wantRemove  int
	}{
		{name: "serve", args: []string{"serve"}, wantServe: 1},
		{
			name: "install",
			args: []string{
				"install", "--allowed-uid", "501", "--artifact-digest", digest, "--build", "dev",
			},
			wantInstall: 1,
		},
		{
			name: "uninstall", args: []string{"uninstall", "--allowed-uid", "501"},
			wantRemove: 1,
		},
		{name: "unknown", args: []string{"shell", "id"}, wantExit: 2},
		{name: "arbitrary install path", args: []string{"install", "--path", "/tmp/x"}, wantExit: 2},
		{name: "invalid uid", args: []string{"uninstall", "--allowed-uid", "0"}, wantExit: 2},
		{
			name: "uppercase digest",
			args: []string{
				"install", "--allowed-uid", "501", "--artifact-digest", strings.Repeat("A", 64), "--build", "dev",
			},
			wantExit: 2,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			serveCalls, installCalls, removeCalls := 0, 0, 0
			exit := run(
				context.Background(), test.args, &stdout, &stderr,
				func(context.Context, string) error {
					serveCalls++
					return nil
				},
				func(_ context.Context, uid int, gotDigest, requested, compiled string) error {
					installCalls++
					if uid != 501 || gotDigest != digest || requested != "dev" || compiled != "dev" {
						t.Fatalf("install args = %d %q %q %q", uid, gotDigest, requested, compiled)
					}
					return nil
				},
				func(_ context.Context, uid int) error {
					removeCalls++
					if uid != 501 {
						t.Fatalf("uninstall uid = %d", uid)
					}
					return nil
				},
			)
			if exit != test.wantExit ||
				serveCalls != test.wantServe ||
				installCalls != test.wantInstall ||
				removeCalls != test.wantRemove {
				t.Fatalf(
					"exit/calls = %d/%d/%d/%d, want %d/%d/%d/%d; stderr=%q",
					exit, serveCalls, installCalls, removeCalls,
					test.wantExit, test.wantServe, test.wantInstall, test.wantRemove,
					stderr.String(),
				)
			}
		})
	}
}
