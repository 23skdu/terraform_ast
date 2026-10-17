package ast

import (
	"context"
	"fmt"
	"runtime"

	"golang.org/x/sync/errgroup"
)

// InspectModulesParallel inspects multiple module directories concurrently using a bounded worker pool.
func InspectModulesParallel(ctx context.Context, dirs []string, concurrency int) ([]*ModuleInfo, error) {
	if len(dirs) == 0 {
		return nil, nil
	}
	if concurrency <= 0 {
		concurrency = runtime.GOMAXPROCS(0)
	}

	results := make([]*ModuleInfo, len(dirs))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(concurrency)

	for i, dir := range dirs {
		idx := i
		targetDir := dir
		g.Go(func() error {
			select {
			case <-gctx.Done():
				return gctx.Err()
			default:
			}
			info, err := Inspect(targetDir)
			if err != nil {
				return fmt.Errorf("parallel inspect failed for %s: %w", targetDir, err)
			}
			results[idx] = info
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}
	return results, nil
}
