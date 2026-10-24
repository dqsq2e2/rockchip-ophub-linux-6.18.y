#!/bin/bash

set -ex

md5sum release/*
sshpass -p admin rsync -avz -P release/kernel-uImage.lzma root@192.168.33.39:/recovery/
sshpass -p admin rsync -avz -P release/rk3588-aigo-s60.dtb root@192.168.33.39:/recovery/

