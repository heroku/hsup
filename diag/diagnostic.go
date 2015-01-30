// Package diag implements a fixed-size ring buffer to store
// diagnostic text in.
//
// Similar to the "log" package, there exists a default, global Diag
// value.  Also by default "diag" will receive a copy of output sent
// by the default "log" logger.
//
// Diag priorities safety and convenience.
//
// "Convenience" is defined as being at least as easy as "log" to use,
// as to maximize the amount of instrumentation in general by
// minimizing its burden on the programmer.
//
// "Safety" is defined as:
//
//  * Being safe to call anywhere in a program.
//  * Being fast enough to call almost anywhere in a program.
//  * Data is synchronized.
//  * Storage is fixed-size.
//  * The implementation has no known bugs to spoil the instrumented
//    program.
package diag

import (
	"fmt"
	"strconv"
	"sync"
)

type Diag struct {
	l   sync.Mutex
	buf []byte
	pos int
}

func New(retentionSz int) *Diag {
	if retentionSz <= 0 {
		panic("Diag requires a zero, non-negative size.  " +
			"Size specified: " + strconv.Itoa(retentionSz))
	}
	return &Diag{buf: make([]byte, retentionSz), pos: 0}
}

func (dg *Diag) Logf(f string, rest ...interface{}) {
	dg.Log(fmt.Sprintf(f, rest...))
}

func (dg *Diag) Log(values ...interface{}) {
	dg.l.Lock()
	defer dg.l.Unlock()

	for _, v := range values[:len(values)-1] {
		dg.add(fmt.Sprintf("%v", v))
		dg.add(" ")
	}
	dg.add(fmt.Sprintf("%v", values[len(values)-1]))

	// Terminate the log record with a nul byte.
	dg.buf[dg.pos] = 0
	dg.pos++
	dg.pos %= len(dg.buf)

	// Clear the next record ahead of the cursor, so dumping
	// doesn't confuse a reader with truncated output.
	i := dg.pos
	for {
		if b := dg.buf[i]; b == 0 {
			break
		}

		dg.buf[i] = 0
		i++
		i %= len(dg.buf)
	}
}
func (dg *Diag) add(s string) {
	for i := 0; i < len(s); i++ {
		dg.buf[dg.pos] = s[i]
		dg.pos++
		dg.pos %= len(dg.buf)
	}
}

func (dg *Diag) Contents() []string {
	c := make([]byte, len(dg.buf))

	// Take a lock only long enough to make a copy of the
	// underlying data.
	dg.l.Lock()
	copy(c, dg.buf)
	tmp := Diag{buf: c, pos: dg.pos}
	dg.l.Unlock()

	return tmp.unsyncRecords()
}

func (dg *Diag) Write(p []byte) (n int, err error) {
	dg.Log(string(p))
	return len(p), nil
}

func (dg *Diag) unsyncRecords() []string {
	var out []string
	var accum []byte
	i := dg.pos
	for {
		if b := dg.buf[i]; b == 0 {
			if len(accum) > 0 {
				out = append(out, string(accum))
				accum = accum[:0]
			}
		} else {
			accum = append(accum, b)
		}

		i++
		i %= len(dg.buf)
		if i == dg.pos {
			break
		}
	}

	return out
}
