package main

import (
	"errors"
	"flag"
	"testing"

	"github.com/ErenKarakas1/repocheck/src/health"
)

func TestParseArgs(t *testing.T) {
	defaults := health.Config{BranchReportLimit: 10}
	tests := []struct {
		args        []string
		wantCfg     health.Config
		wantVerbose bool
	}{
		{nil, defaults, false},
		{[]string{"dir"}, health.Config{Path: "dir", BranchReportLimit: 10}, false},
		{[]string{"-v", "dir"}, health.Config{Path: "dir", BranchReportLimit: 10}, true},
		{[]string{"--verbose", "--", "-dir"}, health.Config{Path: "-dir", BranchReportLimit: 10}, true},
		{[]string{"-j", "4", "--branch-report-limit=0"}, health.Config{Jobs: 4}, false},
		{[]string{"--depth", "3"}, health.Config{Depth: 3, BranchReportLimit: 10}, false},
		{[]string{"--pull", "--follow-symlinks"}, health.Config{FollowSymlinks: true, Fetch: true, Pull: true, BranchReportLimit: 10}, false},
	}
	for _, tt := range tests {
		cfg, verbose, err := parseArgs(tt.args)
		if err != nil {
			t.Errorf("parseArgs(%q): %v", tt.args, err)
		} else if cfg != tt.wantCfg || verbose != tt.wantVerbose {
			t.Errorf("parseArgs(%q) = %+v, %v, want %+v, %v", tt.args, cfg, verbose, tt.wantCfg, tt.wantVerbose)
		}
	}

	for _, args := range [][]string{{"a", "b"}, {"--jobs", "-1"}, {"--branch-report-limit", "-1"}, {"--depth", "-1"}, {"-j", "x"}, {"--bogus"}} {
		if _, _, err := parseArgs(args); err == nil {
			t.Errorf("parseArgs(%q) succeeded, want error", args)
		}
	}

	for _, args := range [][]string{{"-h"}, {"--help", "dir"}} {
		if _, _, err := parseArgs(args); !errors.Is(err, flag.ErrHelp) {
			t.Errorf("parseArgs(%q) = %v, want flag.ErrHelp", args, err)
		}
	}
}
