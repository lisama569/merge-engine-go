# merge-engine-go

仅依赖**本地数据**的三方版本合并引擎。用本地内存存储模拟基线（base）、两个
并行变更侧（sides）与合并目标（merged）三方状态，使并行修改的合并结论
**稳定、可解释、可复现**。无任何外部服务或第三方运行时依赖。

## 架构

```
ingest.Envelope ──▶ ingest.Normalize ──▶ model.ChangeSet
   (text_diff /       (校验 + 翻译)          (统一模型)
    structured)                 │            │
                 internal/thirddiff          ▼
                 (模拟第三方 diff 库)   storage.Store (本地三方状态)
                                              │
                                       merge.MergeSets
                                       (纯函数三方合并)
                                              ▼
                                       model.MergeResult
                                       (applied + conflicts + Explain)
```

- `merr` — 统一错误分类（见下），所有对外错误都是 `*merr.EngineError`。
- `model` — 统一模型：`Snapshot`、`ChangeSet`、`Change`、`Hunk`、`MergeResult`。
- `storage` — 深拷贝的本地快照/变更集仓库（`sync.RWMutex` 保护），模拟持久层。
- `internal/thirddiff` — **模拟的第三方差异解析库**，有自己的原生错误类型
  `DiffSyntaxError`，并会在遇到 `LIBRARY-BOOM` sentinel 时 panic（`LibraryFault`）。
- `ingest` — 把异构输入归一化；封装第三方库，原生异常绝不透出边界。
- `merge` — 确定性三方合并与幂等重放。
- 根包 `mergeengine` — 对外门面，串联以上组件。

## 统一输入模型

两种变更来源都归一为 `model.ChangeSet{SourceID, Changes[]}`：

| 来源 kind      | 形态                                             |
| -------------- | ------------------------------------------------ |
| `text_diff`    | 单文件 unified diff，经第三方库解析为 `OpModify` 的 `Hunks` |
| `structured`   | 显式记录，op ∈ `add / delete / modify / rename / typechange` |

**不完整输入绝不静默跳过**，整个信封要么全部合法，要么返回首个拒绝原因：

| reason 码                  | 含义                                       |
| -------------------------- | ------------------------------------------ |
| `nil_envelope`/`missing_source_id`/`missing_kind`/`unsupported_kind` | 信封层缺字段或类型不支持 |
| `missing_path`/`missing_old_path`/`missing_hunks`/`missing_change_type` | 记录层必要字段缺失 |
| `unknown_op`               | 不支持的操作类型                           |
| `illegal_line_range`       | 负数行号、`end < start`、替换用 0 起始行、插入未用锚点形式等非法行区间 |
| `hunk_length_mismatch`     | 声明区间长度与提供的旧/新行数不一致        |
| `empty_hunk`               | 旧、新行都为空的空 hunk（什么都不改）      |
| `duplicate_path`           | 同一批次重复操作同一路径                   |
| `hunks_not_allowed`/`delete_with_payload`/`rename_same_path`/... | op 与载荷矛盾 |

### 纯插入 hunk 的表达方式

`modify` 的 hunk 中，**`old_lines` 为空即纯插入**：不修改、不删除任何原有行，
只把 `new_lines` 插入到基线第 `old_start` 行之前。此时必须使用**锚点形式**
`old_start == old_end`：

| 场景                 | 写法                                          |
| -------------------- | --------------------------------------------- |
| 插到第 N 行之前      | `old_start == old_end == N`，`old_lines: []`  |
| 空文件 / 插到最前    | `old_start == old_end == 0`                   |
| 追加到文件末尾       | 锚点超过基线行数（应用时钳制到末尾）          |

`old_lines` 为空但 `old_start != old_end` 属于**畸形输入**，按
`illegal_line_range` 拒绝，不会被当成插入；锚点形式但不给 `new_lines`
按 `empty_hunk` 拒绝；`new_lines` 数量与 `new_start/new_end` 区间不符按
`hunk_length_mismatch` 拒绝。这些拒绝原因互相可区分，且都使整批输入被拒绝、
不进入存储。

