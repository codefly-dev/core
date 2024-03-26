package runners

import "context"

type Runner interface {
	Init(ctx context.Context) error
	Run(ctx context.Context) error
	Start(ctx context.Context) error
	Stop() error
}
