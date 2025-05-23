// Copyright 2025 Redpanda Data, Inc.

package manager_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/redpanda-data/benthos/v4/internal/bundle"
	"github.com/redpanda-data/benthos/v4/internal/component"
	"github.com/redpanda-data/benthos/v4/internal/component/cache"
	"github.com/redpanda-data/benthos/v4/internal/component/input"
	"github.com/redpanda-data/benthos/v4/internal/component/output"
	"github.com/redpanda-data/benthos/v4/internal/component/processor"
	"github.com/redpanda-data/benthos/v4/internal/component/ratelimit"
	"github.com/redpanda-data/benthos/v4/internal/component/testutil"
	"github.com/redpanda-data/benthos/v4/internal/docs"
	"github.com/redpanda-data/benthos/v4/internal/manager"
	"github.com/redpanda-data/benthos/v4/internal/message"

	_ "github.com/redpanda-data/benthos/v4/public/components/pure"
)

var _ bundle.NewManagement = &manager.Type{}

func TestManagerProcessorLabels(t *testing.T) {
	t.Skip("No longer validating labels at construction")

	goodLabels := []string{
		"foo",
		"foo_bar",
		"foo_bar_baz_buz",
		"foo__",
		"foo123__45",
	}
	for _, l := range goodLabels {
		conf := processor.NewConfig()
		conf.Type = "bloblang"
		conf.Plugin = "root = this"
		conf.Label = l

		mgr, err := manager.New(manager.NewResourceConfig())
		require.NoError(t, err)

		_, err = mgr.NewProcessor(conf)
		assert.NoError(t, err, "label: %v", l)
	}

	badLabels := []string{
		"_foo",
		"foo-bar",
		"FOO",
		"foo.bar",
	}
	for _, l := range badLabels {
		conf := processor.NewConfig()
		conf.Type = "bloblang"
		conf.Plugin = "root = this"
		conf.Label = l

		mgr, err := manager.New(manager.NewResourceConfig())
		require.NoError(t, err)

		_, err = mgr.NewProcessor(conf)
		assert.EqualError(t, err, docs.ErrBadLabel.Error(), "label: %v", l)
	}
}

func TestManagerCache(t *testing.T) {
	conf := manager.NewResourceConfig()

	fooCache := cache.NewConfig()
	fooCache.Label = "foo"
	conf.ResourceCaches = append(conf.ResourceCaches, fooCache)

	barCache := cache.NewConfig()
	barCache.Label = "bar"
	conf.ResourceCaches = append(conf.ResourceCaches, barCache)

	mgr, err := manager.New(conf)
	if err != nil {
		t.Fatal(err)
	}

	require.True(t, mgr.ProbeCache("foo"))
	require.True(t, mgr.ProbeCache("bar"))
	require.False(t, mgr.ProbeCache("baz"))
}

