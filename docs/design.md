# 版本合并引擎设计说明

仅依赖本地数据的三方版本合并引擎：用内存存储模拟**基线（base）**、
**我方（ours）**、**对方（theirs）** 三方状态，将文本差异与结构化
变更记录归一化为统一模型后执行确定性三方合并。相同输入永远产生相同
结论，结论稳定、可解释、可复现。

## 1. 总体结构

```
                 ┌──────────────────────────────────────────┐
 RawChangeSet ─▶ │ internal/diffparse  归一化 + 输入校验     │
 (文本/结构化)    │   └ 封装第三方库 github.com/sergi/go-diff │
                 └───────────────┬──────────────────────────┘
                                 ▼
                       model.ChangeSet（统一模型）
                                 │
              ┌──────────────────┴───────────────────┐
              ▼                                       ▼
   internal/store（本地三方状态存储）      internal/merge（纯函数合并）
              └───────────────┬───────────────────────┘
                              ▼
                    MergeReport（结论/冲突/拒绝/token）
                              ▲
                       engine.go（对外入口 Engine）
```

| 包 | 职责 |
| --- | --- |
| `internal/model` | 统一变更模型、三方快照、冲突与拒绝结论的数据结构 |
| `internal/errs` | 归一化错误分类码与 `EngineError`，保留原始上下文 |
| `internal/diffparse` | 原始输入归一化、字段/区间校验、第三方库异常封装 |
| `internal/store` | 本地内存存储：命名快照、变更集登记、重放令牌 |
| `internal/merge` | 纯函数三方合并、冲突检测、幂等应用 |
| 根包 `mergeengine` | 对外编排入口：`LoadSnapshot` / `Submit` / `Merge` / `Replay` |

## 2. 统一变更模型

两种外部来源被归一化为同一种 `model.Change`：

- **文本差异** `Source=text_diff`：载荷为 `TextHunk`
  - `Range [Start, End]` 是基线上的 1 起始闭区间；
  - 纯插入表示为 `End == Start-1`，插入点在 `Start` 行之前；
  - `Replacement` 为替换后的行，删除时为空。
- **结构化变更** `Source=structured`：载荷为 `StructuredChange`
  - 操作：`create` / `update` / `delete` / `rename` / `typeset`，
    作用于文件内具名条目（key → {type, value}）。

## 3. 三方合并语义

`merge.ThreeWay(base, ours, theirs)` 遵循：

1. **冲突优先检测，不做取舍**：先在两个变更集之间枚举全部冲突，
   冲突路径上的双方改动都不会自动应用，目标路径保留基线内容。
2. **互不重叠自动合并**：剩余改动按固定顺序（先 ours 后 theirs、
   路径/区间排序）应用，输出顺序确定。
3. **纯函数与防御拷贝**：不修改任何输入快照，深拷贝后再写。

### 3.1 冲突分类（必须显式标记，绝不任选一方）

| `ConflictKind` | 判定条件 |
| --- | --- |
| `overlap` | 同一文本文件的两个 hunk 行区间重叠；相同纯插入点也算重叠 |
| `rename_clash` | 同一旧键被双方 rename 到**不同**新键 |
| `delete_modify` | 一方 delete 某键，另一方 update/rename/typeset 该键 |
| `type_change` | 同一键被双方改成不同类型；或同路径分别按文本/结构化修改 |
| `structured_clash` | 同一键被双方 update 成不同值 |
| `create_existing` | 双方对同一键执行 create；或 create 到已存在且内容不同的键 |

非冲突的典型可自动合并情形：改动不同文件；同文件不同且不相交的行
区间；一方改文本、另一方在另一个结构化文件新增条目；双方对同一键
做**相同**修改（按指纹去重）。

### 3.2 幂等与可复现

- 每条变更有内容指纹（路径+来源+区间/结构化字段），快照记录
  `Applied []string`；**重复应用同一变更集时已存在指纹被跳过**，
  结果保持稳定。
- 一次合并产生 `ReplayToken`（三方标签 + 双方变更指纹的 SHA-256
  前 16 位）。对同一目标再次 `Merge` 或调用 `Replay` 时必须得到
  相同 token；token 漂移会返回 `conflict` 错误，表示结论不可复现。

## 4. 错误分类

### 4.1 引擎错误（`internal/errs.Code`）

| Code | 含义 | 何时产生 |
| --- | --- | --- |
| `invalid_input` | 输入格式不完整/缺字段/区间非法（致命路径） | 上下文取消、作者数不为 2 等 |
| `parse_syntax` | **第三方库报告的语法错误** | go-diff `PatchFromText` 返回 error |
| `parse_library` | **第三方库内部异常** | 库调用 panic，被边界 recover |
| `not_found` | 本地存储缺少指定快照/变更集 | 引用未载入名称 |
| `conflict` | 语义冲突/不可复现重放/同名变更集内容不一致 | 合并与登记阶段 |

`parse_syntax` 与 `parse_library` 是**两类不同错误**：前者是库正常
返回的语法拒绝（原始 error 作为 `Cause` 保留），后者是库崩溃
（recover 到的值作为 `Cause` 保留）。对外只有 `*errs.EngineError`，
第三方库异常不会以未处理形态透出。

### 4.2 逐条输入拒绝（`model.RejectCode`，非致命）

格式不完整、行区间非法或缺必要字段的**单条记录**不会静默跳过，也
不会中断整批解析，而是以 `Rejection{Index, Path, Reason, Detail}`
逐条返回，原因可区分：

`missing_path`、`unknown_op`、`missing_key`、`missing_new_key`、
`invalid_line_range`、`missing_diff_body`、`invalid_hunk_header`。

hunk 头与 hunk 体行数不一致（如声明 `-1,2` 但体里只有 1 行旧侧）
归为 `invalid_hunk_header`，`Detail` 给出声明计数与实际计数。

## 5. 典型调用流程

```go
eng := mergeengine.New()
eng.LoadSnapshot("base", baseSnapshot)                 // 载入基线
eng.Submit(ctx, rawAlice)                              // 归一化我方（含逐条拒绝）
eng.Submit(ctx, rawBob)                                // 归一化对方
report, _ := eng.Merge(ctx, "base", "target",          // 三方合并
    []string{"alice", "bob"})
again,  _ := eng.Replay(ctx, "base", "target",         // 幂等重放校验
    []string{"alice", "bob"})
```

`MergeReport` 同时给出 `Merged`、`AutoMerged`、`Conflicts`、
`Rejections`、`ReplayToken`，每处冲突带 `Kind/Path/Detail/Authors`，
便于人工裁决与审计。
