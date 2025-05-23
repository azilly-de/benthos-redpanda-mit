// Copyright 2025 Redpanda Data, Inc.

package pure

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/redpanda-data/benthos/v4/internal/bundle"
	"github.com/redpanda-data/benthos/v4/internal/component"
	"github.com/redpanda-data/benthos/v4/internal/component/interop"
	"github.com/redpanda-data/benthos/v4/internal/component/processor"
	"github.com/redpanda-data/benthos/v4/internal/component/ratelimit"
	"github.com/redpanda-data/benthos/v4/internal/message"
	"github.com/redpanda-data/benthos/v4/public/service"
)

const (
	rlimitFieldResource = "resource"
)

func rlimitProcSpec() *service.ConfigSpec {
	return service.NewConfigSpec().
		Categories("Utility").
		Stable().
		Summary(`Throttles the throughput of a pipeline according to a specified ` + "xref:components:rate_limits/about.adoc[`rate_limit`]" + ` resource. Rate limits are shared across components and therefore apply globally to all processing pipelines.`).
		Field(service.NewStringField(rlimitFieldResource).
			Description("The target xref:components:rate_limits/about.adoc[`rate_limit` resource]."))
}

func init() {
	service.MustRegisterBatchProcessor(
		"rate_limit", rlimitProcSpec(),
		func(conf *service.ParsedConfig, res *service.Resources) (service.BatchProcessor, error) {
			resStr, err := conf.FieldString(rlimitFieldResource)
			if err != nil {
				return nil, err
			}

			mgr := interop.UnwrapManagement(res)
			r, err := newRateLimitProc(resStr, mgr)
			if err != nil {
				return nil, err
			}
			return interop.NewUnwrapInternalBatchProcessor(processor.NewAutoObservedProcessor("rate_limit", r, mgr)), nil
		})

}

type rateLimitProc struct {
	rlName string
	mgr    bundle.NewManagement

	closeChan chan struct{}
	closeOnce sync.Once
}

func newRateLimitProc(resStr string, mgr bundle.NewManagement) (*rateLimitProc, error) {
	if !mgr.ProbeRateLimit(resStr) {
		return nil, fmt.Errorf("rate limit resource '%v' was not found", resStr)
	}
	r := &rateLimitProc{
		rlName:    resStr,
		mgr:       mgr,
		closeChan: make(chan struct{}),
	}
	return r, nil
}

func (r *rateLimitProc) Process(ctx context.Context, msg *message.Part) ([]*message.Part, error) {
	for {
		var waitFor time.Duration
		var err error
		if rerr := r.mgr.AccessRateLimit(ctx, r.rlName, func(rl ratelimit.V1) {
			waitFor, err = rl.Access(ctx)
		}); rerr != nil {
			err = rerr
		}
		if ctx.Err() != nil {
			return nil, err
		}
		if err != nil {
			r.mgr.Logger().Error("Failed to access rate limit: %v", err)
			waitFor = time.Second
		}
		if waitFor == 0 {
			return []*message.Part{msg}, nil
		}
		select {
		case <-time.After(waitFor):
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-r.closeChan:
			return nil, component.ErrTypeClosed
		}
	}
}

func (r *rateLimitProc) Close(ctx context.Context) error {
	r.closeOnce.Do(func() {
		close(r.closeChan)
	})
	return nil
}
