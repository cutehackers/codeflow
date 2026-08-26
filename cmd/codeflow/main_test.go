package main

import (
	"flag"
	"testing"
	"time"
)

func TestReorderFlagsKeepsValuesAttached(t *testing.T) {
	fs := flag.NewFlagSet("publish", flag.ContinueOnError)
	limit := fs.Int("limit", 50, "")
	_ = fs.Bool("json", false, "")

	// Flags after positionals must still parse (pre-v2 CLI behavior).
	got := reorderFlags(fs, []string{".", "-limit", "5"})
	if err := fs.Parse(got); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	pos := fs.Args()
	if *limit != 5 {
		t.Errorf("limit = %d, want 5", *limit)
	}
	if len(pos) != 1 || pos[0] != "." {
		t.Errorf("posArgs = %v, want [.] (flag value must not leak into positionals)", pos)
	}

	// Bool flags must not swallow the following positional.
	fs2 := flag.NewFlagSet("flows", flag.ContinueOnError)
	_ = fs2.Bool("json", false, "")
	got2 := reorderFlags(fs2, []string{"repo", "--json"})
	if err := fs2.Parse(got2); err != nil {
		t.Fatalf("Parse bool: %v", err)
	}
	if p := fs2.Args(); len(p) != 1 || p[0] != "repo" {
		t.Errorf("posArgs = %v, want [repo]", p)
	}

	// Equals form stays intact.
	fs3 := flag.NewFlagSet("view", flag.ContinueOnError)
	port := fs3.Int("port", 4567, "")
	got3 := reorderFlags(fs3, []string{"--port=5000", "repo"})
	if err := fs3.Parse(got3); err != nil {
		t.Fatalf("Parse equals: %v", err)
	}
	if *port != 5000 {
		t.Errorf("port = %d, want 5000", *port)
	}
	if p := fs3.Args(); len(p) != 1 || p[0] != "repo" {
		t.Errorf("posArgs = %v, want [repo]", p)
	}

	// Duration-style values stay attached to their flag.
	fs4 := flag.NewFlagSet("x", flag.ContinueOnError)
	d := fs4.Duration("wait", time.Second, "")
	got4 := reorderFlags(fs4, []string{"pos", "-wait", "2s"})
	if err := fs4.Parse(got4); err != nil {
		t.Fatalf("Parse duration: %v", err)
	}
	if *d != 2*time.Second {
		t.Errorf("wait = %v, want 2s", *d)
	}
}
