//go:build localjournal

package journalintegration

import (
	"context"
	"encoding/binary"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func pgFrame(c net.Conn) (byte, []byte, error) {
	var h [5]byte
	if _, e := io.ReadFull(c, h[:]); e != nil {
		return 0, nil, e
	}
	n := int(binary.BigEndian.Uint32(h[1:]))
	if n < 4 || n > 1<<20 {
		return 0, nil, errors.New("bad synthetic PG frame")
	}
	b := make([]byte, n-4)
	_, e := io.ReadFull(c, b)
	return h[0], b, e
}
func pgWrite(c net.Conn, typ byte, b []byte) error {
	h := make([]byte, 5+len(b))
	h[0] = typ
	binary.BigEndian.PutUint32(h[1:], uint32(4+len(b)))
	copy(h[5:], b)
	_, e := c.Write(h)
	return e
}

// Private Unix-only proxy drops the first real server COMMIT acknowledgement,
// then accepts new connections so the application's own resolver can run.
func commitProxy(t *testing.T, socket string) (string, func()) {
	return commitProxyMode(t, socket, nil)
}

func commitProxyMode(t *testing.T, socket string, rollbackBeforeClose func()) (string, func()) {
	t.Helper()
	dir, e := os.MkdirTemp("/tmp", "sub2usage-journal-proxy-")
	check(t, e)
	check(t, os.Chmod(dir, 0700))
	listener, e := net.Listen("unix", filepath.Join(dir, ".s.PGSQL.54473"))
	check(t, e)
	var dropped atomic.Bool
	var rollbackSelected atomic.Bool
	var wg sync.WaitGroup
	var mu sync.Mutex
	connections := map[net.Conn]bool{}
	var once sync.Once
	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)
		for {
			client, e := listener.Accept()
			if e != nil {
				return
			}
			mu.Lock()
			connections[client] = true
			mu.Unlock()
			wg.Add(1)
			go func(c net.Conn) {
				defer wg.Done()
				defer c.Close()
				defer func() { mu.Lock(); delete(connections, c); mu.Unlock() }()
				up, e := net.Dial("unix", filepath.Join(socket, ".s.PGSQL.54472"))
				if e != nil {
					return
				}
				defer up.Close()
				mu.Lock()
				connections[up] = true
				mu.Unlock()
				defer func() { mu.Lock(); delete(connections, up); mu.Unlock() }()
				c.SetDeadline(time.Now().Add(20 * time.Second))
				up.SetDeadline(time.Now().Add(20 * time.Second))
				var header [4]byte
				if _, e = io.ReadFull(c, header[:]); e != nil {
					return
				}
				n := int(binary.BigEndian.Uint32(header[:]))
				if n < 8 || n > 1<<20 {
					return
				}
				startup := make([]byte, n-4)
				if _, e = io.ReadFull(c, startup); e != nil {
					return
				}
				if _, e = up.Write(append(header[:], startup...)); e != nil {
					return
				}
				var committing atomic.Bool
				var rollback atomic.Bool
				forwardDone := make(chan struct{})
				go func() {
					defer close(forwardDone)
					for {
						typ, b, e := pgFrame(c)
						if e != nil {
							up.Close()
							return
						}
						if typ == 'Q' && strings.EqualFold(strings.TrimSpace(strings.TrimSuffix(string(b), "\x00")), "COMMIT") {
							committing.Store(true)
							if rollbackBeforeClose != nil && rollbackSelected.CompareAndSwap(false, true) {
								rollback.Store(true)
								b = []byte("ROLLBACK\x00")
							}
						}
						if pgWrite(up, typ, b) != nil {
							return
						}
					}
				}()
				defer func() { c.Close(); up.Close(); <-forwardDone }()
				for {
					typ, b, e := pgFrame(up)
					if e != nil {
						return
					}
					if typ == 'C' && strings.HasPrefix(string(b), "COMMIT") && committing.Load() && dropped.CompareAndSwap(false, true) {
						return
					}
					if typ == 'C' && rollback.Load() && strings.HasPrefix(string(b), "ROLLBACK") {
						rollbackBeforeClose()
						dropped.Store(true)
						return
					}
					if pgWrite(c, typ, b) != nil {
						return
					}
				}
			}(client)
		}
	}()
	stop := func() {
		once.Do(func() {
			listener.Close()
			<-acceptDone
			mu.Lock()
			for c := range connections {
				c.Close()
			}
			mu.Unlock()
			wg.Wait()
			os.RemoveAll(dir)
		})
	}
	t.Cleanup(func() {
		stop()
		if !dropped.Load() {
			t.Error("real COMMIT ACK not dropped")
		}
	})
	return dir, stop
}

