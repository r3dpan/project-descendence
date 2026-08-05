// Package runtimeprune implements the "unused" rule and the actual delete
// for runtime image pruning (task 4.7), shared between the manual API
// endpoint (internal/api's PruneRuntimesHandler) and the supervisor's
// unattended sweep (cmd/supervisor), so a manual call and the automatic one
// can never disagree about what counts as unused.
package runtimeprune

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/r3dpan/project-descendence/internal/podman"
	"github.com/r3dpan/project-descendence/internal/runtimebuild"
	"github.com/r3dpan/project-descendence/internal/store"
)

// Candidates returns built, not-yet-pruned runtimes whose image is
// unreferenced by any run started within maxAge and by any live job -
// referenced either way, a runtime is still "in use" even if it happens to
// be old.
func Candidates(ctx context.Context, queries *store.Queries, maxAge time.Duration) ([]store.Runtime, error) {
	cutoff := time.Now().Add(-maxAge)

	candidates, err := queries.ListPrunableRuntimes(ctx, pgtype.Timestamptz{Time: cutoff, Valid: true})
	if err != nil {
		return nil, fmt.Errorf("listing prunable runtimes: %w", err)
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	inUse, err := queries.ListRuntimeIDsInUseSince(ctx, pgtype.Timestamptz{Time: cutoff, Valid: true})
	if err != nil {
		return nil, fmt.Errorf("listing in-use runtimes: %w", err)
	}
	inUseSet := make(map[int64]bool, len(inUse))
	for _, id := range inUse {
		if id.Valid {
			inUseSet[id.Int64] = true
		}
	}

	unused := make([]store.Runtime, 0, len(candidates))
	for _, runtime := range candidates {
		if !inUseSet[runtime.ID] {
			unused = append(unused, runtime)
		}
	}
	return unused, nil
}

// ImageTag reconstructs the tag BuildImage built runtime under
// (runtimebuild.ImageTag of the same fields), so a delete removes the exact
// image a build produced rather than guessing a name.
func ImageTag(runtime store.Runtime) string {
	return runtimebuild.ImageTag(runtimebuild.Definition{
		BaseImage:    runtime.BaseImage,
		SysPackages:  runtime.SysPackages,
		Lang:         runtime.Lang,
		LangManifest: runtime.LangManifest.String,
	})
}

// Prune deletes runtime's image from Podman and marks the row pruned. The
// row itself is never deleted (decision #18's "row survives, bytes don't"
// pattern) - what was built, from which inputs, when, stays answerable even
// after the image is gone.
func Prune(ctx context.Context, queries *store.Queries, podmanClient *podman.Client, runtime store.Runtime) error {
	if err := podmanClient.DeleteImage(ctx, ImageTag(runtime)); err != nil {
		return err
	}
	if _, err := queries.MarkRuntimePruned(ctx, runtime.ID); err != nil {
		return fmt.Errorf("marking pruned: %w", err)
	}
	return nil
}
