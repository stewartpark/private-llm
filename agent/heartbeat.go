package main

import (
	"context"
	"log"
	"time"

	"cloud.google.com/go/firestore"
)

const firestoreDatabase = "private-llm"

var activityCh chan struct{}

// notifyActivity signals that a request was received (non-blocking).
func notifyActivity() {
	select {
	case activityCh <- struct{}{}:
	default:
	}
}

// startHeartbeat runs a background loop that updates Firestore when there's activity,
// coalescing rapid requests into a single write per minute.
func startHeartbeat(ctx context.Context) {
	activityCh = make(chan struct{}, 1)

	go func() {
		client, err := firestore.NewClientWithDatabase(ctx, cfg.ProjectID, firestoreDatabase)
		if err != nil {
			log.Printf("[heartbeat] failed to create Firestore client: %v", err)
			return
		}
		defer client.Close()

		docRef := client.Collection("vm_state").Doc(cfg.VMName)

		for {
			select {
			case <-ctx.Done():
				return
			case <-activityCh:
				// Got activity — write timestamp
				now := time.Now().Unix()
				_, err := docRef.Set(ctx, map[string]interface{}{
					"last_request_unix": now,
				}, firestore.MergeAll)
				if err != nil {
					if ctx.Err() != nil {
						return
					}
					log.Printf("[heartbeat] failed to update Firestore: %v", err)
				} else {
					log.Printf("[heartbeat] updated timestamp to %d", now)
				}

				// Coalesce: ignore further signals for 1 minute
				timer := time.NewTimer(1 * time.Minute)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
		}
	}()
}