func TestManagerResourceCRUD(t *testing.T) {
	conf := manager.NewResourceConfig()

	mgr, err := manager.New(conf)
	require.NoError(t, err)

	tCtx, done := context.WithTimeout(t.Context(), time.Second*30)
	defer done()

	inConf := input.NewConfig()
	inConf.Type = "inproc"
	inConf.Plugin = "meow"

	outConf := output.NewConfig()
	outConf.Type = "drop"

	require.False(t, mgr.ProbeCache("foo"))
	require.False(t, mgr.ProbeInput("foo"))
	require.False(t, mgr.ProbeOutput("foo"))
	require.False(t, mgr.ProbeProcessor("foo"))
	require.False(t, mgr.ProbeRateLimit("foo"))

	require.NoError(t, mgr.StoreCache(tCtx, "foo", cache.NewConfig()))
	require.NoError(t, mgr.StoreInput(tCtx, "foo", inConf))
	require.NoError(t, mgr.StoreOutput(tCtx, "foo", outConf))
	require.NoError(t, mgr.StoreProcessor(tCtx, "foo", processor.NewConfig()))
	require.NoError(t, mgr.StoreRateLimit(tCtx, "foo", ratelimit.NewConfig()))

	require.True(t, mgr.ProbeCache("foo"))
	require.True(t, mgr.ProbeInput("foo"))
	require.True(t, mgr.ProbeOutput("foo"))
	require.True(t, mgr.ProbeProcessor("foo"))
	require.True(t, mgr.ProbeRateLimit("foo"))

	require.NoError(t, mgr.RemoveCache(tCtx, "foo"))

	require.False(t, mgr.ProbeCache("foo"))
	require.True(t, mgr.ProbeInput("foo"))
	require.True(t, mgr.ProbeOutput("foo"))
	require.True(t, mgr.ProbeProcessor("foo"))
	require.True(t, mgr.ProbeRateLimit("foo"))

	require.NoError(t, mgr.RemoveInput(tCtx, "foo"))

	require.False(t, mgr.ProbeCache("foo"))
	require.False(t, mgr.ProbeInput("foo"))
	require.True(t, mgr.ProbeOutput("foo"))
	require.True(t, mgr.ProbeProcessor("foo"))
	require.True(t, mgr.ProbeRateLimit("foo"))

	require.NoError(t, mgr.RemoveOutput(tCtx, "foo"))

	require.False(t, mgr.ProbeCache("foo"))
	require.False(t, mgr.ProbeInput("foo"))
	require.False(t, mgr.ProbeOutput("foo"))
	require.True(t, mgr.ProbeProcessor("foo"))
	require.True(t, mgr.ProbeRateLimit("foo"))

	require.NoError(t, mgr.RemoveProcessor(tCtx, "foo"))

	require.False(t, mgr.ProbeCache("foo"))
	require.False(t, mgr.ProbeInput("foo"))
	require.False(t, mgr.ProbeOutput("foo"))
	require.False(t, mgr.ProbeProcessor("foo"))
	require.True(t, mgr.ProbeRateLimit("foo"))

	require.NoError(t, mgr.RemoveRateLimit(tCtx, "foo"))

	require.False(t, mgr.ProbeCache("foo"))
	require.False(t, mgr.ProbeInput("foo"))
	require.False(t, mgr.ProbeOutput("foo"))
	require.False(t, mgr.ProbeProcessor("foo"))
	require.False(t, mgr.ProbeRateLimit("foo"))
}

func TestManagerCacheList(t *testing.T) {
	cacheFoo := cache.NewConfig()
	cacheFoo.Label = "foo"

	cacheBar := cache.NewConfig()
	cacheBar.Label = "bar"

	conf := manager.NewResourceConfig()
	conf.ResourceCaches = append(conf.ResourceCaches, cacheFoo, cacheBar)

	mgr, err := manager.New(conf)
	require.NoError(t, err)

	err = mgr.AccessCache(t.Context(), "foo", func(cache.V1) {})
	require.NoError(t, err)

	err = mgr.AccessCache(t.Context(), "bar", func(cache.V1) {})
	require.NoError(t, err)

	err = mgr.AccessCache(t.Context(), "baz", func(cache.V1) {})
	assert.EqualError(t, err, "unable to locate resource: baz")
}

func TestManagerCacheListErrors(t *testing.T) {
	cFoo := cache.NewConfig()
	cFoo.Label = "foo"

	cBar := cache.NewConfig()
	cBar.Label = "foo"

	conf := manager.NewResourceConfig()
	conf.ResourceCaches = append(conf.ResourceCaches, cFoo, cBar)

	_, err := manager.New(conf)
	require.EqualError(t, err, "cache resource label 'foo' collides with a previously defined resource")

	cEmpty := cache.NewConfig()
	conf = manager.NewResourceConfig()
	conf.ResourceCaches = append(conf.ResourceCaches, cEmpty)

	_, err = manager.New(conf)
	require.EqualError(t, err, "cache resource has an empty label")
}

func TestManagerBadCache(t *testing.T) {
	conf := manager.NewResourceConfig()

	badConf := cache.NewConfig()
	badConf.Label = "bad"
	badConf.Type = "notexist"
	conf.ResourceCaches = append(conf.ResourceCaches, badConf)

	mgr, err := manager.New(conf)
	require.NoError(t, err)

	require.Error(t, mgr.AccessCache(t.Context(), "bad", nil))
}

