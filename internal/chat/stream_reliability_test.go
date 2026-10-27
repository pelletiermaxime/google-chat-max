package chat

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestRepeatedRenewalAndReconnectAfterOverdueLease(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var renewals atomic.Int32
	attempts, reconciles, reconnects := 0, 0, 0
	err := runStream(ctx, time.Now().Add(-time.Hour), func(ctx context.Context) error {
		attempts++
		if attempts < 3 {
			return errors.New("simulated transport outage")
		}
		<-ctx.Done()
		return ctx.Err()
	}, func(context.Context) (time.Time, error) {
		if renewals.Add(1) == 4 {
			cancel()
		}
		// An overdue deadline exercises the same timer path used after the
		// process resumes late, while keeping this runtime check bounded.
		return time.Now().Add(-time.Hour), nil
	}, func(event StreamEvent) bool {
		if event.Reconcile {
			reconciles++
		}
		if event.Status == "Reconnecting" {
			reconnects++
		}
		return true
	})
	if err != nil || renewals.Load() != 4 || attempts != 3 || reconciles != 3 || reconnects != 2 {
		t.Fatalf("renew/reconnect failed: renewals=%d attempts=%d reconciles=%d reconnects=%d err=%v", renewals.Load(), attempts, reconciles, reconnects, err)
	}
}

func TestNonRetryableStreamFailuresStopWithoutReconnectLoop(t *testing.T) {
	for _, code := range []codes.Code{codes.PermissionDenied, codes.Unauthenticated, codes.NotFound, codes.InvalidArgument} {
		t.Run(code.String(), func(t *testing.T) {
			attempts, reconnects := 0, 0
			err := runStream(context.Background(), time.Now().Add(15*time.Minute), func(context.Context) error {
				attempts++
				return status.Error(code, "simulated terminal failure")
			}, func(context.Context) (time.Time, error) {
				t.Error("terminal failure unexpectedly renewed")
				return time.Time{}, nil
			}, func(event StreamEvent) bool {
				if event.Status == "Reconnecting" {
					reconnects++
				}
				return true
			})
			if err == nil || attempts != 1 || reconnects != 0 {
				t.Fatalf("terminal error retried: attempts=%d reconnects=%d err=%v", attempts, reconnects, err)
			}
		})
	}
}
