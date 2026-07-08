#include "dr_api.h"

#if defined(ARM_64)
#  define SHIMMY_SYS_OPENAT 56
#  define SHIMMY_SYS_OPENAT2 437
#  define SHIMMY_SYS_OPEN -1
#  define SHIMMY_SYS_CREAT -1
#else
#  define SHIMMY_SYS_OPEN 2
#  define SHIMMY_SYS_CREAT 85
#  define SHIMMY_SYS_OPENAT 257
#  define SHIMMY_SYS_OPENAT2 437
#endif

static bool is_open_syscall(int sysnum) {
    return sysnum == SHIMMY_SYS_OPENAT || sysnum == SHIMMY_SYS_OPENAT2 ||
           sysnum == SHIMMY_SYS_OPEN || sysnum == SHIMMY_SYS_CREAT;
}

static bool event_filter_syscall(void *drcontext, int sysnum) {
    (void)drcontext;
    return is_open_syscall(sysnum);
}

static bool event_pre_syscall(void *drcontext, int sysnum) {
    (void)drcontext;
    if (is_open_syscall(sysnum)) {
        dr_fprintf(STDERR, "[dr-open-log] saw open-family syscall %d\n", sysnum);
    }
    return true;
}

DR_EXPORT void dr_client_main(client_id_t id, int argc, const char *argv[]) {
    (void)id;
    (void)argc;
    (void)argv;
    dr_set_client_name("shimmy-dbi-open-log", "https://github.com/bkmashiro/shimmy");
    dr_register_filter_syscall_event(event_filter_syscall);
    dr_register_pre_syscall_event(event_pre_syscall);
    dr_fprintf(STDERR, "[dr-open-log] client init pid=%d\n", dr_get_process_id());
}
