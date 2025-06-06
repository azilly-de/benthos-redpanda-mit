// Copyright 2025 Redpanda Data, Inc.

package pure

import (
	"context"
	"fmt"

	"github.com/redpanda-data/benthos/v4/internal/bundle"
	"github.com/redpanda-data/benthos/v4/internal/component/interop"
	"github.com/redpanda-data/benthos/v4/internal/component/processor"
	"github.com/redpanda-data/benthos/v4/internal/log"
	"github.com/redpanda-data/benthos/v4/internal/message"
	"github.com/redpanda-data/benthos/v4/public/service"
)

const (
	compressPFieldAlgorithm = "algorithm"
	compressPFieldLevel     = "level"
)

func init() {
	compAlgs := CompressionAlgsList()
	service.MustRegisterBatchProcessor(
		"compress", service.NewConfigSpec().
			Categories("Parsing").
			Stable().
			Summary(fmt.Sprintf("Compresses messages according to the selected algorithm. Supported compression algorithms are: %v", compAlgs)).
			Description(`The 'level' field might not apply to all algorithms.`).
			Fields(
				service.NewStringEnumField(compressPFieldAlgorithm, compAlgs...).
					Description("The compression algorithm to use.").
					LintRule(``),
				service.NewIntField(compressPFieldLevel).
					Description("The level of compression to use. May not be applicable to all algorithms.").
					Default(-1),
			),
		func(conf *service.ParsedConfig, res *service.Resources) (service.BatchProcessor, error) {
			algStr, err := conf.FieldString(compressPFieldAlgorithm)
			if err != nil {
				return nil, err
			}

			level, err := conf.FieldInt(compressPFieldLevel)
			if err != nil {
				return nil, err
			}

			mgr := interop.UnwrapManagement(res)
			p, err := newCompress(algStr, level, mgr)
			if err != nil {
				return nil, err
			}
			return interop.NewUnwrapInternalBatchProcessor(processor.NewAutoObservedProcessor("compress", p, mgr)), nil
		})
}

type compressProc struct {
	level int
	comp  CompressFunc
	log   log.Modular
}

func newCompress(algStr string, level int, mgr bundle.NewManagement) (*compressProc, error) {
	cor, err := strToCompressFunc(algStr)
	if err != nil {
		return nil, err
	}
	return &compressProc{
		level: level,
		comp:  cor,
		log:   mgr.Logger(),
	}, nil
}

func (c *compressProc) Process(ctx context.Context, msg *message.Part) ([]*message.Part, error) {
	newBytes, err := c.comp(c.level, msg.AsBytes())
	if err != nil {
		c.log.Error("Failed to compress message: %v\n", err)
		return nil, err
	}
	msg.SetBytes(newBytes)
	return []*message.Part{msg}, nil
}

func (c *compressProc) Close(context.Context) error {
	return nil
}
