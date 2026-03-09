======================================================================
SHIMMY SANDBOX SECURITY REPORT
======================================================================

Date: 2026-03-09 12:53:00
Sandbox: ./sandbox_exec --cpu 2 --mem 64 --timeout 5 --no-network --no-fork --clean-env...

## Summary

Total Tests: 60
Passed: 60 (100.0%)
Failed: 0

## Results by Category

### ✅ advanced (21/21)

  ✅ cgroup_escape: Blocked (exit=2)
  ✅ core_pattern: Blocked (exit=2)
  ✅ futex_wake: Blocked (exit=2)
  ✅ inotify_spy: Blocked (exit=2)
  ✅ keyring: Blocked (exit=2)
  ✅ landlock: Blocked (exit=2)
  ✅ memfd_exec: Blocked (exit=2)
  ✅ memfd_secret: Blocked (exit=2)
  ✅ mprotect_exec: Blocked (exit=2)
  ✅ oom_trigger: Blocked (exit=2)
  ✅ open_tree: Blocked (exit=2)
  ✅ personality: Blocked (exit=2)
  ✅ prctl_dangerous: Blocked (exit=2)
  ✅ prctl_seccomp: Blocked (exit=2)
  ✅ proc_mem_write: Blocked (exit=2)
  ✅ proc_sys_write: Blocked (exit=2)
  ✅ resource_exhaust_advanced: Blocked (exit=2)
  ✅ sched_attack: Blocked (exit=2)
  ✅ timerfd_signal: Blocked (exit=2)
  ✅ toctou_symlink: Blocked (exit=2)
  ✅ unix_socket: Blocked (exit=2)

### ✅ advanced_r3 (5/5)

  ✅ madvise_dontneed: Blocked (exit=2)
  ✅ membarrier: Blocked (exit=2)
  ✅ memfd_execveat: Blocked (exit=2)
  ✅ prctl_set_mm: Blocked (exit=2)
  ✅ tiocsti: Blocked (exit=2)

### ✅ escape (3/3)

  ✅ chroot: Blocked (exit=2)
  ✅ kernel_module: Blocked (exit=2)
  ✅ pivot_root: Blocked (exit=2)

### ✅ info_leak (5/5)

  ✅ parent_environ: Blocked (exit=2)
  ✅ proc_environ: Blocked (exit=2)
  ✅ proc_maps: Blocked (exit=2)
  ✅ proc_net: Blocked (exit=2)
  ✅ process_vm_read: Blocked (exit=2)

### ✅ network (4/4)

  ✅ dns_exfil: Blocked (exit=2)
  ✅ raw_socket: Blocked (exit=2)
  ✅ tcp_connect: Blocked (exit=2)
  ✅ udp_send: Blocked (exit=2)

### ✅ persistence (4/4)

  ✅ env_pollution: Blocked (exit=2)
  ✅ semaphore: Blocked (exit=2)
  ✅ shmem: Blocked (exit=2)
  ✅ write_tmp: Blocked (exit=2)

### ✅ privilege (5/5)

  ✅ bpf: Blocked (exit=2)
  ✅ io_uring: Blocked (exit=2)
  ✅ mount: Blocked (exit=2)
  ✅ ptrace_parent: Blocked (exit=2)
  ✅ unshare_ns: Blocked (exit=2)

### ✅ process (3/3)

  ✅ clone3: Blocked (exit=2)
  ✅ fork: Blocked (exit=2)
  ✅ subprocess_exec: Blocked (exit=2)

### ✅ resource_exhaustion (5/5)

  ✅ cpu_bomb: Blocked (exit=2)
  ✅ disk_bomb: Blocked (exit=2)
  ✅ fd_exhaustion: Blocked (exit=2)
  ✅ fork_bomb: Blocked (exit=2)
  ✅ memory_bomb: Blocked (exit=2)

### ✅ surface_attacks (5/5)

  ✅ copy_file_range: Blocked (exit=2)
  ✅ env_harvest: Blocked (exit=2)
  ✅ prctl_stealth: Blocked (exit=2)
  ✅ shellcode_exec: Blocked (exit=2)
  ✅ vmsplice_exfil: Blocked (exit=2)

## Blocked Syscalls (47)

Network: socket, connect, bind, listen, accept, accept4,
         sendto, recvfrom, sendmsg, recvmsg, socketpair
Process: clone (no CLONE_THREAD)
Debug:   ptrace, process_vm_readv, process_vm_writev
Kernel:  io_uring_*, bpf, userfaultfd, perf_event_open
Keys:    keyctl, add_key, request_key
NS:      unshare, setns
FS:      mount, umount2, chroot, pivot_root
System:  reboot, kexec_load, kexec_file_load
Module:  init_module, finit_module, delete_module
Misc:    acct, swap*, set*name, *time*, io*, modify_ldt

## Resource Limits

  CPU:    2 seconds
  Memory: 64 MB
  Files:  10 MB max size
  FDs:    100 max open
  Procs:  10 max (per-user)

## Known Limitations

  - /proc/self/* readable (Linux limitation)
  - /proc/net/* readable (info leak)
  - RLIMIT_NPROC is per-user, not per-sandbox
  - Cannot mount private /tmp without CAP_SYS_ADMIN

🔒 ALL SECURITY TESTS PASSED!