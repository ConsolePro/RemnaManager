package services

import "context"

type progressKey struct{}

// WithProgress attaches a synchronous observer for safe, human-readable actions.
func WithProgress(ctx context.Context, report func(string, string)) context.Context {
	return context.WithValue(ctx, progressKey{}, report)
}

func ReportProgress(ctx context.Context, ru, en string) {
	if report, ok := ctx.Value(progressKey{}).(func(string, string)); ok {
		report(ru, en)
	}
}
