// Copyright 2025 Redpanda Data, Inc.

package pure

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/cenkalti/backoff/v4"

	"github.com/redpanda-data/benthos/v4/internal/component/interop"
	"github.com/redpanda-data/benthos/v4/internal/component/processor"
	"github.com/redpanda-data/benthos/v4/internal/log"
	"github.com/redpanda-data/benthos/v4/internal/message"
	"github.com/redpanda-data/benthos/v4/public/service"
)

const (
	rpFieldProcessors = "processors"
	rpFieldBackoff    = "backoff"
	rpFieldParallel   = "parallel"
	rpFieldMaxRetries = "max_retries"
)

func retryProcSpec() *service.ConfigSpec {
	return service.NewConfigSpec().
		Beta().
		Categories("Composition").
		Version("4.27.0").
		Summary(`Attempts to execute a series of child processors until success.`).
		Description(`
Executes child processors and if a resulting message is errored then, after a specified backoff period, the same original message will be attempted again through those same processors. If the child processors result in more than one message then the retry mechanism will kick in if _any_ of the resulting messages are errored.

It is important to note that any mutations performed on the message during these child processors will be discarded for the next retry, and therefore it is safe to assume that each execution of the child processors will always be performed on the data as it was when it first reached the retry processor.

By default the retry backoff has a specified `+"<<backoffmax_elapsed_time,`max_elapsed_time`>>"+`, if this time period is reached during retries and an error still occurs these errored messages will proceed through to the next processor after the retry (or your outputs). Normal xref:configuration:error_handling.adoc[error handling patterns] can be used on these messages.

In order to avoid permanent loops any error associated with messages as they first enter a retry processor will be cleared.

== Metadata

This processor adds the following metadata fields to each message:

`+"```text"+`
- retry_count - The number of retry attempts.
- backoff_duration - The total time elapsed while performing retries.
`+"```"+`

[CAUTION]
.Batching
====
If you wish to wrap a batch-aware series of processors then take a look at the <<batching, batching section>>.
====
`).
		Footnotes(`
== Batching

When messages are batched the child processors of a `+"retry"+` are executed for each individual message in isolation, performed serially by default but in parallel when the field `+"<<parallel, `parallel`>> is set to `true`"+`. This is an intentional limitation of the retry processor and is done in order to ensure that errors are correctly associated with a given input message. Otherwise, the archiving, expansion, grouping, filtering and so on of the child processors could obfuscate this relationship.

If the target behavior of your retried processors is "batch aware", in that you wish to perform some processing across the entire batch of messages and repeat it in the event of errors, you can use an `+"xref:components:processors/archive.adoc[`archive` processor]"+` to collapse the batch into an individual message. Then, within these child processors either perform your batch aware processing on the archive, or use an `+"xref:components:processors/unarchive.adoc[`unarchive` processor]"+` in order to expand the single message back out into a batch.

For example, if the retry processor were being used to wrap an HTTP request where the payload data is a batch archived into a JSON array it should look something like this:

`+"```yaml"+`
pipeline:
  processors:
    - archive:
        format: json_array
    - retry:
        processors:
          - http:
              url: example.com/nope
              verb: POST
    - unarchive:
        format: json_array
`+"```"+`
`).
		Example("Stop ignoring me Taz", `
Here we have a config where I generate animal noises and send them to Taz via HTTP. Taz has a tendency to stop his servers whenever I dispatch my animals upon him, and therefore these HTTP requests sometimes fail. However, I have the retry processor and with this super power I can specify a back off policy and it will ensure that for each animal noise the HTTP processor is attempted until either it succeeds or my Redpanda Connect instance is stopped.

I even go as far as to zero-out the maximum elapsed time field, which means that for each animal noise I will wait indefinitely, because I really really want Taz to receive every single animal noise that he is entitled to.`,
			`
input:
  generate:
    interval: 1s
    mapping: 'root.noise = [ "woof", "meow", "moo", "quack" ].index(random_int(min: 0, max: 3))'

pipeline:
  processors:
    - retry:
        backoff:
          initial_interval: 100ms
          max_interval: 5s
          max_elapsed_time: 0s
        processors:
          - http:
              url: 'http://example.com/try/not/to/dox/taz'
              verb: POST

output:
  # Drop everything because it's junk data, I don't want it lol
  drop: {}
`,
		).
		Fields(
			service.NewBackOffField(rpFieldBackoff, true, nil),
			service.NewProcessorListField(rpFieldProcessors).
				Description("A list of xref:components:processors/about.adoc[processors] to execute on each message."),
			service.NewBoolField(rpFieldParallel).
				Description("When processing batches of messages these batches are ignored and the processors apply to each message sequentially. However, when this field is set to `true` each message will be processed in parallel. Caution should be made to ensure that batch sizes do not surpass a point where this would cause resource (CPU, memory, API limits) contention.").
				Default(false),
			service.NewIntField(rpFieldMaxRetries).
				Description("The maximum number of retry attempts before the request is aborted. Setting this value to `0` will result in unbounded number of retries.").
				Default(0),
		)
}

