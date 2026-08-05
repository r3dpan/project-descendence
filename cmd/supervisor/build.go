package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/r3dpan/project-descendence/internal/podman"
	"github.com/r3dpan/project-descendence/internal/runtimebuild"
	"github.com/r3dpan/project-descendence/internal/store"
)

// buildPollInterval matches pollInterval (task 1.12's cadence): a runtime
// build is rarer than a run but no less latency-sensitive to the operator
// waiting on `runtime build` to finish, so there is no reason to poll it
// less often.
const buildPollInterval = 1 * time.Second

// runBuildClaimLoop is runClaimLoop's structural twin (task 4.4/4.5) over
// runtimes instead of runs. Kept as a second, parallel loop rather than
// generalizing runClaimLoop: the two claim queries, row types and execution
// steps are different enough (a build has no container to wait on, a run has
// no Containerfile to render) that sharing the loop would need an interface
// with one implementation on each side, for a codebase whose stated
// learning-value stance (CLAUDE.md conventions) is to avoid abstraction the
// task does not need. Both loops run under the same advisory lock (task
// 1.16), so there is still exactly one of each at a time.
func runBuildClaimLoop(ctx context.Context, queries *store.Queries, podmanClient *podman.Client) {
	ticker := time.NewTicker(buildPollInterval)
	defer ticker.Stop()

	for {
		claimAndExecuteAllPendingBuilds(ctx, queries, podmanClient)

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func claimAndExecuteAllPendingBuilds(ctx context.Context, queries *store.Queries, podmanClient *podman.Client) {
	for {
		runtime, err := queries.ClaimNextPendingRuntimeBuild(ctx)
		if err != nil {
			if !errors.Is(err, pgx.ErrNoRows) && ctx.Err() == nil {
				log.Printf("build claim: %v", err)
			}
			return
		}

		log.Printf("claimed runtime build %d (%s, lang=%s)", runtime.ID, runtime.Name, runtime.Lang)
		executeBuild(ctx, queries, podmanClient, runtime)
	}
}

// executeBuild renders, builds and resolves the digest for one runtime,
// mirroring executeRun's shape (render/create → run → record outcome) even
// though the steps underneath are build-specific.
func executeBuild(ctx context.Context, queries *store.Queries, podmanClient *podman.Client, runtime store.Runtime) {
	def := runtimebuild.Definition{
		BaseImage:    runtime.BaseImage,
		SysPackages:  runtime.SysPackages,
		Lang:         runtime.Lang,
		LangManifest: runtime.LangManifest.String,
	}
	tag := runtimebuild.ImageTag(def)

	contextTar, err := runtimebuild.BuildContext(def)
	if err != nil {
		failBuild(ctx, queries, runtime.ID, fmt.Sprintf("rendering build context: %v", err))
		return
	}

	if err := podmanClient.BuildImage(ctx, tag, contextTar); err != nil {
		failBuild(ctx, queries, runtime.ID, err.Error())
		return
	}

	inspect, err := podmanClient.InspectImage(ctx, tag)
	if err != nil {
		failBuild(ctx, queries, runtime.ID, fmt.Sprintf("resolving digest after a successful build: %v", err))
		return
	}
	if len(inspect.RepoDigests) == 0 {
		// A local-only build (nothing pushed) can still lack a repo digest
		// entirely depending on storage backend; fall back to the image id,
		// which is still a stable, content-addressed local identifier - just
		// not one shareable with a registry.
		markReady(ctx, queries, runtime.ID, inspect.ID)
		return
	}
	markReady(ctx, queries, runtime.ID, inspect.RepoDigests[0])
}

func markReady(ctx context.Context, queries *store.Queries, runtimeID int64, digest string) {
	if _, err := queries.MarkRuntimeReady(ctx, store.MarkRuntimeReadyParams{
		ID:          runtimeID,
		ImageDigest: pgtype.Text{String: digest, Valid: digest != ""},
	}); err != nil {
		log.Printf("build: runtime %d: marking ready: %v", runtimeID, err)
		return
	}
	log.Printf("runtime %d built successfully, digest=%s", runtimeID, digest)
}

func failBuild(ctx context.Context, queries *store.Queries, runtimeID int64, reason string) {
	log.Printf("build: runtime %d failed: %s", runtimeID, reason)
	if _, err := queries.MarkRuntimeFailed(ctx, store.MarkRuntimeFailedParams{
		ID:         runtimeID,
		BuildError: pgtype.Text{String: reason, Valid: true},
	}); err != nil {
		log.Printf("build: runtime %d: marking failed: %v", runtimeID, err)
	}
}
