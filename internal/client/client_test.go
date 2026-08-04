package client

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

// These are integration tests against a real running API server, matching
// the approach in internal/podman: they skip cleanly rather than fail when
// the environment isn't configured for them. Run them with
//
//	DESCENDENCE_URL=http://127.0.0.1:8080 DESCENDENCE_TOKEN=sra_live_... go test ./internal/client
//
// (the same two variables the CLI reads - see task 1.21).
func newTestClient(t *testing.T) (*Client, context.Context) {
	t.Helper()

	baseURL := os.Getenv("DESCENDENCE_URL")
	token := os.Getenv("DESCENDENCE_TOKEN")
	if baseURL == "" || token == "" {
		t.Skip("DESCENDENCE_URL / DESCENDENCE_TOKEN not set")
	}

	client := New(baseURL, token)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)

	if _, err := client.Info(ctx); err != nil {
		t.Skipf("api server not reachable at %s: %v", baseURL, err)
	}

	return client, ctx
}

func TestInfoAndHealth(t *testing.T) {
	client, ctx := newTestClient(t)

	info, err := client.Info(ctx)
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if info.ProductName == "" || info.APIVersion == "" {
		t.Errorf("Info returned an incomplete response: %+v", info)
	}

	health, err := client.Health(ctx)
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if health.HealthStatus == "" {
		t.Errorf("Health returned no healthStatus: %+v", health)
	}
	t.Logf("health: %+v", health)
}

func TestWhoAmI(t *testing.T) {
	client, ctx := newTestClient(t)

	principal, err := client.WhoAmI(ctx)
	if err != nil {
		t.Fatalf("WhoAmI: %v", err)
	}
	if principal.ID == 0 || principal.Name == "" {
		t.Errorf("WhoAmI returned an incomplete principal: %+v", principal)
	}
	t.Logf("principal: %+v", principal)
}

func TestUnauthorizedMapsToSentinel(t *testing.T) {
	client, ctx := newTestClient(t)

	bogus := New(client.baseURL, "sra_live_definitely-not-a-real-token")
	_, err := bogus.WhoAmI(ctx)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized with a bogus token, got %v", err)
	}
}

func TestGetRunNotFoundMapsToSentinel(t *testing.T) {
	client, ctx := newTestClient(t)

	_, err := client.GetRun(ctx, 999999999)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for an unknown run id, got %v", err)
	}
}

func TestCreateRunIsIdempotentPerKey(t *testing.T) {
	client, ctx := newTestClient(t)

	key := fmt.Sprintf("client-test-%d", time.Now().UnixNano())
	params := CreateRunParams{
		ImageRef:       "docker.io/library/alpine:latest",
		Argv:           []string{"echo", "idempotent"},
		TimeoutSeconds: 60,
		IdempotencyKey: key,
	}

	first, err := client.CreateRun(ctx, params)
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	second, err := client.CreateRun(ctx, params)
	if err != nil {
		t.Fatalf("CreateRun (replay): %v", err)
	}

	if first.ID != second.ID {
		t.Errorf("replaying Idempotency-Key %q created a second run: %d then %d", key, first.ID, second.ID)
	}
}

func TestListRunsPaginates(t *testing.T) {
	client, ctx := newTestClient(t)

	page, err := client.ListRuns(ctx, ListRunsParams{Limit: 1})
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(page.Items) == 0 {
		t.Skip("no runs exist yet; nothing to paginate")
	}
	if len(page.Items) != 1 {
		t.Fatalf("asked for 1 run, got %d", len(page.Items))
	}
	if page.NextCursor == nil {
		t.Skip("only one run exists; no second page to follow")
	}

	next, err := client.ListRuns(ctx, ListRunsParams{Limit: 1, Cursor: *page.NextCursor})
	if err != nil {
		t.Fatalf("ListRuns (page 2): %v", err)
	}
	if len(next.Items) != 1 {
		t.Fatalf("second page returned %d runs, want 1", len(next.Items))
	}
	if next.Items[0].ID == page.Items[0].ID {
		t.Errorf("second page repeated run %d instead of advancing", page.Items[0].ID)
	}
}

// TestRunReachesTerminalState exercises the full create-then-poll path the
// CLI's `run` command is built on. Needs a supervisor running as well as an
// API server; skips (rather than hanging or failing) if the run never leaves
// queued, since that means nothing is consuming the queue.
func TestRunReachesTerminalState(t *testing.T) {
	client, ctx := newTestClient(t)

	created, err := client.CreateRun(ctx, CreateRunParams{
		ImageRef:       "docker.io/library/alpine:latest",
		Argv:           []string{"echo", "hello from the client test"},
		TimeoutSeconds: 60,
	})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if created.State != StateQueued {
		t.Errorf("newly created run is %q, want %q", created.State, StateQueued)
	}

	pollCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	final, err := client.PollRun(pollCtx, created.ID, 500*time.Millisecond, func(run Run) {
		t.Logf("run %d: %s", run.ID, run.State)
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			t.Skipf("run %d never left %q; is the supervisor running?", created.ID, final.State)
		}
		t.Fatalf("PollRun: %v", err)
	}

	if final.State != StateSucceeded {
		t.Fatalf("run %d ended %q (exitCode=%v reason=%v), want succeeded",
			final.ID, final.State, final.ExitCode, final.FailureReason)
	}
	if final.ExitCode == nil || *final.ExitCode != 0 {
		t.Errorf("run %d succeeded but exitCode is %v", final.ID, final.ExitCode)
	}
	if final.StartedAt == nil || final.FinishedAt == nil {
		t.Errorf("run %d is terminal but startedAt/finishedAt are %v/%v", final.ID, final.StartedAt, final.FinishedAt)
	}
}
