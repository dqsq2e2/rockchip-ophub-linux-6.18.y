#!/bin/bash

set -ex

TARGET_IP="${1:-192.168.33.39}"

md5sum release/*
sshpass -p root rsync -avz -P release/recovery.conf       root@${TARGET_IP}:/recovery/
sshpass -p root rsync -avz -P release/kernel-uImage.lzma  root@${TARGET_IP}:/recovery/
sshpass -p root rsync -avz -P release/rk3588-aigo-s60.dtb root@${TARGET_IP}:/recovery/

#sshpass -p root ssh root@${TARGET_IP} /bin/hack



