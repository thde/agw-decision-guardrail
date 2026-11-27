// Package logctx carries log attributes in a context, so code deeper in a
// request logs them without being passed them.
package logctx

import (
	"context"
	"log/slog"
	"slices"
)

type key struct{}

// With returns ctx carrying attrs in addition to those already in ctx.
func With(ctx context.Context, attrs ...slog.Attr) context.Context {
	prev, _ := ctx.Value(key{}).([]slog.Attr)
	return context.WithValue(ctx, key{}, append(slices.Clip(prev), attrs...))
}

// NewHandler returns a handler adding the context's attributes to each record.
func NewHandler(next slog.Handler) slog.Handler { return handler{next} }

type handler struct{ slog.Handler }

func (h handler) Handle(ctx context.Context, r slog.Record) error {
	if attrs, ok := ctx.Value(key{}).([]slog.Attr); ok {
		r = r.Clone()
		r.AddAttrs(attrs...)
	}
	return h.Handler.Handle(ctx, r) //nolint:wrapcheck // Transparent decorator.
}

func (h handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return handler{h.Handler.WithAttrs(attrs)}
}

func (h handler) WithGroup(name string) slog.Handler {
	return handler{h.Handler.WithGroup(name)}
}
