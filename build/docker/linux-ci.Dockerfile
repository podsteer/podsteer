# A Linux build environment matching what CI installs, for reproducing
# Linux-only failures from a developer's own machine.
#
# It exists because one was worth a day: a local shell that would not stop on
# Linux was diagnosed in five minutes here after being reasoned about
# fruitlessly from macOS. `ps` inside the container showed a grandchild that
# had escaped its process group holding the terminal open, which no amount of
# reading the code from the other platform was going to reveal.
#
#   docker build -f build/docker/linux-ci.Dockerfile -t podsteer-linux .
#   docker run --rm -v "$PWD":/src -w /src podsteer-linux \
#       go test -race -tags gtk3 ./app/...
#
# The GTK3 packages and the build tag go together: v3 defaults to gtk4 with
# webkitgtk-6.0, which Ubuntu 22.04 does not carry, so the application opts
# into the gtk3 backend and this image installs what that needs. Keep both in
# step with the workflow's apt list.
FROM golang:1.27

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        libgtk-3-dev \
        libwebkit2gtk-4.1-dev \
        pkg-config \
        procps \
    && rm -rf /var/lib/apt/lists/*

ENV GOCACHE=/tmp/gocache \
    GOMODCACHE=/tmp/gomod \
    CGO_ENABLED=1
