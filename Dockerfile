# alive.name is fully usable as a native binary; this image is an OPTIONAL way to
# run it with every dependency bundled, including the Python-based git-filter-repo
# that `alive reclaim` needs.
#
# Two bind mounts are REQUIRED for real use (see README and docker-entrypoint.sh):
#   -v "/path/to/working/repo:/repo"     the git repository to operate on
#   -v "/path/to/backups:/backups"       where verified backups are kept
#
# Without the /backups mount, any backup would live only inside the container and
# be lost when it is removed. The entrypoint refuses backup-creating commands
# unless /backups is mounted.

# --- Build stage: compile a static binary -----------------------------------
FROM golang:1.26-bookworm AS build
WORKDIR /src

# Cache module downloads first.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# CGO is off so the binary is static and runs on the slim runtime image. The app
# itself needs no cgo (only the race detector does, and that is a test concern).
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/alive ./cmd/alive

# --- Runtime stage: git + git-filter-repo (which pulls in python3) -----------
FROM debian:bookworm-slim

RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      git \
      git-filter-repo \
      ca-certificates \
 && rm -rf /var/lib/apt/lists/*

# A bind-mounted repository is owned by the host user, not by the container's
# user, so git would otherwise refuse to operate on it ("dubious ownership").
RUN git config --system --add safe.directory '*'

COPY --from=build /out/alive /usr/local/bin/alive
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /usr/local/bin/docker-entrypoint.sh

# Backups default here. This MUST be a bind mount to a host directory.
ENV ALIVE_STATE_DIR=/backups

# The repository to operate on is bind-mounted here; a bare `alive` runs the
# guided walkthrough against it.
WORKDIR /repo

ENTRYPOINT ["docker-entrypoint.sh"]
CMD []
