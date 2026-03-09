# Sandbox Attack Surface - Complete Enumeration

## 1. 可写入位置

| 位置 | 状态 | 风险 | 缓解措施 |
|------|------|------|----------|
| `/tmp` | ✅ 可写 | 跨运行污染 | --isolate-tmp |
| `/var/tmp` | ✅ 可写 | 跨运行污染 | shimmy清理 |
| `/dev/shm` | ✅ 可写 | **跨运行污染** | **shimmy需清理** |
| `/etc` | ✅ 可写* | 容器内无影响 | 容器隔离 |
| `/var` | ✅ 可写* | 容器内无影响 | 容器隔离 |
| `/home` | ✅ 可写* | 容器内无影响 | 容器隔离 |

### 可写/dev设备

| 设备 | 风险 |
|------|------|
| `/dev/null` | 无 |
| `/dev/zero` | 无 |
| `/dev/random` | 无 |
| `/dev/urandom` | 无 |
| `/dev/full` | 无 |
| `/dev/tty` | 无 (sandbox无tty) |
| `/dev/ptmx` | 无 (TIOCSTI已测试失败) |
| `/dev/stdin/out/err` | 无 |

## 2. Syscall状态

### ✅ 允许的 (必需)

| Syscall | 用途 | 风险评估 |
|---------|------|----------|
| open/read/write/close | 文件IO | 低 - 有rlimit |
| mmap/mprotect/munmap | 内存管理 | 低 - Python需要 |
| brk | 堆分配 | 无 |
| execve/execveat | 执行程序 | 中 - 允许运行命令 |
| pipe/dup | IPC | 低 |
| futex | 同步 | 无 |
| clock_gettime | 时间 | 无 |
| getrandom | 随机数 | 无 |
| prctl (部分) | 进程属性 | 低 |
| ioctl (部分) | 设备控制 | 低 - 危险cmd已测试 |
| fcntl | 文件控制 | 低 |

### ❌ 阻断的 (65+)

```
网络:     socket connect bind listen accept accept4 
          sendto recvfrom sendmsg recvmsg socketpair
进程:     clone(无THREAD) ptrace process_vm_readv process_vm_writev
信号:     kill(pid>0) kill(pid<0) tkill tgkill
内核:     io_uring_* bpf userfaultfd perf_event_open
密钥:     keyctl add_key request_key
监控:     inotify_* fanotify_*
文件:     mount umount2 symlink symlinkat link linkat
          chroot pivot_root open_tree move_mount fsopen fspick fsconfig fsmount
系统:     reboot kexec_load kexec_file_load init_module finit_module delete_module
          acct swapon swapoff sethostname setdomainname
          settimeofday clock_settime adjtimex
硬件:     ioperm iopl modify_ldt
其他:     personality quotactl nfsservctl
```

## 3. 可读信息

### /proc (信息泄露)

| 路径 | 内容 | 风险 |
|------|------|------|
| `/proc/self/environ` | 环境变量 | ⚠️ 用clean-env缓解 |
| `/proc/self/maps` | 内存布局 | ⚠️ ASLR信息 |
| `/proc/self/cmdline` | 命令行 | 低 |
| `/proc/self/status` | 进程状态 | 低 |
| `/proc/self/cgroup` | cgroup | 低 |
| `/proc/self/mountinfo` | 挂载信息 | 低 |
| `/proc/1/environ` | PID1环境 | ⚠️ 可能含密钥 |
| `/proc/1/cmdline` | PID1命令 | 低 |
| `/proc/net/tcp` | 网络连接 | ⚠️ 但无法建连 |
| `/proc/cpuinfo` | CPU信息 | 低 |
| `/proc/version` | 内核版本 | 低 |

### 无法阻断原因

需要 `mount namespace` 挂载私有 `/proc`，但 Lambda 不允许 `unshare(CLONE_NEWNS)`

## 4. prctl选项

| 选项 | 状态 | 风险 |
|------|------|------|
| PR_SET_PDEATHSIG | ✅ 允许 | 无 - 正常功能 |
| PR_GET_DUMPABLE | ✅ 允许 | 无 |
| PR_SET_NAME | ❌ 失败 | - |
| PR_GET_SECCOMP | ✅ 返回2 | 无 - 只读 |
| PR_SET_SECCOMP | ❌ 失败 | - |
| PR_SET_MM | ❌ 失败 | ✅ 已阻断 |
| PR_SET_CHILD_SUBREAPER | ✅ 允许 | 低 - fork已阻断 |
| PR_SET_NO_NEW_PRIVS | ❌ 失败 | - |
| PR_CAP_AMBIENT | ❌ 失败 | ✅ 已阻断 |

## 5. 残余风险矩阵

| 风险 | 级别 | 状态 | 备注 |
|------|------|------|------|
| 网络外连 | 高 | ✅ 已阻断 | seccomp |
| 进程逃逸 | 高 | ✅ 已阻断 | seccomp |
| 权限提升 | 高 | ✅ 已阻断 | NO_NEW_PRIVS |
| 内核攻击 | 高 | ✅ 已阻断 | io_uring/bpf |
| 信息泄露 | 中 | ⚠️ 残留 | /proc可读 |
| 跨运行污染 | 中 | ⚠️ 残留 | /dev/shm |
| DoS | 低 | ✅ 已缓解 | rlimits |

## 6. 需要shimmy配合的清理

```bash
# 每次eval前执行
rm -rf /tmp/*
rm -rf /var/tmp/*
rm -rf /dev/shm/*
```

## 7. 理论攻击向量（已测试失败）

| 攻击 | 测试结果 |
|------|----------|
| TIOCSTI终端注入 | ❌ EPERM |
| Dirty COW变种 | ❌ madvise失败 |
| memfd_create+execveat | ❌ ENOSYS |
| personality READ_IMPLIES_EXEC | ❌ EPERM |
| ptrace父进程 | ❌ EPERM |
| io_uring绕过seccomp | ❌ EPERM |
| bpf注入 | ❌ EPERM |
| TOCTOU symlink | ❌ EPERM |
| inotify监控 | ❌ EPERM |
| kill其他进程 | ❌ EPERM |

---

**结论**: 攻击面已最小化，残留风险需shimmy层面配合处理。
