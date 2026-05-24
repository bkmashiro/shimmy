# shimmy-wasm TODO

## #1 — Uffd 真正跑起来（dirty-page restore）

**状态：** 代码完整，但目前 `UffdStrategy` 被 supervisor 选中时实际上无法注册
WASM 线性内存，退回 FullMemcpy。原因见下。

**目标：** `Take` 只做 memcpy，`Restore` 只复制脏页（~10% dirty → ~10x restore加速），
对 reactor-python 的 ~26 MB CPython 堆意义最大。

**根本问题：wazero MAP_SHARED**

wazero 在 Linux 上用 `mmap(MAP_ANONYMOUS | MAP_SHARED)` 分配线性内存。
`UFFDIO_REGISTER_MODE_WP` 要求映射是 `MAP_PRIVATE`（内核拒绝 MAP_SHARED）。
所以即使 `unsafe.SliceData(mem.Read(0, size))` 能拿到正确的基址，
`UFFDIO_REGISTER` ioctl 也会返回 `EINVAL`。

**三条可行路径（按可行性排序）：**

### 路径 A：patch wazero，改 MAP_PRIVATE（推荐）

- 改 `wazero/internal/platform/mmap_linux.go`，把 `MAP_SHARED` 换成 `MAP_PRIVATE`
- 对 wazero 语义无破坏（WASM 线性内存本来就是单进程私有的）
- 改动 < 5 行，可以提 upstream PR
- 改了之后现有 `NewUffdStrategy` 里的 `UFFDIO_REGISTER` 就能成功

**实验步骤：**
1. fork wazero，改 mmap_linux.go
2. `go.mod` replace 指向本地 fork
3. 跑 `TestUffdStrategy_EndToEnd` 和 `TestUffdStrategy_SupervisorIntegration`
4. 如果测试过，benchmark 对比 FullMemcpy vs Uffd at 26MB/10% dirty

### 路径 B：自己 mmap，绕过 wazero 内存分配

- 在 `NewUffdStrategy` 里额外 `mmap(MAP_PRIVATE)` 一块同等大小的区域
- 把 WASM 线性内存内容 memcpy 进去，注册这块区域到 uffd
- Restore 时把脏页从这块区域写回 WASM 线性内存
- 问题：两块内存不同步，WASM 写的是原始地址，uffd 监控的是副本地址，无法捕获写事件

→ 方案不可行，跳过。

### 路径 C：mprotect 替代（已有实现，但进程级限制）

- `MprotectStrategy` 已完整实现，对单实例有效
- 多实例时 SIGSEGV handler 进程共享，无法区分来自哪个实例 → 已有多实例 guard

→ 对单实例部署可用，不是 uffd 的替代。

**计划：先走路径 A。**

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
