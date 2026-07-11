#!/bin/sh
# Entry point for the alive.name container.
#
# It enforces the two bind mounts the tool needs so that nothing important is
# lost when the container is removed:
#
#   /repo      the git repository to operate on (required by most commands)
#   /backups   where verified backups are written (required by anything that
#              creates a backup, otherwise the backup would be ephemeral)
#
# Commands that create backups are refused unless /backups is a real bind mount.
set -eu

REPO_DIR=/repo
BACKUP_DIR=/backups

# is_mounted reports whether a path is a mount point (a bind mount) inside the
# container, by looking it up in the kernel's mount table.
is_mounted() {
	grep -q " $1 " /proc/self/mountinfo 2>/dev/null
}

command="${1:-}"

case "$command" in
	cleanup | help | --help | -h | version | --version | completion)
		repo_required=0
		backups_required=0
		;;
	trace | mend)
		repo_required=1
		backups_required=0
		;;
	reclaim | backup)
		repo_required=1
		backups_required=1
		;;
	guide | "")
		# A bare `alive` runs the guided walkthrough, which can create a backup.
		repo_required=1
		backups_required=1
		;;
	*)
		# Anything else (for example a global flag before a subcommand): be safe
		# and require both mounts.
		repo_required=1
		backups_required=1
		;;
esac

usage_hint() {
	echo "       Example:" >&2
	echo "         docker run --rm -it -v \"/path/to/working/repo:/repo\" -v \"/path/to/backups:/backups\" alive $*" >&2
}

if [ "$repo_required" -eq 1 ] && ! is_mounted "$REPO_DIR"; then
	echo "alive: /repo is not mounted. Mount your git repository read-write." >&2
	usage_hint "$@"
	exit 1
fi

if [ "$backups_required" -eq 1 ] && ! is_mounted "$BACKUP_DIR"; then
	echo "alive: /backups is not mounted." >&2
	echo "       This command can create a backup, and without a host mount that backup" >&2
	echo "       would be EPHEMERAL: lost the moment this container is removed. Refusing" >&2
	echo "       to continue. Mount a host directory at /backups." >&2
	usage_hint "$@"
	exit 1
fi

exec alive "$@"
