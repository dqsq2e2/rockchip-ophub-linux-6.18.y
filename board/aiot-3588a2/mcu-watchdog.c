// SPDX-License-Identifier: GPL-2.0-only
#include <fcntl.h>
#include <linux/i2c-dev.h>
#include <stdio.h>
#include <string.h>
#include <sys/ioctl.h>
#include <unistd.h>

static int is_supported_board(void)
{
	static const char bus_node[] = "/sys/bus/i2c/devices/i2c-6/of_node";
	char buf[512];
	const char *name;
	int fd;
	ssize_t len;

	fd = open("/proc/device-tree/compatible", O_RDONLY | O_CLOEXEC);
	if (fd < 0)
		return 0;
	len = read(fd, buf, sizeof(buf) - 1);
	close(fd);
	if (len < 1)
		return 0;
	buf[len] = '\0';
	if (strcmp(buf, "smdt,aiot-3588a2"))
		return 0;
	len = readlink(bus_node, buf, sizeof(buf) - 1);
	if (len < 1)
		return 0;
	buf[len] = '\0';
	name = strrchr(buf, '/');
	return name && !strcmp(name + 1, "i2c@fec80000") &&
	       access("/sys/bus/i2c/devices/6-0062/driver", F_OK) != 0;
}

static int send_pair(int fd, unsigned char reg, unsigned char value)
{
	unsigned char data[] = { reg, value };

	return write(fd, data, sizeof(data)) == sizeof(data) ? 0 : -1;
}

int main(void)
{
	unsigned char ack_reg = 0xd1;
	unsigned char ack;
	int fd;

	if (!is_supported_board()) {
		fputs("Unsupported board or MCU bus already claimed\n", stderr);
		return 1;
	}
	fd = open("/dev/i2c-6", O_RDWR | O_CLOEXEC);
	if (fd < 0 || ioctl(fd, I2C_SLAVE, 0x62) < 0)
		goto error;

	/* Acknowledge Linux startup before feeding the board's external watchdog. */
	if (send_pair(fd, 0x51, 0x33) < 0 ||
	    write(fd, &ack_reg, 1) != 1 || read(fd, &ack, 1) != 1)
		goto error;
	if (ack != 0x33) {
		fprintf(stderr, "MCU did not acknowledge startup: %#04x\n", ack);
		return 1;
	}
	puts("MCU startup acknowledged");
	fflush(stdout);
	for (;;) {
		if (send_pair(fd, 0x33, 0xab) < 0)
			goto error;
		sleep(9);
	}
error:
	perror("MCU I2C");
	return 1;
}
