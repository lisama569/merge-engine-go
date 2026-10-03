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
| `illegal_line_range`       | 负数行号、`end < start` 等非法行区间       |
| `hunk_length_mismatch`     | 声明区间长度与提供的旧/新行数不一致        |
| `duplicate_path`           | 同一批次重复操作同一路径                   |
| `hunks_not_allowed`/`delete_with_payload`/`rename_same_path`/... | op 与载荷矛盾 |

## 合并语义

合并是纯函数：`merge.MergeSets(base, sides...)`，同一输入永远得到同一输出；
side 按 `SourceID` 排序、冲突按路径/类型/侧排序，因此**与输入顺序无关**。

| 场景                               | 结论                                  |
| ---------------------------------- | ------------------------------------- |
| 不同路径 / 不相交 hunk 行区间      | 自动合并                              |
| 纯插入（单侧/双侧不同锚点）        | 自动合并                              |
| 两侧在同一锚点插入**相同**内容     | 只应用一次（幂等，非冲突）            |
| 两侧在同一锚点插入**不同**内容     | `text_overlap` 冲突                   |
| 插入锚点落在另一侧改写的行上       | `text_overlap` 冲突（保守判定）       |
| 两侧完全相同的变更（含相同 hunk、相同 add、相同 rename） | 只应用一次（幂等，非冲突） |
| 同一基线行区间被两侧做**不同**修改 | `text_overlap` 冲突（hunk 级）        |
| 两侧把同一文件 rename 到不同目标   | `rename_rename` 冲突                  |
| 一侧 rename、另一侧 delete         | `rename_delete` 冲突                  |
| 一侧 rename、另一侧修改旧路径      | `rename_delete` 冲突（语义冲突）      |
| 一侧 modify、另一侧 delete         | `modify_delete` 冲突                  |
| 两侧 add 同一路径但内容不同        | `add_add` 冲突；内容相同则自动合并    |
| 文件/目录类型变化与其他修改冲突    | `type_mismatch` 冲突                  |

### 插入语义

纯插入 hunk 不带旧行（`old_lines` 为空），锚点是零宽位置：

- `old_start == old_end == k`（k ≥ 1）：在第 k 行**之前**插入；
- `old_start == old_end == 0`：插入空文件 / 插到首行之前；
- `old_start == old_end == len(base)+1`：追加到文件末尾。

结构化输入中以下形态会被**明确拒绝而非当成插入**（reason 可区分）：
声明了多行旧区间却不给旧行（`hunk_length_mismatch`）、新旧都为空
（`empty_hunk`）、负行号或 `end < start`（`illegal_line_range`）、
`new_lines` 数量与声明区间不符（`hunk_length_mismatch`）。

### 冲突与自动合并并存

一次合并中冲突与正常合并**同时成立**，互不回退：

- 路径级：某路径冲突时，其余路径的无关变更照常并入结果；
- hunk 级：同一文件内，只有真正重叠的 hunk 对被判冲突，两侧各自
  不干扰的 hunk 仍以"部分 modify"并入（`Applied` 中照常记录）；
- 冲突路径自身保持基线内容，引擎从不任选一方、不写猜测结果；
- `OK=false` 仅表示"存在需要人工处理的冲突"，不代表整版回退；
- 有冲突时合并结果**不持久化**到目标快照名（保守），但
  `MergeResult.Merged` 中的部分合并结果是确定、可校验的。

### 幂等与可复现

- **幂等重放**：`Engine.Replay(base, result, n, sides...)` 每轮从同一基线
  重新合并，逐轮比对，结果不一致返回 `non_idempotent_replay`。
- **校验式再合并不漂移**：hunk 应用是幂等的——按基线位置升序应用并
  跟踪行数位移，若某 hunk 的"新行"已出现在其（位移修正后的）位置上
  则跳过。因此把上一次输出当作新基线、把同一批变更集再应用一遍，
  插入不会堆叠、替换不会重复、rename 不会清空目标内容。
  已知取舍：要插入的行与锚点处现有行逐字相同时，无法与"已应用过"
  区分，会被视为幂等跳过。
- **两层合并串联**：上一层输出作为下一层基线、变更集针对各自基线
  构造时，整条链是确定的，重跑结果逐字节一致。

关键原则：

- 每个冲突带有 `Kind / Path / SideA / SideB / Detail / Evidence`，
  `MergeResult.Explain()` 输出可读的判定报告，可据报告复现判定。

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
  相同变更幂等、**幂等重放**、输入排列确定性；
  **插入场景**（`insert_test.go`）：单侧插入、文件末尾插入、空文件插入、
  双侧同锚点同内容（幂等）/不同内容（冲突）、不同锚点双侧插入、
  插入+不相交修改并存、插入锚点落在被改写行上的保守冲突、
  插入重放稳定性、**校验式再合并不漂移**；
  **混合场景**（`mixed_test.go`）：冲突与自动合并在同一次合并中并存、
  hunk 级部分接受、同一输入连跑 25 次结论逐字节一致、两层合并串联。
  每个用例都通过 `logThreeWay`/`logVerdict` 在测试日志中打印基线、
  两侧输入与判定依据（冲突 kind、evidence、applied 列表）。
- `ingest` 包：各类**非法输入**（缺字段、非法行区间、长度不一致、重复路径）
  的可区分拒绝原因；**插入形态接受/拒绝**（`insert_test.go`）：合法插入
  （中部/末尾/空文件/首行前）归一化为零宽 hunk，形似插入的非法记录
  （多行区间无旧行、空 hunk、负锚点、新区间不符）按各自 reason 拒绝；
  库**语法错误 vs 内部 panic** 的分类差异；原始上下文保留。
- 根包端到端：文本 diff 侧与结构化侧跨格式合并、冲突重放一致性、
  **插入+冲突混合场景的并存判定与 10 次重放零漂移**、非法输入不入库、
  库原生异常不透出。

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
