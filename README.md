# merge-engine-go

仅依赖本地数据的版本合并引擎：以内存存储模拟**基线 / 我方 / 对方**
三方状态，把文本差异（unified diff 或字段式 hunk）与结构化变更记录
归一为统一模型，执行确定性的三方合并。结论稳定、可解释、可复现。

底层差异解析使用第三方库 [`github.com/sergi/go-diff`](https://github.com/sergi/go-diff)，
其语法错误与内部异常被归一化为不同错误类型，不向外透出未处理异常。

## 能力一览

- **统一归一化**：文本差异与结构化变更（create/update/delete/
  rename/typeset）归一为同一 `Change` 模型。
- **不静默丢弃非法输入**：缺路径、未知操作、缺 key/new_key、非法行
  区间、hunk 头与体计数不符等，逐条返回带可区分原因码的 `Rejection`。
- **三方合并**：互不重叠改动自动合并；行区间重叠、重命名分歧、
  删除/修改、类型变化、同值分歧等显式标记 `Conflict`，绝不任选一方。
- **幂等与可复现**：重复应用同一变更集结果稳定；每次合并生成
  `ReplayToken`，`Replay` 重放结论必须逐字节一致。
- **库异常封装**：库语法错误 → `parse_syntax`；库 panic →
  `parse_library`（recover 并保留原始上下文）。

## 目录结构

```
engine.go                  对外入口 Engine
internal/model/            统一模型、冲突与拒绝结论
internal/errs/             归一化错误分类
internal/diffparse/        归一化与第三方库封装（go-diff）
internal/store/            本地三方状态内存存储
internal/merge/            三方合并与冲突检测（纯函数）
docs/design.md             合并语义、错误分类的详细设计
```

## 快速开始

```go
eng := mergeengine.New()
eng.LoadSnapshot("base", model.Snapshot{ /* 基线文件与条目 */ })

set, rejections, err := eng.Submit(ctx, model.RawChangeSet{
    Author: "alice",
    TextDiffs: []model.RawTextDiff{{
        Path: "config.txt",
        OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 1,
        Lines: []string{"new line"},
    }},
})
// rejections 中是逐条非法记录及其可区分原因；err 仅在库致命错误时非空

report, err := eng.Merge(ctx, "base", "target", []string{"alice", "bob"})
// report.Conflicts / report.AutoMerged / report.ReplayToken
```

## 合并语义与错误分类

详见 [`docs/design.md`](docs/design.md)。摘要：

- 冲突类型：`overlap`、`rename_clash`、`delete_modify`、
  `type_change`、`structured_clash`、`create_existing`。
- 引擎错误码：`invalid_input`、`parse_syntax`、`parse_library`、
  `not_found`、`conflict`。
- 逐条拒绝码：`missing_path`、`unknown_op`、`missing_key`、
  `missing_new_key`、`invalid_line_range`、`missing_diff_body`、
  `invalid_hunk_header`。

## 验证方法

需要 Go 1.25+（开发环境为 Go 1.26）。首次构建会经 `go mod` 下载
`go-diff`（go.mod 已固定 `v1.4.0`）。

```bash
# 全量单元测试
go test ./...

# 带三方输入与判定依据的详细日志（JSON 形式打印 base/ours/theirs）
go test -v ./...

# 竞态检测 + 关闭缓存，验证并发安全与可复现
go test -race -count=1 ./...

# 静态检查
go vet ./...
```

### 测试覆盖矩阵

| 测试 | 覆盖点 |
| --- | --- |
| `TestEngineOverlapConflict` / `TestOverlappingTextConflict` | 行区间重叠冲突，冲突路径保留基线 |
| `TestEngineRenameConflict` / `TestRenameConflict` | 同键 rename 到不同目标 → `rename_clash` |
| `TestDeleteModifyConflict` | 一方删除、一方修改 → `delete_modify` |
| `TestTypeChangeConflict` | 同键类型改成不同类型 → `type_change` |
| `TestEngineInvalidInputRejections` | 五类非法输入逐条拒绝、原因可区分、不静默跳过 |
| `TestEngineIdempotentReplayMerge` / `TestIdempotentReplay` | 重复 Apply、重复 Merge、Replay 结论与 token 稳定 |
| `TestNormalizeLibraryPanicMapsToLibraryError` | 库 panic → `parse_library`，保留原始上下文 |
| `TestParseSyntaxMappedFromThirdParty` / `...SyntaxDistinctFromPanic` | 库语法错误 → `parse_syntax`，与内部异常可区分 |
| `TestChangeSetIdempotentRegistration` | 同名变更集重复登记幂等、内容不一致显式报错 |

测试日志中会打印三方输入（`BASE` / `OURS` / `THEIRS`）以及
`JUDGEMENT` 判定依据（冲突区间、冲突双方作者、拒绝原因、
replay token），便于解释每一条结论。
