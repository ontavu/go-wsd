// Copyright (C) 2022-2026 Jean-Francois SMIGIELSKI
//
// SPDX-License-Identifier: MIT

package wsd

import (
	"context"
	"net"
	"os"
	"sync"
	"testing"
	"time"
)

// blockingConn parks in ReadFrom until its read deadline is in the past, which is what a
// real socket does and what fakeConn cannot: fakeConn records deadlines and honours none,
// so its reader is never actually blocked when the context ends.
//
// That is why this file exists. The 2026-09-11 audit deleted readReplies' whole watchdog
// and the entire wsd package stayed green — TestReadRepliesHonoursCancelledContext
// included, which is named for the mechanism it does not reach. Moving a read deadline
// into the past from another goroutine is the only way to wake a reader parked in
// ReadFrom, it is why ci.yml runs the race detector, and until these two tests it was
// covered by nothing.
type blockingConn struct {
	mu       sync.Mutex
	deadline time.Time
	once     sync.Once
	reading  chan struct{} // closed once ReadFrom has parked
}

func newBlockingConn() *blockingConn {
	return &blockingConn{reading: make(chan struct{})}
}

func (c *blockingConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deadline = t
	return nil
}

func (c *blockingConn) expired() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.deadline.IsZero() && !time.Now().Before(c.deadline)
}

func (c *blockingConn) ReadFrom([]byte) (int, net.Addr, error) {
	c.once.Do(func() { close(c.reading) })
	for !c.expired() {
		time.Sleep(time.Millisecond)
	}
	return 0, nil, os.ErrDeadlineExceeded
}

func (c *blockingConn) WriteTo(b []byte, _ net.Addr) (int, error) { return len(b), nil }
func (c *blockingConn) Close() error                              { return nil }

// TestReadRepliesUnblocksABlockedReadOnCancel pins what the watchdog is for: a caller that
// cancels mid-window must not wait out the rest of it.
func TestReadRepliesUnblocksABlockedReadOnCancel(t *testing.T) {
	conn := newBlockingConn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := readReplies(ctx, conn, 30*time.Second, "")
		done <- err
	}()

	<-conn.reading // the reader is parked; only the deadline can wake it
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("readReplies: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a cancelled context left the reader parked in ReadFrom: nothing moved the " +
			"read deadline, so collection ran for the whole window")
	}
}

// TestReadRepliesDoesNotLoseAnAlreadyCancelledContext is the same guard for the context
// that was already done when the window opened. This is the case the watchdog's placement
// exists for: started above the SetReadDeadline call, it would fire first and have its
// past deadline overwritten by the collection deadline, and the window would run in full.
func TestReadRepliesDoesNotLoseAnAlreadyCancelledContext(t *testing.T) {
	conn := newBlockingConn()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan error, 1)
	go func() {
		_, err := readReplies(ctx, conn, 30*time.Second, "")
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("readReplies: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a context cancelled before the window opened waited the whole window out")
	}
}
