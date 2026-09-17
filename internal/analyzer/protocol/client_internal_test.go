package protocol

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"testing"
)

func TestIDAllocationUniqueAndMonotonic(t *testing.T) {
	var g idGen
	const workers = 50
	const perWorker = 20
	ids := make(chan string, workers*perWorker)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				ids <- g.next()
			}
		}()
	}
	wg.Wait()
	close(ids)

	seen := map[string]bool{}
	values := make([]int, 0, len(seen))
	for id := range ids {
		if seen[id] {
			t.Fatalf("duplicate id %q", id)
		}
		var n int
		if _, err := fmt.Sscanf(id, "cf-%d", &n); err != nil {
			t.Fatalf("malformed id %q", id)
		}
		seen[id] = true
		values = append(values, n)
	}
	if len(seen) != workers*perWorker {
		t.Fatalf("got %d unique ids, want %d", len(seen), workers*perWorker)
	}
	sort.Ints(values)
	for i, n := range values {
		if n != i+1 {
			t.Fatalf("ids must be exactly 1..N with no gaps; index %d has %d", i, n)
		}
	}
}

// TestPendingResolutionRace hammers removePending from multiple
// goroutines for the same pending call: exactly one racer may resolve.
// Run with -race.
func TestPendingResolutionRace(t *testing.T) {
	for iter := 0; iter < 200; iter++ {
		c := &Conn{pending: map[string]chan *reply{}}
		id := fmt.Sprintf("cf-%04d", iter)
		c.registerPending(id)

		const racers = 8
		resolved := make(chan *reply, racers)
		rep := &reply{ok: true, result: []byte(`{}`)}
		var wg sync.WaitGroup
		for r := 0; r < racers; r++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if ch, ok := c.removePending(id); ok {
					select {
					case ch <- rep:
					default:
					}
					resolved <- rep
				}
			}()
		}
		wg.Wait()
		close(resolved)
		n := 0
		for range resolved {
			n++
		}
		if n != 1 {
			t.Fatalf("iter %d: resolved %d times, want exactly 1", iter, n)
		}
	}
}

// TestMarkBrokenFailsAllPending exercises the reader-side fan-out of
// E_CRASHED to every in-flight call. Run with -race.
func TestMarkBrokenFailsAllPending(t *testing.T) {
	c := &Conn{pending: map[string]chan *reply{}}
	const calls = 16
	chans := make([]chan *reply, calls)
	for i := 0; i < calls; i++ {
		chans[i] = c.registerPending(fmt.Sprintf("cf-%03d", i))
	}
	c.markBroken(CrashedError("test crash"))

	for i, ch := range chans {
		select {
		case rep := <-ch:
			if rep.ok || rep.err == nil || rep.err.Code != ECrashed {
				t.Fatalf("pending %d got %+v, want E_CRASHED failure", i, rep)
			}
		default:
			t.Fatalf("pending %d not notified", i)
		}
	}
	if err := c.Broken(); err == nil {
		t.Fatal("conn should report broken")
	}
	// Idempotent: second mark must not panic or double-fire.
	c.markBroken(CrashedError("again"))
}

func TestReadLimitedLine(t *testing.T) {
	cases := []struct {
		name   string
		input  string
		limit  int64
		want   string
		tooBig bool
		err    error
	}{
		{"simple", `{"a":1}` + "\n", 100, `{"a":1}`, false, nil},
		{"crlf", "x\r\n", 10, "x", false, nil},
		{"exact fit", strings.Repeat("a", 9) + "\n", 10, strings.Repeat("a", 9), false, nil},
		{"over limit single chunk", strings.Repeat("a", 20) + "\n", 10, "", true, nil},
		{"over limit multi chunk", strings.Repeat("b", 5000) + "\n", 10, "", true, nil},
		{"eof unterminated", "partial", 100, "", false, io.EOF},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			br := bufio.NewReaderSize(strings.NewReader(tc.input), 64)
			line, tooBig, err := readLimitedLine(br, tc.limit)
			if !errors.Is(err, tc.err) {
				t.Fatalf("err = %v, want %v", err, tc.err)
			}
			if tooBig != tc.tooBig {
				t.Fatalf("tooBig = %v, want %v", tooBig, tc.tooBig)
			}
			if string(line) != tc.want {
				t.Fatalf("line = %q, want %q", line, tc.want)
			}
		})
	}
}
