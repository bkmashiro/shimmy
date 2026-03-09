# Shimmy Sandbox 完整威胁模型

## 1. 资源耗尽攻击

| 攻击 | Syscall | 我们的防护 | 状态 |
|------|---------|-----------|------|
| CPU耗尽 | - | RLIMIT_CPU | ✅ |
| 内存耗尽 | brk, mmap | RLIMIT_AS | ✅ |
| 磁盘耗尽 | write | RLIMIT_FSIZE | ✅ |
| FD耗尽 | open, socket | RLIMIT_NOFILE | ✅ |
| 进程炸弹 | clone, fork | RLIMIT_NPROC | ⚠️ 每用户 |
| 栈溢出 | - | RLIMIT_STACK | ❓ 未设 |
| 锁文件 | flock | - | ❓ 未防 |
| inotify耗尽 | inotify_init | - | ❓ 未防 |
| 信号队列 | rt_sigqueueinfo | RLIMIT_SIGPENDING | ❓ 未设 |
| msgqueue | mq_open | RLIMIT_MSGQUEUE | ❓ 未设 |

## 2. 网络攻击

| 攻击 | Syscall | 防护 | 状态 |
|------|---------|------|------|
| TCP连接 | socket, connect | seccomp | ✅ |
| UDP发包 | socket, sendto | seccomp | ✅ |
| Raw socket | socket(RAW) | seccomp | ✅ |
| Unix socket | socket(AF_UNIX) | seccomp | ✅ |
| Netlink | socket(AF_NETLINK) | seccomp | ✅ |
| socketpair | socketpair | seccomp | ✅ |
| sendfile | sendfile | - | ❓ 未防 |
| splice到socket | splice | - | ❓ 未防 |

## 3. 进程/线程操作

| 攻击 | Syscall | 防护 | 状态 |
|------|---------|------|------|
| fork | clone(无THREAD) | seccomp | ✅ |
| vfork | vfork | seccomp继承 | ✅ |
| clone3 | clone3 | - | ❓ 未防 |
| 线程 | clone(THREAD) | 允许 | ✅ |
| exec | execve, execveat | 允许 | ⚠️ |
| kill其他进程 | kill, tkill | - | ❓ 未防 |
| 修改调度 | sched_setscheduler | - | ❓ |
| 优先级 | setpriority, nice | - | ❓ |

## 4. 进程间攻击

| 攻击 | Syscall | 防护 | 状态 |
|------|---------|------|------|
| ptrace父进程 | ptrace | seccomp | ✅ |
| 读其他进程内存 | process_vm_readv | seccomp | ✅ |
| 写其他进程内存 | process_vm_writev | seccomp | ✅ |
| 共享内存 | shmat, shmget | - | ❓ 未防 |
| 信号量 | semget, semop | - | ❓ 未防 |
| 消息队列 | msgget, msgsnd | - | ❓ 未防 |
| POSIX mq | mq_open | - | ❓ 未防 |

## 5. 文件系统攻击

| 攻击 | Syscall | 防护 | 状态 |
|------|---------|------|------|
| 读敏感文件 | read, open | - | ❓ 未防 |
| /proc信息泄露 | open(/proc/*) | - | ❌ 可读 |
| /sys信息泄露 | open(/sys/*) | - | ❌ 可读 |
| 符号链接攻击 | symlink, link | - | ❓ |
| 设备访问 | open(/dev/*) | - | ❓ |
| 挂载 | mount, umount | seccomp | ✅ |
| chroot逃逸 | chroot | - | ❓ 未防 |
| pivot_root | pivot_root | - | ❓ 未防 |

## 6. 权限提升

| 攻击 | Syscall | 防护 | 状态 |
|------|---------|------|------|
| setuid | setuid, setgid | NO_NEW_PRIVS | ✅ |
| 能力获取 | capset | NO_NEW_PRIVS | ✅ |
| 命名空间 | unshare, setns | seccomp | ✅ |
| 内核模块 | init_module | seccomp | ✅ |
| kexec | kexec_load | seccomp | ✅ |
| 重启 | reboot | seccomp | ✅ |

## 7. 内核交互

| 攻击 | Syscall | 防护 | 状态 |
|------|---------|------|------|
| eBPF注入 | bpf | seccomp | ✅ |
| perf采样 | perf_event_open | seccomp | ✅ |
| userfaultfd竞态 | userfaultfd | seccomp | ✅ |
| io_uring | io_uring_setup | - | ❓ 未防 |
| keyring | keyctl | seccomp | ✅ |
| seccomp自己 | seccomp | - | ❓ |
| prctl | prctl | - | ❓ |

## 8. 信息泄露

| 目标 | 路径/方法 | 防护 | 状态 |
|------|----------|------|------|
| 环境变量 | /proc/self/environ | clean-env | ⚠️ |
| 内存布局 | /proc/self/maps | - | ❌ |
| 网络状态 | /proc/net/* | - | ❌ |
| 系统信息 | /proc/cpuinfo等 | - | ❌ |
| 内核版本 | uname | - | ❌ |
| 主机名 | gethostname | - | ❌ |
| 时间 | gettimeofday | 允许 | ✅ |
| 随机数 | getrandom | 允许 | ✅ |

## 9. 时序/侧信道

| 攻击 | 方法 | 防护 | 状态 |
|------|------|------|------|
| 计时攻击 | rdtsc, clock_gettime | - | ❌ 不防 |
| CPU缓存 | 访存模式 | - | ❌ 不防 |
| 功耗侧信道 | - | - | N/A |

---

## Linux x86_64 所有Syscall分类

### 已阻断 (23个)
```
socket, connect, bind, listen, accept, accept4
sendto, recvfrom, sendmsg, recvmsg, socketpair
clone(无THREAD)
ptrace, process_vm_readv, process_vm_writev
userfaultfd, perf_event_open, bpf
keyctl, add_key, request_key
unshare, setns, mount, umount2
reboot, kexec_load, kexec_file_load
init_module, finit_module, delete_module
```

### 应该阻断但未阻断 (建议添加)
```
# 进程间通信
shmget, shmat, shmdt, shmctl        # System V共享内存
semget, semop, semctl               # System V信号量
msgget, msgsnd, msgrcv, msgctl      # System V消息队列
mq_open, mq_send, mq_receive        # POSIX消息队列

# 危险操作
io_uring_setup, io_uring_enter      # io_uring (可绕过seccomp!)
clone3                              # 新版clone
chroot, pivot_root                  # 文件系统隔离
acct                                # 进程记账
quotactl                            # 磁盘配额
swapon, swapoff                     # 交换分区
sethostname, setdomainname          # 主机名
settimeofday, clock_settime         # 时间修改
adjtimex                            # 时间调整
ioperm, iopl                        # IO权限
modify_ldt                          # LDT修改
vm86, vm86old                       # VM86模式
lookup_dcookie                      # 内核cookie
personality                         # 执行域
```

### 允许但需注意
```
execve, execveat    # 可以执行其他程序
kill, tkill, tgkill # 可以发信号（限本进程组）
prctl               # 部分危险
ioctl               # 设备相关
fcntl               # 文件控制
mmap, mprotect      # 内存映射（JIT需要）
```
