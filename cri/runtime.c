//go:build ignore

#define _GNU_SOURCE

#include <errno.h>
#include <fcntl.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <sys/types.h>
#include <sys/wait.h>
#include <sys/un.h>
#include <unistd.h>

#define MAX_CONTAINERS 64
#define BUFFER_SIZE 4096

/* TODO: 現在は fork + exec までを実装している。
 * 最終形では UTS、PID、mount、rootfs、network namespace を追加する。 */

struct container {
    int used;
    char id[64];
    char pod[128];
    pid_t pid;
    const char *state;
};

static struct container containers[MAX_CONTAINERS];
static const char *socket_path = "/tmp/toy-kubernetes-runtime.sock";
static const char *bundle_dir = "bundles";
static int server_fd = -1;

static void cleanup(void)
{
    if (server_fd >= 0)
        close(server_fd);
    unlink(socket_path);
}

static void handle_signal(int signal_number)
{
    (void)signal_number;
    exit(0);
}

static void send_error(int client_fd, const char *message)
{
    dprintf(client_fd, "{\"ok\":false,\"error\":\"%s\"}\n", message);
}

static struct container *find_container(const char *pod)
{
    for (int index = 0; index < MAX_CONTAINERS; index++) {
        if (containers[index].used && strcmp(containers[index].pod, pod) == 0)
            return &containers[index];
    }
    return NULL;
}

static struct container *new_container(void)
{
    for (int index = 0; index < MAX_CONTAINERS; index++) {
        if (!containers[index].used) {
            containers[index].used = 1;
            return &containers[index];
        }
    }
    return NULL;
}

/* 終了した child process を回収し、runtime の状態を Stopped にする */
static void reap_containers(void)
{
    for (int index = 0; index < MAX_CONTAINERS; index++) {
        if (!containers[index].used || containers[index].pid <= 0)
            continue;

        if (waitpid(containers[index].pid, NULL, WNOHANG) > 0) {
            containers[index].state = "Stopped";
            containers[index].pid = 0;
        }
    }
}

static int json_value(const char *request, const char *key, char *value, size_t value_size)
{
    char pattern[64];
    int written = snprintf(pattern, sizeof(pattern), "\"%s\":\"", key);
    if (written < 0 || (size_t)written >= sizeof(pattern))
        return -1;

    const char *start = strstr(request, pattern);
    if (start == NULL)
        return -1;
    start += strlen(pattern);

    const char *end = strchr(start, '"');
    if (end == NULL || (size_t)(end - start) + 1 > value_size)
        return -1;

    memcpy(value, start, (size_t)(end - start));
    value[end - start] = '\0';
    return 0;
}

/* nginx bundle を child process として起動し、Pod と PID の対応を保存する */
static int run_container(int client_fd, const char *pod, const char *image)
{
    if (strcmp(image, "nginx") != 0) {
        send_error(client_fd, "only the nginx image is supported");
        return -1;
    }

    struct container *container = find_container(pod);
    if (container != NULL && strcmp(container->state, "Running") == 0) {
        dprintf(client_fd, "{\"ok\":true,\"id\":\"%s\",\"pod\":\"%s\",\"state\":\"Running\"}\n", container->id, container->pod);
        return 0;
    }

    if (container == NULL)
        container = new_container();
    if (container == NULL) {
        send_error(client_fd, "container limit reached");
        return -1;
    }

    char command[512];
    if (snprintf(command, sizeof(command), "%s/nginx/rootfs/usr/sbin/nginx", bundle_dir) >= (int)sizeof(command)) {
        send_error(client_fd, "bundle path is too long");
        return -1;
    }

    pid_t pid = fork();
    if (pid < 0) {
        send_error(client_fd, "fork failed");
        return -1;
    }
    if (pid == 0) {
        execl(command, "nginx", "-g", "daemon off;", (char *)NULL);
        _exit(127);
    }

    container->pid = pid;
    container->state = "Running";
    snprintf(container->id, sizeof(container->id), "pid-%ld", (long)pid);
    snprintf(container->pod, sizeof(container->pod), "%s", pod);
    dprintf(client_fd, "{\"ok\":true,\"id\":\"%s\",\"pod\":\"%s\",\"state\":\"Running\"}\n", container->id, container->pod);
    return 0;
}