## 合并语义

合并是纯函数：`merge.MergeSets(base, sides...)`，同一输入永远得到同一输出；
side 按 `SourceID` 排序、冲突按路径/类型/侧排序，因此**与输入顺序无关**。

| 场景                               | 结论                                  |
| ---------------------------------- | ------------------------------------- |
| 不同路径 / 不相交 hunk 行区间      | 自动合并                              |
| 单侧纯插入（中间 / 空文件 / 末尾） | 自动合并                              |
| 两侧在同一锚点插入**相同**内容     | 只应用一次（幂等，非冲突）            |
| 两侧在同一锚点插入**不同**内容     | `text_overlap` 冲突                   |
| 插入与不相交的修改并存             | 自动合并（hunk 统一按基线坐标一次应用）|
| 两侧完全相同的变更（含相同 hunk、相同 add、相同 rename） | 只应用一次（幂等，非冲突） |
| 同一基线行区间被两侧做**不同**修改 | `text_overlap` 冲突                   |
| 两侧把同一文件 rename 到不同目标   | `rename_rename` 冲突                  |
| 一侧 rename、另一侧 delete         | `rename_delete` 冲突                  |
| 一侧 rename、另一侧修改旧路径      | `rename_delete` 冲突（语义冲突）      |
| 一侧 modify、另一侧 delete         | `modify_delete` 冲突                  |
| 两侧 add 同一路径但内容不同        | `add_add` 冲突；内容相同则自动合并    |
| 文件/目录类型变化与其他修改冲突    | `type_mismatch` 冲突                  |

关键原则：

- **冲突与自动合并可以并存**：一次合并里无法自动处理的路径被标成冲突，
  但互不干扰的变更**照常并入结果树**，不会整体回退成基线。`OK` 只在
  存在冲突时为 `false`；`Applied` 列出实际生效的变更，`Conflicts` 列出
  被保留为基线内容的路径及其判定依据。
- 冲突的路径引擎**从不任选一方**，该路径在结果树中保持基线内容，
  不写任何猜测结果；含冲突的结果不会被持久化为目标快照
  （`Engine.Merge` 仅在 `OK` 时写入 `resultName`，调用方如需串联两层
  合并，可自行 `SaveSnapshot(res.Merged)`）。
- 同一文件多个被接受的 hunk 以**基线坐标**一次性应用（先按锚点降序、
  相同 hunk 去重），插入导致的行号偏移不会影响其他 hunk 的落点。
- 每个冲突带有 `Kind / Path / SideA / SideB / Detail / Evidence`，
  `MergeResult.Explain()` 输出可读的判定报告，可据报告复现判定。
- **幂等重放**：`Engine.Replay(base, result, n, sides...)` 每轮都从同一基线
  重新合并相同变更集，逐轮比较结果；同一变更集重复应用不会叠加（hunk 去重、
  相同语义变更折叠），结果不一致会返回 `non_idempotent_replay`。两层合并
  串联（上一层的输出作为下一层的基线）或对输出做无变更的校验式合并，
  只要输入与基线相同，结论与内容完全一致，不发生漂移。

## 错误分类

所有公共 API 返回的错误都可用 `errors.As(err, &ee)` 取到 `*merr.EngineError`，
其中 `Kind` 为：

| Kind                    | 谓词                     | 用途                           |
| ----------------------- | ------------------------ | ------------------------------ |
| `invalid_input`         | `merr.IsInvalid`         | 归一化阶段拒绝非法输入         |
| `syntax_error`          | `merr.IsSyntax`          | 第三方库报告的 diff 语法错误   |
| `library_internal_error`| `merr.IsLibraryInternal` | 第三方库内部故障/panic（已恢复）|
| `not_found`             | `merr.IsNotFound`        | 快照/变更集不存在              |
| `conflict`              | `merr.IsConflict`        | 合并冲突（结果体亦含 Conflicts）|

第三方库异常的归一化映射（见 `ingest/textdiff.go`）：

- `*thirddiff.DiffSyntaxError` → `KindSyntax` / reason `diff_syntax`，
  保留出错行号与原始文本；
