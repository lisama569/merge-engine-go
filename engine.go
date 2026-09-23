// Package mergeengine 是版本合并引擎的对外入口。
//
// 它组合本地三方存储、差异归一化解析器与纯函数三方合并器，
// 使并行修改的合并结论稳定、可解释且可复现。
package mergeengine

import (
	"context"
	"fmt"

	"github.com/lisama569/merge-engine-go/internal/diffparse"
	"github.com/lisama569/merge-engine-go/internal/errs"
	"github.com/lisama569/merge-engine-go/internal/merge"
	"github.com/lisama569/merge-engine-go/internal/model"
	"github.com/lisama569/merge-engine-go/internal/store"
)

// Engine 协调存储、解析与合并。
type Engine struct {
	store  *store.Store
	parser diffparse.Parser
}

// New 创建引擎，默认使用基于 go-diff 的解析器与内存存储。
func New() *Engine {
	return &Engine{
		store:  store.New(),
		parser: diffparse.NewGoDiffParser(),
	}
}

// NewWithParser 使用自定义解析器创建引擎（注入故障用）。
func NewWithParser(p diffparse.Parser) *Engine {
	return &Engine{store: store.New(), parser: p}
}

// Store 暴露底层存储（测试/检查用）。
func (e *Engine) Store() *store.Store { return e.store }

// LoadSnapshot 载入一方命名状态（base/ours/theirs 等）。
func (e *Engine) LoadSnapshot(name string, snap model.Snapshot) error {
	snap.Label = name
	return e.store.PutSnapshot(snap)
}

// Submit 归一化并登记一个并行修改来源。
// 返回归一化结果与逐条拒绝原因；解析器致命错误以归一化错误返回，
// 绝不透出未处理的第三方库异常。
func (e *Engine) Submit(ctx context.Context, raw model.RawChangeSet) (*model.ChangeSet, []model.Rejection, error) {
	set, rejections, err := e.parser.Normalize(ctx, raw)
	if err != nil {
		return nil, nil, err
	}
	if err := e.store.PutChangeSet(*set); err != nil {
		return nil, rejections, err
	}
	return set, rejections, nil
}

// Merge 对 base 上的两个变更集执行三方合并并持久化目标快照。
//
// 参数 authors 必须恰好包含两个已登记变更集的作者；顺序决定报告中的
// 应用顺序（稳定）。目标已存在时执行幂等重放校验：结论必须与首次
// 完全一致，否则返回 CodeConflict（出现了不可复现的分歧）。
func (e *Engine) Merge(ctx context.Context, baseName, targetName string, authors []string) (*model.MergeReport, error) {
	if err := ctx.Err(); err != nil {
		return nil, errs.New(errs.CodeInvalidInput, "context cancelled", err)
	}
	if len(authors) != 2 {
		return nil, errs.New(errs.CodeInvalidInput,
			fmt.Sprintf("three-way merge requires exactly 2 authors, got %d", len(authors)), nil)
	}

	base, err := e.store.GetSnapshot(baseName)
	if err != nil {
		return nil, err
	}
	ours, err := e.store.GetChangeSet(authors[0])
	if err != nil {
		return nil, err
	}
	theirs, err := e.store.GetChangeSet(authors[1])
	if err != nil {
		return nil, err
	}

	report := merge.ThreeWay(base, ours, theirs)

	// 幂等重放：若目标已合并过，结论必须逐字段一致。
	if prev := e.store.Token(targetName); prev != "" {
		if prev != report.ReplayToken {
			return nil, errs.New(errs.CodeConflict,
				"replay produced a different result; merge is not reproducible", nil)
		}
	}

	merged := *report.Merged
	merged.Label = targetName
	if err := e.store.PutSnapshot(merged); err != nil {
		return nil, err
	}
	for _, a := range authors {
		e.store.MarkApplied(targetName, a)
	}
	e.store.SaveToken(targetName, report.ReplayToken)
	return &report, nil
}

// Replay 以既定作者顺序重放合并，返回的报告必须与首次一致。
// 它不修改存储，仅从三方状态重新计算并校验 replay token。
func (e *Engine) Replay(ctx context.Context, baseName, targetName string, authors []string) (*model.MergeReport, error) {
	if err := ctx.Err(); err != nil {
		return nil, errs.New(errs.CodeInvalidInput, "context cancelled", err)
	}
	base, err := e.store.GetSnapshot(baseName)
	if err != nil {
		return nil, err
	}
	ours, err := e.store.GetChangeSet(authors[0])
	if err != nil {
		return nil, err
	}
	theirs, err := e.store.GetChangeSet(authors[1])
	if err != nil {
		return nil, err
	}
	report := merge.ThreeWay(base, ours, theirs)

	prev := e.store.Token(targetName)
	if prev == "" {
		return nil, errs.New(errs.CodeNotFound,
			fmt.Sprintf("target %q has never been merged; call Merge first", targetName), nil)
	}
	if prev != report.ReplayToken {
		return nil, errs.New(errs.CodeConflict,
			"replay diverged from the original merge result", nil)
	}
	return &report, nil
}
