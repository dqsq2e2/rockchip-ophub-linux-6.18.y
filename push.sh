#!/bin/bash

set -ex

TARGET_IP="${1:-192.168.33.43}"
TARGET_USER="root"
TARGET_PASS="root"
REMOTE_DIR="/tmp"

SSH_CMD="sshpass -p ${TARGET_PASS} ssh ${TARGET_USER}@${TARGET_IP}"
RSYNC_CMD="sshpass -p ${TARGET_PASS} rsync -avz"

ls -alh release/*
${SSH_CMD} mkdir -p "${REMOTE_DIR}"
${RSYNC_CMD} release/recovery.img "${TARGET_USER}@${TARGET_IP}:${REMOTE_DIR}/"
${SSH_CMD} "dd if=/tmp/recovery.img of=/dev/mmcblk0p2 oflag=direct"
${SSH_CMD} "sync"
${SSH_CMD} "sync"
${SSH_CMD} "sync"

${SSH_CMD} tmux new-session -d -s reboot_session 'reboot -f'