func TestJournalUnknownBillingCommitActualResolver(t *testing.T) {
	f := newFixture(t)
	dir, stop := commitProxy(t, f.socket)
	db := connect(t, f.schema, dir, 54473)
	defer db.Close()
	defer stop()
	key := "synthetic-billing-unknown"
	check(t, f.gateway(t, key, false, repository.NewUsageBillingRepository(nil, db)))
	f.assertOnce(t, key, false)
	check(t, f.gateway(t, key, false, nil))
	f.assertOnce(t, key, false)
	_, e := repository.NewUsageJournalRepository(f.db).ProcessNext(context.Background())
	check(t, e)
	f.assertOnce(t, key, true)
	note(t, map[string]any{"real_server_billing_COMMIT_ACK_dropped": true, "actual_repository_fresh_context_resolved": true, "same_key_retry_no_second_debit": true})
}

func TestJournalUnknownWorkerCommitActualResolver(t *testing.T) {
	f := newFixture(t)
	key := "synthetic-worker-unknown"
	check(t, f.gateway(t, key, false, nil))
	dir, stop := commitProxy(t, f.socket)
	db := connect(t, f.schema, dir, 54473)
	defer db.Close()
	defer stop()
	worked, e := repository.NewUsageJournalRepository(db).ProcessNext(context.Background())
	check(t, e)
	if !worked {
		t.Fatal("worker did not claim")
	}
	f.assertOnce(t, key, true)
	worked, e = repository.NewUsageJournalRepository(f.db).ProcessNext(context.Background())
	check(t, e)
	if worked {
		t.Fatal("done was delivered twice")
	}
	f.assertOnce(t, key, true)
	note(t, map[string]any{"real_server_worker_COMMIT_ACK_dropped": true, "actual_done_state_resolved": true, "usage_rows": 1, "billing_replayed": false})
}

type captureApplied struct {
	service.UsageBillingRepository
	observed chan bool
}

func (c captureApplied) Apply(ctx context.Context, cmd *service.UsageBillingCommand) (*service.UsageBillingApplyResult, error) {
	r, e := c.UsageBillingRepository.Apply(ctx, cmd)
	if e == nil && r != nil {
		c.observed <- r.Applied
	}
	return r, e
}

func TestJournalRollbackCommitCompetitionOnlyWinnerFinalizes(t *testing.T) {
	f := newFixture(t)
	key := "synthetic-commit-competition"
	winner := make(chan bool, 1)
	loser := make(chan bool, 1)
	winnerErr := make(chan error, 1)
	dir, stop := commitProxyMode(t, f.socket, func() {
		winnerErr <- f.gateway(t, key, false, captureApplied{repository.NewUsageBillingRepository(nil, f.db), winner})
	})
	defer stop()
	db := connect(t, f.schema, dir, 54473)
	defer db.Close()
	check(t, f.gateway(t, key, false, captureApplied{repository.NewUsageBillingRepository(nil, db), loser}))
	check(t, <-winnerErr)
	if !<-winner || <-loser {
		t.Fatal("only the actual committing attempt may own post-commit effects")
	}
	f.assertOnce(t, key, false)
	_, e := repository.NewUsageJournalRepository(f.db).ProcessNext(context.Background())
	check(t, e)
	f.assertOnce(t, key, true)
	note(t, map[string]any{"real_wire_first_COMMIT_replaced_with_ROLLBACK": true, "competing_actual_gateway_commit": true, "original_unknown_call_applied": false, "winner_applied": true, "post_commit_owner_count": 1, "debit_once": true})
}
