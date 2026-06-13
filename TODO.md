# shimmy-wasm TODO

## #1 — Uffd 真正跑起来（dirty-page restore） ✅ 完成

**状态：** 完整实现，CI + Lambda 均已验证。

**目标：** `Take` 只做 memcpy，`Restore` 只复制脏页（~10% dirty → ~10x restore加速），
对 reactor-python 的 ~26 MB CPython 堆意义最大。

**已完成：**
- `UffdStrategy` 完整实现（faultLoop, Take, Restore, Close）
  - `faultLoop`：`unix.Poll` + wakeup pipe，可靠关闭
  - `Restore`：O(1) 单 `UFFDIO_WRITEPROTECT` ioctl，`unsafe.Slice` 批量拷贝
- `supervisor_linux.go` `selectStrategy("uffd")` 路径已接入
- CI（Ubuntu）测试全通过：`TestUffdStrategy_DirtyPageTracking`，`SupervisorIntegration`，`TestUffdProbe_GoMakeSlice`
- **Lambda 验证（eu-west-2，AL2 + AL2023）**：`uffd_wp_register ✅`（`features=0x1ff`，MAP_PRIVATE 注册成功）
- 无需 fork wazero，无需自定义分配器（`make([]byte, N)` → `MAP_PRIVATE`，直接兼容）

**基准数据（CI，10% dirty）：**
- 3 MB：uffd Restore ~32 µs vs memcpy ~204 µs → **6.3× 加速**
- 26 MB（CPython 堆）：uffd ~310 µs vs memcpy ~3123 µs → **10× 加速**

---

## #2 — Python runtime policy: Pyodide 默认，reactor 作为性能优化

不要做 requirements/import 自动路由。Python eval function 的兼容默认路径应是 `pyodide`：尽量支持 Lambda Feedback 现有 Python 包生态；当某个 evaluator 需要性能优化、且依赖集已知可在 CPython-WASI/reactor 中运行时，再由部署配置显式切到 `reactor-python`。

**待办：**
- 将文档中“根据 requirements 自动选择 runtime”的描述改成“部署配置显式选择 runtime”。
- Pyodide runner 先做 Lambda Feedback compatibility adapter：package layout、`lf_toolkit.Result` 序列化、preview 签名兼容。
- reactor-python 保留为 fast path，不作为默认 Python 路径，也不通过扫描自动接管。

---

## #3 — 删除 `python-wasm`（per-request）路径 ✅ 完成

`internal/execution/wasm/python.go` + `python_test.go` 已删除。
`parseJSONResponse` 工具函数已移至 `json_util.go`（reactor + resident 继续使用）。

---

## #4 — Rust/C/C++ CI 测试 ✅ 完成

`.github/workflows/build-wasm-examples.yml` 已包含：
- `build-rust`：`cargo build --target wasm32-wasip1 --release`
- `build-c-cpp`：WASI-SDK 25 编译 eval-c + eval-cpp
- `build-adversarial`：所有对抗性 WASM 模块
- `smoke-test`：`go test ./examples/...`（TestEvalRust, TestEvalC, TestEvalCpp）

触发条件：`examples/**` 变更时自动运行（main + feat/wasm-backend + PR）。