func TestManagerRateLimit(t *testing.T) {
	conf := manager.NewResourceConfig()

	fooRL := ratelimit.NewConfig()
	fooRL.Label = "foo"
	conf.ResourceRateLimits = append(conf.ResourceRateLimits, fooRL)

	barRL := ratelimit.NewConfig()
	barRL.Label = "bar"
	conf.ResourceRateLimits = append(conf.ResourceRateLimits, barRL)

	mgr, err := manager.New(conf)
	if err != nil {
		t.Fatal(err)
	}

	require.True(t, mgr.ProbeRateLimit("foo"))
	require.True(t, mgr.ProbeRateLimit("bar"))
	require.False(t, mgr.ProbeRateLimit("baz"))
}

func TestManagerRateLimitList(t *testing.T) {
	cFoo := ratelimit.NewConfig()
	cFoo.Label = "foo"

	cBar := ratelimit.NewConfig()
	cBar.Label = "bar"

	conf := manager.NewResourceConfig()
	conf.ResourceRateLimits = append(conf.ResourceRateLimits, cFoo, cBar)

	mgr, err := manager.New(conf)
	require.NoError(t, err)

	err = mgr.AccessRateLimit(t.Context(), "foo", func(ratelimit.V1) {})
	require.NoError(t, err)

	err = mgr.AccessRateLimit(t.Context(), "bar", func(ratelimit.V1) {})
	require.NoError(t, err)

	err = mgr.AccessRateLimit(t.Context(), "baz", func(ratelimit.V1) {})
	assert.EqualError(t, err, "unable to locate resource: baz")
}

func TestManagerRateLimitListErrors(t *testing.T) {
	cFoo := ratelimit.NewConfig()
	cFoo.Label = "foo"

	cBar := ratelimit.NewConfig()
	cBar.Label = "foo"

	conf := manager.NewResourceConfig()
	conf.ResourceRateLimits = append(conf.ResourceRateLimits, cFoo, cBar)

	_, err := manager.New(conf)
	require.EqualError(t, err, "rate limit resource label 'foo' collides with a previously defined resource")

	cEmpty := ratelimit.NewConfig()
	conf = manager.NewResourceConfig()
	conf.ResourceRateLimits = append(conf.ResourceRateLimits, cEmpty)

	_, err = manager.New(conf)
	require.EqualError(t, err, "rate limit resource has an empty label")
}

func TestManagerBadRateLimit(t *testing.T) {
	conf := manager.NewResourceConfig()
	badConf := ratelimit.NewConfig()
	badConf.Type = "notexist"
	badConf.Label = "bad"
	conf.ResourceRateLimits = append(conf.ResourceRateLimits, badConf)

	mgr, err := manager.New(conf)
	require.NoError(t, err)

	require.Error(t, mgr.AccessCache(t.Context(), "bad", nil))
}

func TestManagerProcessor(t *testing.T) {
	conf := manager.NewResourceConfig()

	fooProc := processor.NewConfig()
	fooProc.Label = "foo"
	conf.ResourceProcessors = append(conf.ResourceProcessors, fooProc)

	barProc := processor.NewConfig()
	barProc.Label = "bar"
	conf.ResourceProcessors = append(conf.ResourceProcessors, barProc)

	mgr, err := manager.New(conf)
	if err != nil {
		t.Fatal(err)
	}

	require.True(t, mgr.ProbeProcessor("foo"))
	require.True(t, mgr.ProbeProcessor("bar"))
	require.False(t, mgr.ProbeProcessor("baz"))
}

