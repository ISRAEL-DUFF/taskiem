// slowsync adds a delay to every fdatasync and fsync of the process it is
// loaded into, so a Postgres on a fast disk behaves like one on a slow
// disk (docs/performance.md, "A slow disk"). It is a test aid, never
// shipped: nothing builds or loads it but a person running a load test.
//
//   gcc -shared -fPIC -O2 -o slowsync.so tools/loadtest/slowsync/slowsync.c -ldl
//   SLOWSYNC_US=10000 LD_PRELOAD=$PWD/slowsync.so pg_ctl -D <data> start
//
// SLOWSYNC_US: microseconds to add to each flush (default 0).
#define _GNU_SOURCE
#include <dlfcn.h>
#include <stdlib.h>
#include <unistd.h>
static long delay_us = -1;
static void init(void) { if (delay_us < 0) { const char *e = getenv("SLOWSYNC_US"); delay_us = e ? atol(e) : 0; } }
int fdatasync(int fd) {
  static int (*real)(int) = 0; if (!real) real = dlsym(RTLD_NEXT, "fdatasync");
  init(); if (delay_us > 0) usleep(delay_us); return real(fd);
}
int fsync(int fd) {
  static int (*real)(int) = 0; if (!real) real = dlsym(RTLD_NEXT, "fsync");
  init(); if (delay_us > 0) usleep(delay_us); return real(fd);
}
