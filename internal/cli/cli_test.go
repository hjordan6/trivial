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