func TestManagerProcessorList(t *testing.T) {
	cFoo := processor.NewConfig()
	cFoo.Label = "foo"

	cBar := processor.NewConfig()
	cBar.Label = "bar"

	conf := manager.NewResourceConfig()
	conf.ResourceProcessors = append(conf.ResourceProcessors, cFoo, cBar)

	mgr, err := manager.New(conf)
	require.NoError(t, err)

	err = mgr.AccessProcessor(t.Context(), "foo", func(processor.V1) {})
	require.NoError(t, err)

	err = mgr.AccessProcessor(t.Context(), "bar", func(processor.V1) {})
	require.NoError(t, err)

	err = mgr.AccessProcessor(t.Context(), "baz", func(processor.V1) {})
	assert.EqualError(t, err, "unable to locate resource: baz")
}

func TestManagerProcessorListErrors(t *testing.T) {
	cFoo := processor.NewConfig()
	cFoo.Label = "foo"

	cBar := processor.NewConfig()
	cBar.Label = "foo"

	conf := manager.NewResourceConfig()
	conf.ResourceProcessors = append(conf.ResourceProcessors, cFoo, cBar)

	_, err := manager.New(conf)
	require.EqualError(t, err, "processor resource label 'foo' collides with a previously defined resource")

	cEmpty := processor.NewConfig()
	conf = manager.NewResourceConfig()
	conf.ResourceProcessors = append(conf.ResourceProcessors, cEmpty)

	_, err = manager.New(conf)
	require.EqualError(t, err, "processor resource has an empty label")
}

func TestManagerInputList(t *testing.T) {
	cFoo, err := testutil.InputFromYAML(`
label: foo
generate:
  mapping: 'root = {}'
`)
	require.NoError(t, err)

	cBar, err := testutil.InputFromYAML(`
label: bar
generate:
  mapping: 'root = {}'
`)
	require.NoError(t, err)

	conf := manager.NewResourceConfig()
	conf.ResourceInputs = append(conf.ResourceInputs, cFoo, cBar)

	mgr, err := manager.New(conf)
	require.NoError(t, err)

	err = mgr.AccessInput(t.Context(), "foo", func(i input.Streamed) {})
	require.NoError(t, err)

	err = mgr.AccessInput(t.Context(), "bar", func(i input.Streamed) {})
	require.NoError(t, err)

	err = mgr.AccessInput(t.Context(), "baz", func(i input.Streamed) {})
	assert.EqualError(t, err, "unable to locate resource: baz")
}

func TestManagerInputListErrors(t *testing.T) {
	cFoo := input.NewConfig()
	cFoo.Label = "foo"

	cBar := input.NewConfig()
	cBar.Label = "foo"

	conf := manager.NewResourceConfig()
	conf.ResourceInputs = append(conf.ResourceInputs, cFoo, cBar)

	_, err := manager.New(conf)
	require.EqualError(t, err, "input resource label 'foo' collides with a previously defined resource")

	cEmpty := input.NewConfig()
	conf = manager.NewResourceConfig()
	conf.ResourceInputs = append(conf.ResourceInputs, cEmpty)

	_, err = manager.New(conf)
	require.EqualError(t, err, "input resource has an empty label")
}

func TestManagerOutputList(t *testing.T) {
	cFoo := output.NewConfig()
	cFoo.Type = "drop"
	cFoo.Label = "foo"

	cBar := output.NewConfig()
	cBar.Type = "drop"
	cBar.Label = "bar"

	conf := manager.NewResourceConfig()
	conf.ResourceOutputs = append(conf.ResourceOutputs, cFoo, cBar)

	mgr, err := manager.New(conf)
	require.NoError(t, err)

	err = mgr.AccessOutput(t.Context(), "foo", func(ow output.Sync) {})
	require.NoError(t, err)

	err = mgr.AccessOutput(t.Context(), "bar", func(ow output.Sync) {})
	require.NoError(t, err)

	err = mgr.AccessOutput(t.Context(), "baz", func(ow output.Sync) {})
	assert.EqualError(t, err, "unable to locate resource: baz")
}