func init() {
	service.MustRegisterBatchProcessor(
		"retry", retryProcSpec(),
		func(conf *service.ParsedConfig, res *service.Resources) (service.BatchProcessor, error) {
			mgr := interop.UnwrapManagement(res)
			p := &retryProc{
				log: mgr.Logger(),
			}

			procList, err := conf.FieldProcessorList(rpFieldProcessors)
			if err != nil {
				return nil, err
			}
			if len(procList) == 0 {
				return nil, errors.New("at least one child processor must be specified")
			}
			for _, tmp := range procList {
				p.children = append(p.children, interop.UnwrapOwnedProcessor(tmp))
			}

			if p.boff, err = conf.FieldBackOff(rpFieldBackoff); err != nil {
				return nil, err
			}

			if p.parallel, err = conf.FieldBool(rpFieldParallel); err != nil {
				return nil, err
			}

			if p.maxRetries, err = conf.FieldInt(rpFieldMaxRetries); err != nil {
				return nil, err
			}

			return interop.NewUnwrapInternalBatchProcessor(processor.NewAutoObservedBatchedProcessor("retry", p, mgr)), nil
		})
}

type retryProc struct {
	children   []processor.V1
	boff       *backoff.ExponentialBackOff
	parallel   bool
	maxRetries int
	log        log.Modular
}

func (r *retryProc) ProcessBatch(ctx *processor.BatchProcContext, msgs message.Batch) ([]message.Batch, error) {
	var resMsg message.Batch
	if r.parallel {
		resBatches := make([][]message.Batch, len(msgs))

		var wg sync.WaitGroup
		wg.Add(len(msgs))

		for i, tmp := range msgs {
			go func(index int, p *message.Part) {
				defer wg.Done()
				var err error
				if resBatches[index], err = r.dispatchMessage(ctx.Context(), p); err != nil {
					return
				}
			}(i, tmp)
		}

		wg.Wait()
		if err := ctx.Context().Err(); err != nil {
			return nil, err
		}

		for _, batches := range resBatches {
			for _, batch := range batches {
				resMsg = append(resMsg, batch...)
			}
		}
	} else {
		for _, p := range msgs {
			tmp, err := r.dispatchMessage(ctx.Context(), p)
			if err != nil {
				return nil, err
			}
			for _, b := range tmp {
				resMsg = append(resMsg, b...)
			}
		}
	}
	return []message.Batch{resMsg}, nil
}

func (r *retryProc) dispatchMessage(ctx context.Context, p *message.Part) (resBatches []message.Batch, err error) {
	// NOTE: We always ensure we start off with a copy of the reference backoff.
	boff := *r.boff
	boff.Reset()

	retries := 0
	var backoffDuration time.Duration

	defer func() {
		for _, b := range resBatches {
			for _, m := range b {
				m.MetaSetMut("retry_count", retries)
				m.MetaSetMut("backoff_duration", backoffDuration)
			}
		}
	}()

	// Ensure we do not start off with an error.
	p.ErrorSet(nil)

	for {
		resBatches, err = processor.ExecuteAll(ctx, r.children, message.Batch{p.ShallowCopy()})
		if err != nil {
			return nil, err
		}

		hasFailed := false

	errorChecks:
		for _, b := range resBatches {
			for _, m := range b {
				if m.ErrorGet() != nil {
					hasFailed = true
					break errorChecks
				}
			}
		}

		if !hasFailed {
			return resBatches, nil
		}

		retries++
		if retries == r.maxRetries {
			r.log.With("error", err).Debug("Error occurred and maximum number of retries was reached.")
			return resBatches, nil
		}

		nextSleep := boff.NextBackOff()
		backoffDuration += nextSleep
		if nextSleep == backoff.Stop {
			r.log.With("error", err).Debug("Error occurred and maximum wait period was reached.")
			return resBatches, nil
		}

		r.log.With("error", err, "backoff", nextSleep).Debug("Error occurred, sleeping for next backoff period.")
		select {
		case <-time.After(nextSleep):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (r *retryProc) Close(ctx context.Context) error {
	for _, c := range r.children {
		if err := c.Close(ctx); err != nil {
			return err
		}
	}
	return nil
}
