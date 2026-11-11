package main

import "testing"

func TestEnvironmentAccessRejectsFactoryBootPartition(t *testing.T) {
	parts := []gptPartition{
		{Number: 1, Start: 16384, End: 24575, Name: "uboot"},
		{Number: 2, Start: 24576, End: 32767, Name: "misc"},
		{Number: 3, Start: 32768, End: 163839, Name: "boot"},
	}
	if validateEnvironmentLayout(parts) == nil {
		t.Fatal("factory boot partition would be overwritten by environment writes")
	}
}

func TestEnvironmentAccessRequiresReservedRecoveryLayout(t *testing.T) {
	parts := []gptPartition{
		{Number: 1, Start: 65536, End: 524253, Name: "PHX_RECOVERY"},
		{Number: 2, Start: 589824, End: 900000, Name: "system-boot"},
	}
	if err := validateEnvironmentLayout(parts); err != nil {
		t.Fatal(err)
	}
	parts = append(parts, gptPartition{Number: 3, Start: 32768, End: 33023, Name: "unsafe"})
	if validateEnvironmentLayout(parts) == nil {
		t.Fatal("accepted a partition overlapping the environment")
	}
	if validateEnvironmentLayout(nil) == nil {
		t.Fatal("accepted missing partition table")
	}
}
