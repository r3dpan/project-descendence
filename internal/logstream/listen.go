package logstream

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
)

// reconnectDelay is how long to wait before re-establishing the listening
// connection after it drops. Long enough not to hammer a database that is
// down or restarting, short enough that a restart costs a moment of latency
// rather than a visible outage.
const reconnectDelay = 2 * time.Second

// Listen opens one connection to databaseURL, LISTENs on Channel, and
// republishes everything it receives to broker, reconnecting on failure until
// ctx is cancelled.
//
// One connection for the whole process, not one per streaming client: a
// connection parked in WaitForNotification cannot serve queries, so a
// connection per subscriber would let a handful of open browser tabs exhaust
// the pool.
//
// It is a connection of its own rather than one borrowed from the pool - the
// pattern the advisory lock uses (task 1.16) - because a listening connection
// must never go back into general circulation still subscribed. Owning it
// outright means dropping it is enough; there is no UNLISTEN to get right on
// every error path.
//
// Notifications sent while it is reconnecting are lost, with no way to detect
// it. That is not a flaw to fix here: it is why events are watermarks and why
// subscribers poll on a slow timer regardless. Making this exactly-once would
// mean rebuilding a durable queue on top of a mechanism that deliberately
// isn't one.
func Listen(ctx context.Context, databaseURL string, broker *Broker) {
	for ctx.Err() == nil {
		if err := listenOnce(ctx, databaseURL, broker); err != nil && ctx.Err() == nil {
			log.Printf("logstream: listener stopped (%v); reconnecting in %s", err, reconnectDelay)

			select {
			case <-ctx.Done():
			case <-time.After(reconnectDelay):
			}
		}
	}
}

// listenOnce runs a single connection's lifetime: connect, LISTEN, then relay
// notifications until something fails.
func listenOnce(ctx context.Context, databaseURL string, broker *Broker) error {
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer func() {
		// A fresh context: ctx is usually already cancelled by the time this
		// runs, and the connection should still be closed rather than leaked.
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := conn.Close(closeCtx); err != nil {
			log.Printf("logstream: closing the listening connection: %v", err)
		}
	}()

	if _, err := conn.Exec(ctx, "LISTEN "+Channel); err != nil {
		return err
	}

	log.Printf("logstream: listening on %q", Channel)

	for {
		notification, err := conn.WaitForNotification(ctx)
		if err != nil {
			return err
		}

		event, err := ParseEvent(notification.Payload)
		if err != nil {
			// A payload this process cannot read is not a reason to tear the
			// listener down - far more likely a newer supervisor sending a
			// field this build doesn't know than a real fault.
			log.Printf("logstream: %v", err)
			continue
		}

		broker.Publish(event)
	}
}
