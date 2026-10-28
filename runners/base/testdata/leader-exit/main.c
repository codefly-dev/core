// Linux keeps this process alive after pthread_exit removes /proc/PID/exe.
// The surviving worker answers one request so the test proves actual liveness.
#include <pthread.h>
#include <unistd.h>

static pthread_mutex_t gate = PTHREAD_MUTEX_INITIALIZER;

static void *answer(void *unused) {
    (void)unused;
    pthread_mutex_lock(&gate);
    pthread_mutex_unlock(&gate);
    char value;
    if (read(STDIN_FILENO, &value, 1) != 1 ||
        write(STDOUT_FILENO, &value, 1) != 1)
        _exit(2);
    return NULL;
}

int main(void) {
    pthread_t worker;
    pthread_mutex_lock(&gate);
    if (pthread_create(&worker, NULL, answer, NULL) != 0)
        return 2;
    char release;
    if (write(STDOUT_FILENO, "R", 1) != 1 ||
        read(STDIN_FILENO, &release, 1) != 1)
        return 2;
    pthread_mutex_unlock(&gate);
    pthread_exit(NULL);
}
