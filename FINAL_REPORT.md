# Shimmy Sandbox 安全研究最终报告

**作者:** Akashi (CTO)  
**日期:** 2026-03-09  
**状态:** ✅ 研究完成，可提交PR

---

## 📊 执行摘要

| 指标 | 数值 |
|------|------|
| 威胁测试总数 | 29 |
| 通过率 | **100%** |
| 阻断的syscall | 47 |
| 资源限制 | 6种 |
| 代码量 | 269行 C |
| 性能开销 | ~1.5ms |

---

## 🎯 项目目标

为Lambda Feedback的shimmy项目实现用户空间沙箱，用于安全执行学生提交的代码。

**约束条件:**
- 无root权限
- 无KVM/容器特权
- Lambda已有seccomp（需兼容）
- 需支持Python运行

---

## 🔒 安全能力

### 已阻断的Syscall (47个)

| 类别 | Syscall | 数量 |
|------|---------|------|
| 网络 | socket, connect, bind, listen, accept, accept4, sendto, recvfrom, sendmsg, recvmsg, socketpair | 11 |
| 进程 | clone (无CLONE_THREAD) | 1 |
| 调试 | ptrace, process_vm_readv, process_vm_writev | 3 |
| 内核接口 | io_uring_setup/enter/register, bpf, userfaultfd, perf_event_open | 6 |
| 密钥 | keyctl, add_key, request_key | 3 |
| 命名空间 | unshare, setns | 2 |
| 文件系统 | mount, umount2, chroot, pivot_root | 4 |
| 系统控制 | reboot, kexec_load, kexec_file_load | 3 |
| 模块 | init_module, finit_module, delete_module | 3 |
| 资源 | acct, swapon, swapoff | 3 |
| 身份 | sethostname, setdomainname | 2 |
| 时间 | settimeofday, clock_settime, adjtimex | 3 |
| 硬件 | ioperm, iopl, modify_ldt | 3 |

### 资源限制

| 资源 | 实现 | 默认值 |
|------|------|--------|
| CPU时间 | RLIMIT_CPU | 5秒 |
| 内存 | RLIMIT_AS | 256MB |
| 文件大小 | RLIMIT_FSIZE | 10MB |
| 打开文件数 | RLIMIT_NOFILE | 100 |
| 进程数 | RLIMIT_NPROC | 10 |
| 栈大小 | RLIMIT_STACK | 8MB |

### 环境隔离

| 功能 | 实现 |
|------|------|
| 环境变量清理 | `--clean-env` |
| 临时目录隔离 | `--isolate-tmp` |
| 工作目录指定 | `--workdir` |

---

## 🧪 威胁测试结果

### 按类别统计

| 类别 | 测试数 | 通过 | 状态 |
|------|--------|------|------|
| resource_exhaustion | 5 | 5 | ✅ |
| network | 4 | 4 | ✅ |
| process | 3 | 3 | ✅ |
| privilege | 5 | 5 | ✅ |
| info_leak | 5 | 5 | ✅ |
| escape | 3 | 3 | ✅ |
| persistence | 4 | 4 | ✅ |
| **总计** | **29** | **29** | **100%** |

### 威胁测试清单

```
threats/
├── resource_exhaustion/
│   ├── fork_bomb.py       ✅
│   ├── memory_bomb.py     ✅
│   ├── cpu_bomb.py        ✅
│   ├── disk_bomb.py       ✅
│   └── fd_exhaustion.py   ✅
├── network/
│   ├── tcp_connect.py     ✅
│   ├── udp_send.py        ✅
│   ├── dns_exfil.py       ✅
│   └── raw_socket.py      ✅
├── process/
│   ├── fork.py            ✅
│   ├── subprocess_exec.py ✅
│   └── clone3.py          ✅
├── privilege/
│   ├── ptrace_parent.py   ✅
│   ├── io_uring.py        ✅
│   ├── bpf.py             ✅
│   ├── mount.py           ✅
│   └── unshare_ns.py      ✅
├── info_leak/
│   ├── proc_environ.py    ✅
│   ├── proc_maps.py       ✅
│   ├── proc_net.py        ✅
│   ├── parent_environ.py  ✅
│   └── process_vm_read.py ✅
├── escape/
│   ├── chroot.py          ✅
│   ├── pivot_root.py      ✅
│   └── kernel_module.py   ✅
└── persistence/
    ├── write_tmp.py       ✅
    ├── shmem.py           ✅
    ├── semaphore.py       ✅
    └── env_pollution.py   ✅
```

---

## ⚠️ 已知限制

| 限制 | 原因 | 影响 | 缓解 |
|------|------|------|------|
| /proc可读 | 需mount ns | 信息泄露 | --clean-env |
| NPROC每用户 | Linux设计 | fork炸弹受限 | Lambda容器级限制 |
| 无法mount私有/tmp | 需CAP_SYS_ADMIN | 跨运行污染 | --isolate-tmp (创建子目录) |

---

## 📁 交付物

| 文件 | 描述 | 行数 |
|------|------|------|
| `sandbox_exec.c` | 主程序 | 269 |
| `threats/` | 威胁测试库 | 29个文件 |
| `benchmark_threats.py` | Benchmark工具 | 180 |
| `THREAT_MODEL.md` | 威胁模型文档 | 163 |
| `SECURITY_REPORT.md` | 安全报告 | 自动生成 |
| `benchmark_results.json` | JSON结果 | 自动生成 |

---

## 🚀 使用方法

```bash
# 编译
gcc -O2 -o sandbox_exec sandbox_exec.c -lseccomp

# 基本使用
./sandbox_exec -- python3 student.py

# 完整安全模式
./sandbox_exec \
  --cpu 5 \
  --mem 128 \
  --timeout 30 \
  --no-network \
  --no-fork \
  --clean-env \
  --isolate-tmp \
  -- python3 student.py

# 运行benchmark
python3 benchmark_threats.py
```

---

## 🔄 与shimmy集成

```go
// internal/execution/worker/worker_unix.go
func (w *ProcessWorker) initCmd(ctx context.Context, cmd string, args []string) *exec.Cmd {
    sandboxArgs := []string{
        "--cpu", "5",
        "--mem", "256",
        "--no-network",
        "--no-fork",
        "--clean-env",
        "--isolate-tmp",
        "--",
        cmd,
    }
    sandboxArgs = append(sandboxArgs, args...)
    return exec.CommandContext(ctx, "sandbox_exec", sandboxArgs...)
}
```

---

## ✅ 结论

sandbox_exec v5 满足Lambda环境下学生代码执行的安全需求：

1. **100%威胁测试通过**
2. **47个危险syscall阻断**
3. **6种资源限制**
4. **环境隔离支持**
5. **~1.5ms性能开销**

**建议:** 提交PR，在Lambda实际环境验证后合并。

---

*报告生成时间: 2026-03-09 12:01 GMT*
