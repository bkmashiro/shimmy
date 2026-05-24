# shimmy-wasm TODO

## #1 — Uffd 真正跑起来（dirty-page restore）

**状态：** 实现完整，CI 验证进行中。

**目标：** `Take` 只做 memcpy，`Restore` 只复制脏页（~10% dirty → ~10x restore加速），
对 reactor-python 的 ~26 MB CPython 堆意义最大。

**实现路径：已走通（UffdStrategy + 直接注册 wazero 线性内存）**

`UffdStrategy`（`snapshot_uffd_linux.go`）已完整实现：
- `Take`：memcpy 到 snapshot，re-arm WP 整个区域，清 dirty 位图
- `Restore`：只 memcpy 脏页，re-arm WP，清 dirty 位图
- `faultLoop`：阻塞读 uffd_msg，WP fault → 标记脏页，disarm WP，让 WASM 继续写

**为何能直接注册 wazero 线性内存：**

之前 TODO 里的"MAP_SHARED"假设**不正确**。经过代码调查：

1. `wazero/internal/wasm/memory.go` 里 `NewMemoryInstance` 用 `make([]byte, N)` 分配线性内存
2. `api.Memory.Read(0, size)` 返回的是 `m.Buffer[0:size]`——直接指向 backing store，不做拷贝
3. `unsafe.SliceData(buf)` 拿到 `m.Buffer[0]` 的地址
4. Go runtime 对大型 `make([]byte, N)` 使用 `mmap(MAP_ANON|MAP_PRIVATE)`
5. `MAP_PRIVATE` 满足 `UFFDIO_REGISTER_MODE_WP` 的内核要求（`VM_SHARED` 的才会 EINVAL）

因此**不需要 fork wazero**，也不需要自定义 allocator。

**已完成：**
- `UffdStrategy` 完整实现（faultLoop, Take, Restore, Close）
- `supervisor_linux.go` `selectStrategy("uffd")` 路径已接入 `NewUffdStrategy(mem)`
- 新增 `TestUffdProbe_GoMakeSlice`：专门测 `make([]byte, N)` 能否注册 uffd WP
- `uffd-probe.yml` CI 新增：`TestUffdStrategy_DirtyPageTracking` + `SupervisorIntegration`

**待 CI 确认（`feat/wasm-backend` 分支，`uffd-probe.yml`）：**
- `TestUffdProbe_GoMakeSlice` pass → Go 大 slice 确实是 MAP_PRIVATE，uffd WP 兼容
- `TestUffdStrategy_DirtyPageTracking` pass → end-to-end 验证
- `TestUffdStrategy_SupervisorIntegration` pass → 线上用法验证

**如果 CI 失败：**
可能原因：
1. Go heap arena 边界导致地址范围跨 VMA（unlikely for large alloc）
2. 地址未 page-align（unlikely for large alloc）
3. 其他内核限制

备用方案：用 `experimental.MemoryAllocator` 自定义分配器，直接 `mmap(MAP_PRIVATE)` 返回已知地址，走 wazero 的 allocator 接口。代码改动 < 50 行。

---

## #2 — scipy 自动路由

当 `eval.py` 里有 `import scipy` / `import pandas` 时，自动切到 Pyodide/Node.js 子进程，
否则用 `reactor-python`。需要：
- 静态 import 扫描（正则或 AST parse）
- dispatcher factory 根据扫描结果选后端

**状态：** 未开始

---

## #3 — 删除 `python-wasm`（per-request）路径

`internal/execution/wasm/python.go` + `PythonRunner` 已标 deprecated，
可以直接删除，减少维护面。

**状态：** 未开始

---

## #4 — Rust/C/C++ CI 测试

`examples/eval-rust/` 等存在但 CI 没跑。加 workflow 验证编译 + dispatcher 测试。

**状态：** 未开始
