# SPDX-License-Identifier: GPL-2.0-only
ccflags-y += -I$(src)/include
obj-m += st_accel.o st_accel_i2c.o st_sensors.o st_sensors_i2c.o
st_accel-y := st_accel_core.o
st_accel-$(CONFIG_IIO_BUFFER) += st_accel_buffer.o
st_sensors-y := st_sensors_core.o
st_sensors-$(CONFIG_IIO_BUFFER) += st_sensors_buffer.o
st_sensors-$(CONFIG_IIO_TRIGGER) += st_sensors_trigger.o
