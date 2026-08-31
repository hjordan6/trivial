package cli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/hjordan6/trivial/internal/cli"
)

func TestRunArgumentErrors(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"no arguments", []string{}, "usage"},
		{"unknown command", []string{"dance"}, "unknown command"},
		{"migrate without direction", []string{"migrate"}, "usage"},
		{"migrate with bad direction", []string{"migrate", "sideways"}, "up or down"},
		{"puzzles without subcommand", []string{"puzzles"}, "usage"},
		{"puzzles show without a date", []string{"puzzles", "show"}, "usage"},
		{"puzzles show with a bad date", []string{"puzzles", "show", "tomorrow"}, "parse date"},
		{"puzzles generate with zero days", []string{"puzzles", "generate", "--days", "0"}, "must be positive"},
		{"seed without subcommand", []string{"seed"}, "usage"},
		{"mail without subcommand", []string{"mail"}, "usage"},
		{"mail with an unknown subcommand", []string{"mail", "blast"}, "usage"},
		{"mail test without an address", []string{"mail", "test"}, "usage"},
		{"mail test with extra arguments", []string{"mail", "test", "a@b.com", "c@d.com"}, "usage"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := cli.Run(context.Background(), tt.args, &stdout, &stderr)
			if err == nil {
				t.Fatal("Run() error = nil, want error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Run() error = %q, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestRunHelpSucceeds(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := cli.Run(context.Background(), []string{"help"}, &stdout, &stderr); err != nil {
		t.Fatalf("Run(help) error = %v", err)
	}
	for _, want := range []string{"migrate", "seed", "puzzles"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("help output missing %q; got:\n%s", want, stdout.String())
		}
	}
}

// A mail test with no provider configured has to fail loudly. The log sender
// would report success without sending anything, which is indistinguishable
// from the broken setup this command exists to diagnose.
func TestMailTestWithoutAProviderRefuses(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://unused/unused")
	t.Setenv("RESEND_API_KEY", "")
	t.Setenv("DEVELOPMENT_MODE", "true")

	var stdout, stderr bytes.Buffer
	err := cli.Run(context.Background(), []string{"mail", "test", "player@example.com"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("Run() error = nil, want an error about the missing key")
	}
	if !strings.Contains(err.Error(), "RESEND_API_KEY") {
		t.Errorf("Run() error = %q, want it to name RESEND_API_KEY", err)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want nothing written before the failure", stdout.String())
	}
}