func TestManagerOutputListErrors(t *testing.T) {
	cFoo := output.NewConfig()
	cFoo.Label = "foo"

	cBar := output.NewConfig()
	cBar.Label = "foo"

	conf := manager.NewResourceConfig()
	conf.ResourceOutputs = append(conf.ResourceOutputs, cFoo, cBar)

	_, err := manager.New(conf)
	require.EqualError(t, err, "output resource label 'foo' collides with a previously defined resource")

	cEmpty := output.NewConfig()
	conf = manager.NewResourceConfig()
	conf.ResourceOutputs = append(conf.ResourceOutputs, cEmpty)

	_, err = manager.New(conf)
	require.EqualError(t, err, "output resource has an empty label")
}

func TestManagerPipeErrors(t *testing.T) {
	conf := manager.NewResourceConfig()
	mgr, err := manager.New(conf)
	if err != nil {
		t.Fatal(err)
	}

	if _, err = mgr.GetPipe("does not exist"); err != component.ErrPipeNotFound {
		t.Errorf("Wrong error returned: %v != %v", err, component.ErrPipeNotFound)
	}
}

func TestManagerPipeGetSet(t *testing.T) {
	conf := manager.NewResourceConfig()
	mgr, err := manager.New(conf)
	if err != nil {
		t.Fatal(err)
	}

	t1 := make(chan message.Transaction)
	t2 := make(chan message.Transaction)
	t3 := make(chan message.Transaction)

	mgr.SetPipe("foo", t1)
	mgr.SetPipe("bar", t3)

	var p <-chan message.Transaction
	if p, err = mgr.GetPipe("foo"); err != nil {
		t.Fatal(err)
	}
	if p != t1 {
		t.Error("Wrong transaction chan returned")
	}

	// Should be a noop
	mgr.UnsetPipe("foo", t2)
	if p, err = mgr.GetPipe("foo"); err != nil {
		t.Fatal(err)
	}
	if p != t1 {
		t.Error("Wrong transaction chan returned")
	}
	if p, err = mgr.GetPipe("bar"); err != nil {
		t.Fatal(err)
	}
	if p != t3 {
		t.Error("Wrong transaction chan returned")
	}

	mgr.UnsetPipe("foo", t1)
	if _, err = mgr.GetPipe("foo"); err != component.ErrPipeNotFound {
		t.Errorf("Wrong error returned: %v != %v", err, component.ErrPipeNotFound)
	}

	// Back to before
	mgr.SetPipe("foo", t1)
	if p, err = mgr.GetPipe("foo"); err != nil {
		t.Fatal(err)
	}
	if p != t1 {
		t.Error("Wrong transaction chan returned")
	}

	// Now replace pipe
	mgr.SetPipe("foo", t2)
	if p, err = mgr.GetPipe("foo"); err != nil {
		t.Fatal(err)
	}
	if p != t2 {
		t.Error("Wrong transaction chan returned")
	}
	if p, err = mgr.GetPipe("bar"); err != nil {
		t.Fatal(err)
	}
	if p != t3 {
		t.Error("Wrong transaction chan returned")
	}
}

type (
	testKeyTypeA int
	testKeyTypeB int
)

const (
	testKeyA testKeyTypeA = iota
	testKeyB testKeyTypeB = iota
)

func TestManagerGenericResources(t *testing.T) {
	mgr, err := manager.New(manager.NewResourceConfig())
	require.NoError(t, err)

	mgr.SetGeneric(testKeyA, "foo")
	mgr.SetGeneric(testKeyB, "bar")

	_, exists := mgr.GetGeneric("not a key")
	assert.False(t, exists)

	v, exists := mgr.GetGeneric(testKeyA)
	assert.True(t, exists)
	assert.Equal(t, "foo", v)

	v, exists = mgr.GetGeneric(testKeyB)
	assert.True(t, exists)
	assert.Equal(t, "bar", v)
}

func TestManagerGenericGetOrSet(t *testing.T) {
	mgr, err := manager.New(manager.NewResourceConfig())
	require.NoError(t, err)

	v, loaded := mgr.GetOrSetGeneric(testKeyA, "foo")
	assert.False(t, loaded)
	assert.Equal(t, "foo", v)

	v, loaded = mgr.GetOrSetGeneric(testKeyA, "bar")
	assert.True(t, loaded)
	assert.Equal(t, "foo", v)
}
