/* Bounded DynamoRIO policy client for the Shimmy Lean demo.
 * This is evidence code, not a complete production sandbox.
 */
#include "dr_api.h"
#include <errno.h>
#include <fcntl.h>
#include <string.h>
#include <sys/mman.h>

#define SYS_write 1
#define SYS_open 2
#define SYS_mprotect 10
#define SYS_connect 42
#define SYS_clone 56
#define SYS_fork 57
#define SYS_vfork 58
#define SYS_execve 59
#define SYS_creat 85
#define SYS_openat 257
#define SYS_execveat 322
#define DENIED_READ_PATH "/tmp/shimmy-dbi-denied.txt"
#define DENIED_WRITE_PATH "/tmp/shimmy-dbi-denied-write.txt"
#define DENIED_FD 198
#define INIT_MARKER "/tmp/shimmy-dbi-client-init.marker"
#define BLOCK_MARKER "/tmp/shimmy-dbi-policy-block.marker"
#define AUDIT_PATH "/tmp/shimmy-dbi-policy-audit.jsonl"
#define ALLOW_EXEC_CHILD_MARKER "/tmp/shimmy-dbi-allow-exec-child.marker"

static void write_file_text(const char *path, const char *message, uint mode) {
    file_t file = dr_open_file(path, mode | DR_FILE_ALLOW_LARGE);
    if (file != INVALID_FILE) {
        dr_write_file(file, message, strlen(message));
        dr_close_file(file);
    }
}

static void audit(const char *rule, int sysnum) {
    char line[256];
    dr_snprintf(line, sizeof(line), "{\"decision\":\"deny\",\"rule\":\"%s\",\"syscall\":%d,\"errno\":%d}\n", rule, sysnum, EPERM);
    write_file_text(AUDIT_PATH, line, DR_FILE_WRITE_APPEND);
    write_file_text(BLOCK_MARKER, "blocked\n", DR_FILE_WRITE_OVERWRITE);
}

static bool read_app_string(const char *app, char *out, size_t cap) {
    size_t i;
    if (app == NULL || cap == 0) return false;
    for (i = 0; i + 1 < cap; i++) {
        size_t got = 0;
        if (!dr_safe_read(app + i, 1, out + i, &got) || got != 1) return false;
        if (out[i] == '\0') return true;
    }
    out[cap - 1] = '\0';
    return false;
}

static bool event_filter_syscall(void *ctx, int n) {
    (void)ctx;
    return n == SYS_open || n == SYS_openat || n == SYS_creat || n == SYS_write ||
           n == SYS_connect || n == SYS_clone || n == SYS_fork || n == SYS_vfork ||
           n == SYS_execve || n == SYS_execveat || n == SYS_mprotect;
}

static bool deny(void *ctx, const char *rule, int n) {
    audit(rule, n);
    dr_syscall_set_result(ctx, -EPERM);
    return false;
}

static bool event_pre_syscall(void *ctx, int n) {
    const char *app_path;
    char path[256];
    if (n == SYS_write && (int)dr_syscall_get_param(ctx, 0) == DENIED_FD)
        return deny(ctx, "fd_write", n);
    if (n == SYS_connect)
        return deny(ctx, "network_connect", n);
    if ((n == SYS_clone || n == SYS_fork || n == SYS_vfork) && dr_file_exists(ALLOW_EXEC_CHILD_MARKER))
        return true;
    if (n == SYS_clone || n == SYS_fork || n == SYS_vfork)
        return deny(ctx, "process_create", n);
    if (n == SYS_execve || n == SYS_execveat)
        return deny(ctx, "process_exec", n);
    if (n == SYS_mprotect && (((int)dr_syscall_get_param(ctx, 2)) & PROT_EXEC) != 0)
        return deny(ctx, "executable_memory", n);
    if (n == SYS_openat)
        app_path = (const char *)dr_syscall_get_param(ctx, 1);
    else if (n == SYS_open || n == SYS_creat)
        app_path = (const char *)dr_syscall_get_param(ctx, 0);
    else
        return true;
    if (!read_app_string(app_path, path, sizeof(path))) return true;
    if (strcmp(path, DENIED_READ_PATH) == 0)
        return deny(ctx, "filesystem_read", n);
    if (strcmp(path, DENIED_WRITE_PATH) == 0)
        return deny(ctx, "filesystem_write", n);
    return true;
}

DR_EXPORT void dr_client_main(client_id_t id, int argc, const char *argv[]) {
    (void)id; (void)argc; (void)argv;
    dr_set_client_name("shimmy-lean-demo-policy", "https://github.com/bkmashiro/shimmy-wasm-go");
    dr_register_filter_syscall_event(event_filter_syscall);
    dr_register_pre_syscall_event(event_pre_syscall);
    write_file_text(INIT_MARKER, "initialized\n", DR_FILE_WRITE_OVERWRITE);
    dr_fprintf(STDERR, "[shimmy-dbi-policy] client init pid=%d\n", dr_get_process_id());
}
