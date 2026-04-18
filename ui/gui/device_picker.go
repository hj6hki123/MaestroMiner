package gui

import (
	"fmt"
	"strings"

	"github.com/kvarenzn/ssm/device/adb"
)

func pickDevice(serial string) (*adb.Device, error) {
	client := adb.NewDefaultClient()
	devices, err := client.Devices()
	if err != nil {
		return nil, err
	}
	if len(devices) == 0 {
		return nil, fmt.Errorf("no adb devices")
	}

	serial = strings.TrimSpace(serial)
	if serial != "" {
		for _, d := range devices {
			if d.Serial() == serial && d.Authorized() {
				return d, nil
			}
		}
		return nil, fmt.Errorf("device %s not found or unauthorized", serial)
	}

	if d := adb.FirstAuthorizedDevice(devices); d != nil {
		return d, nil
	}
	return nil, fmt.Errorf("no authorized adb device")
}