- 库 panic（`*thirddiff.LibraryFault` 或任意值）→ `recover()` 后封装为
  `KindLibraryInternal` / reason `diff_library_panic`，保留 recovered 上下文；
- 其他库错误 → `KindLibraryInternal` / reason `diff_library_fault`。

对外错误文本不会出现 `DiffSyntaxError`/`LibraryFault` 等库原生类型。

## 验证方法

```bash
go build ./...
go vet ./...
gofmt -l .
go test -race -count=1 ./... -v   # -v 可看到三方输入与判定依据日志
```

测试矩阵：

- `merge` 包：不相交自动合并、**重叠 hunk 冲突**、**重命名/重命名、
  重命名/删除、重命名/修改、修改/删除冲突**、add/add、类型变化冲突、
  相同变更幂等、**幂等重放**、输入排列确定性；**插入语义**（单侧中间/
  末尾/空文件插入、双侧同锚点同内容幂等、双侧同锚点不同内容冲突、
  插入与不相交修改并存）；**混合场景**（冲突与自动合并并存时干净部分
  照常并入、冲突路径保持基线、`Applied` 可核对）；**混合输入重复跑
  结论逐位一致**；**两层合并串联与无变更校验式合并不漂移**。
  每个用例都通过 `logThreeWay`/`logVerdict` 在测试日志中打印基线、
  两侧输入与判定依据（冲突的 `Kind`/`Evidence` 一目了然）。
- `ingest` 包：各类**非法输入**（缺字段、非法行区间、长度不一致、重复路径）
  的可区分拒绝原因；**插入边界**（合法锚点形式被接受，非锚点空 `old_lines`、
  空 hunk、新行数不符分别按 `illegal_line_range`/`empty_hunk`/
  `hunk_length_mismatch` 拒绝）；库**语法错误 vs 内部 panic** 的分类差异；
  原始上下文保留。
- 根包端到端：文本 diff 侧与结构化侧跨格式合并、冲突重放一致性、
  **结构化插入经门面进入合并**、**混合冲突下干净插入仍然生效且重复跑
  不漂移**、**两层串联与校验式合并**、非法输入不入库、库原生异常不透出。

兼容范围：结构化输入的 op 集合不变；`modify` hunk 在原有"替换/删除行区间"
之外新增"纯插入"（空 `old_lines` + 锚点形式）。原有合法输入的判定结论不变；
原先被 `hunk_length_mismatch` 拒绝的锚点形式插入现在被接受（这是本轮的
行为扩展），而 `old_start == 0` 且带 `old_lines` 的写法从
`hunk_length_mismatch` 改判为更准确的 `illegal_line_range`。

## 快速示例

```go
eng := mergeengine.New()
eng.SaveSnapshot(&model.Snapshot{Name: "base", Files: map[string]string{
    "doc.txt": "one\ntwo\n",
}})
eng.Ingest(&ingest.Envelope{SourceID: "alice", Kind: ingest.SourceStructured,
    Structured: &ingest.StructuredInput{Changes: []ingest.StructuredChange{{
        Path: "doc.txt", Op: model.OpModify,
        Hunks: []ingest.StructuredHunk{{
            OldStart: 1, OldEnd: 1, NewStart: 1, NewEnd: 1,
            OldLines: []string{"one"}, NewLines: []string{"ONE"},
        }},
    }}}})
res, err := eng.Replay("base", "merged", 3, "alice")
fmt.Println(res.OK, res.Explain())
```

纯插入（只在第 2 行前插入一行，不动任何原有行）：

```go
eng.Ingest(&ingest.Envelope{SourceID: "bob", Kind: ingest.SourceStructured,
    Structured: &ingest.StructuredInput{Changes: []ingest.StructuredChange{{
        Path: "doc.txt", Op: model.OpModify,
        Hunks: []ingest.StructuredHunk{{
            OldStart: 2, OldEnd: 2, // 锚点形式：old_start == old_end
            NewStart: 1, NewEnd: 1,
            NewLines: []string{"inserted"}, // old_lines 留空即纯插入
        }},
    }}}})
```
