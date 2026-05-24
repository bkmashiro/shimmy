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

## #2 — scipy 自动路由 ✅ 完成

当 `eval.py` 里有 `import scipy` / `import pandas` / `import statsmodels` / `import sklearn` / `import matplotlib` / `import seaborn` 时，自动切到 Pyodide/Node.js 子进程，否则用 `reactor-python`。

**实现：**
- `internal/execution/wasm/import_scan.go`：`ScriptNeedsHeavyRuntime(src string) bool`
  - `(?m)^\s*(?:import|from)\s+(scipy|pandas|...) (?:\s|\.|\r?\n|$)` 行首 regexp，不匹配注释行
  - `ScriptFileNeedsHeavyRuntime(path string)` 读文件版本
- `internal/execution/wasm/import_scan_test.go`：单元测试覆盖注释/字符串/word-boundary 边界情况
- `internal/execution/dispatcher.go` `ReactorPythonIO` case：启动前 scan 脚本
  - heavy → 透明路由至 `FUNCTION_PYODIDE_RUNNER`（Node.js/Pyodide）
  - 轻量 → 正常走 `ReactorPythonDispatcher`（CPython-WASI）
  - scan 失败（文件不可读）→ 保守回退到 reactor，记 warn 日志

**注意事项：**
- 不捕获 `__import__("scipy")` 等动态 import（可接受，运营者可手动指定 `PyodideIO`）
- `FUNCTION_PYODIDE_RUNNER` 未设置时默认用 `runner.js`（与 `PyodideIO` case 行为一致）

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
