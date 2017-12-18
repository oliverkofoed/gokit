package dbkit

import "context"

type CockroachDBBatch interface {
	ExecuteCockroachDB(ctx context.Context, noUpdateConflict bool, returningNothing bool) error
}