/* Pod の process に終了シグナルを送り、終了を待って状態を更新する */
static int stop_container(int client_fd, const char *pod)
{
    struct container *container = find_container(pod);
    if (container == NULL) {
        send_error(client_fd, "container not found");
        return -1;
    }

    if (container->pid > 0) {
        kill(container->pid, SIGTERM);
        waitpid(container->pid, NULL, 0);
        container->pid = 0;
    }
    container->state = "Stopped";
    dprintf(client_fd, "{\"ok\":true,\"pod\":\"%s\",\"state\":\"Stopped\"}\n", pod);
    return 0;
}

static int inspect_container(int client_fd, const char *pod)
{
    struct container *container = find_container(pod);
    if (container == NULL) {
        send_error(client_fd, "container not found");
        return -1;
    }

    dprintf(client_fd, "{\"ok\":true,\"id\":\"%s\",\"pod\":\"%s\",\"state\":\"%s\"}\n", container->id, container->pod, container->state);
    return 0;
}

static void list_containers(int client_fd)
{
    dprintf(client_fd, "{\"ok\":true,\"containers\":[");
    int first = 1;
    for (int index = 0; index < MAX_CONTAINERS; index++) {
        if (!containers[index].used)
            continue;
        if (!first)
            dprintf(client_fd, ",");
        dprintf(client_fd, "{\"id\":\"%s\",\"pod\":\"%s\",\"state\":\"%s\"}", containers[index].id, containers[index].pod, containers[index].state);
        first = 0;
    }
    dprintf(client_fd, "]}\n");
}

/* 1 行の JSON request を読み取り、対応する runtime 操作へ振り分ける */
static void handle_request(int client_fd, const char *request)
{
    char operation[32];
    char pod[128];
    char image[128];

    if (json_value(request, "op", operation, sizeof(operation)) < 0) {
        send_error(client_fd, "op is required");
        return;
    }
    reap_containers();

    if (strcmp(operation, "list") == 0) {
        list_containers(client_fd);
    } else if (strcmp(operation, "run") == 0 && json_value(request, "pod", pod, sizeof(pod)) == 0 && json_value(request, "image", image, sizeof(image)) == 0) {
        run_container(client_fd, pod, image);
    } else if (strcmp(operation, "stop") == 0 && json_value(request, "pod", pod, sizeof(pod)) == 0) {
        stop_container(client_fd, pod);
    } else if (strcmp(operation, "inspect") == 0 && json_value(request, "pod", pod, sizeof(pod)) == 0) {
        inspect_container(client_fd, pod);
    } else {
        send_error(client_fd, "invalid request");
    }
}

static int parse_arguments(int argc, char **argv)
{
    for (int index = 1; index < argc; index++) {
        if (strcmp(argv[index], "--socket") == 0 && index + 1 < argc) {
            socket_path = argv[++index];
        } else if (strcmp(argv[index], "--bundle-dir") == 0 && index + 1 < argc) {
            bundle_dir = argv[++index];
        } else {
            fprintf(stderr, "usage: toy-cri --socket PATH --bundle-dir PATH\n");
            return -1;
        }
    }
    return 0;
}

int main(int argc, char **argv)
{
    if (parse_arguments(argc, argv) < 0)
        return EXIT_FAILURE;

    atexit(cleanup);
    signal(SIGINT, handle_signal);
    signal(SIGTERM, handle_signal);

    server_fd = socket(AF_UNIX, SOCK_STREAM, 0);
    if (server_fd < 0)
        return perror("socket"), EXIT_FAILURE;

    struct sockaddr_un address = {0};
    address.sun_family = AF_UNIX;
    if (strlen(socket_path) >= sizeof(address.sun_path))
        return fprintf(stderr, "socket path is too long\n"), EXIT_FAILURE;
    snprintf(address.sun_path, sizeof(address.sun_path), "%s", socket_path);
    unlink(socket_path);

    if (bind(server_fd, (struct sockaddr *)&address, sizeof(address)) < 0)
        return perror("bind"), EXIT_FAILURE;
    if (listen(server_fd, 16) < 0)
        return perror("listen"), EXIT_FAILURE;

    for (;;) {
        int client_fd = accept(server_fd, NULL, NULL);
        if (client_fd < 0) {
            if (errno == EINTR)
                continue;
            return perror("accept"), EXIT_FAILURE;
        }

        char request[BUFFER_SIZE];
        ssize_t length = read(client_fd, request, sizeof(request) - 1);
        if (length > 0) {
            request[length] = '\0';
            handle_request(client_fd, request);
        }
        close(client_fd);
    }
}
