// Copyright (C) 2024, 2025 kvarenzn
// SPDX-License-Identifier: GPL-3.0-or-later

package controllers

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"github.com/google/gousb"

	"github.com/kvarenzn/ssm/core/common"
	"github.com/kvarenzn/ssm/core/config"
	"github.com/kvarenzn/ssm/core/log"
	"github.com/kvarenzn/ssm/game/stage"
)

var _REPORT_DESC_HEAD = []byte{
	0x05, 0x0d, // Usage Page (Digitalizers)
	0x09, 0x04, // Usage (Touch Screen)
	0xa1, 0x01, // Collection (Application)
	0x15, 0x00, //		Logical Mininum (0)
}

var _REPORT_DESC_BODY_PART1 = []byte{
	0x09, 0x22, //		Usage (Fingers)
	0xa1, 0x02, //		Collection (Logical)
	0x09, 0x51, //			Usage (Contact Identifier)
	0x75, 0x04, //			Report Size (4)
	0x95, 0x01, //			Report Count (1)
	0x25, 0x09, //			Logical Maximum (9)
	0x81, 0x02, //			Input (Data, Variable, Absolute)
	0x09, 0x42, //			Usage (Tip Switch)
	0x25, 0x01, //			Logical Maximum (1)
	0x75, 0x01, //			Report Size (1)
	0x81, 0x02, //			Input (Data, Variable, Absolute)
	0x09, 0x32, //			Usage (In Range)
	0x25, 0x01, //			Logical Maximum (1)
	0x81, 0x02, //			Input (Data, Variable, Absolute)
	0x75, 0x02, //			Report Size (2)
	0x81, 0x01, //			Input (Constant)
	0x05, 0x01, //			Usage Page (Generic Desktop Page)
	0x09, 0x30, //			Usage (X)
	0x26, //				Logical Maximum (Currently Unknown)
}

var _REPORT_DESC_BODY_PART2 = []byte{
	0x75, 0x10, //			Report Size (16)
	0x81, 0x02, //			Input (Data, Variable, Absolute)
	0x09, 0x31, //			Usage (Y)
	0x26, //				Logical Maximum (Currently Unknown)
}

var _REPORT_DESC_BODY_PART3 = []byte{
	0x81, 0x02, //			Input (Data, Variable, Absolute)
	0x05, 0x0d, //			Usage Page (Digitalizers)
	0xc0, //			End Collection
}

var _REPORT_DESC_TAIL = []byte{
	0xc0, //		End Collection
}

const ACCESSORY_ID uint16 = 114514 & 0xffff

func fingerEvent(id int, onScreen bool, x, y int) []byte {
	result := make([]byte, 5)
	result[0] = byte(id & 0b1111)
	if onScreen {
		result[0] |= 0b110000
	}

	binary.LittleEndian.PutUint16(result[1:3], uint16(x))
	binary.LittleEndian.PutUint16(result[3:5], uint16(y))

	return result
}

type PointerStatus struct {
	X        int
	Y        int
	OnScreen bool
}

func genHIDEventData(pointers []PointerStatus) []byte {
	result := bytes.NewBuffer([]byte{})
	for i, s := range pointers {
		result.Write(fingerEvent(i, s.OnScreen, s.X, s.Y))
	}
	return result.Bytes()
}

type HIDController struct {
	serial            string
	dc                *config.DeviceConfig
	device            *gousb.Device
	reportDescription []byte
	usbContext        *gousb.Context
}

func NewHIDController(dc *config.DeviceConfig) *HIDController {
	usbContext := gousb.NewContext()
	// defer usbContext.Close()

	devs, _ := usbContext.OpenDevices(func(desc *gousb.DeviceDesc) bool {
		if desc.Class != gousb.ClassPerInterface || desc.SubClass != gousb.ClassPerInterface {
			return false
		}
		return true
	})

	var device *gousb.Device = nil

	for _, dev := range devs {
		s, err := dev.SerialNumber()
		if err != nil || s != dc.Serial {
			dev.Close()
			continue
		}

		if device == nil {
			device = dev
		} else {
			dev.Close()
		}
	}

	uint16Buffer := make([]byte, 2)

	reportDescBody := bytes.NewBuffer(nil)
	reportDescBody.Write(_REPORT_DESC_BODY_PART1)
	binary.LittleEndian.PutUint16(uint16Buffer, uint16(dc.Width))
	reportDescBody.Write(uint16Buffer)
	reportDescBody.Write(_REPORT_DESC_BODY_PART2)
	binary.LittleEndian.PutUint16(uint16Buffer, uint16(dc.Height))
	reportDescBody.Write(uint16Buffer)
	reportDescBody.Write(_REPORT_DESC_BODY_PART3)

	reportDescription := bytes.NewBuffer(nil)
	reportDescription.Write(_REPORT_DESC_HEAD)
	for range 10 {
		reportDescription.Write(reportDescBody.Bytes())
	}
	reportDescription.Write(_REPORT_DESC_TAIL)

	return &HIDController{
		dc:                dc,
		device:            device,
		reportDescription: reportDescription.Bytes(),
		usbContext:        usbContext,
	}
}

func (c *HIDController) ensureDevice() error {
	if c == nil {
		return fmt.Errorf("HID controller is nil")
	}
	if c.device == nil {
		return fmt.Errorf("HID device not found or not accessible")
	}
	return nil
}

func (c *HIDController) registerHID() error {
	if err := c.ensureDevice(); err != nil {
		return err
	}
	_, err := c.device.Control(
		64, // ENDPOINT_OUT | REQUEST_TYPE_VENDOR
		54, // ACCESSORY_REGISTER_HID
		ACCESSORY_ID,
		uint16(len(c.reportDescription)),
		nil,
	)
	if err != nil {
		return fmt.Errorf("libusb register HID failed: %w", err)
	}
	return nil
}

func (c *HIDController) unregisterHID() error {
	if err := c.ensureDevice(); err != nil {
		return err
	}
	_, err := c.device.Control(
		64, // ENDPOINT_OUT | REQUEST_TYPE_VENDOR
		55, // ACCESSORY_UNREGISTER_ID
		ACCESSORY_ID,
		0,
		nil,
	)
	if err != nil {
		return fmt.Errorf("libusb unregister HID failed: %w", err)
	}
	return nil
}

func (c *HIDController) setHIDReportDescription() error {
	if err := c.ensureDevice(); err != nil {
		return err
	}
	_, err := c.device.Control(
		64, // ENDPOINT_OUT | REQUEST_TYPE_VENDOR
		56, // ACCESSORY_SET_HID_REPORT_DESC
		ACCESSORY_ID,
		0,
		c.reportDescription,
	)
	if err != nil {
		return fmt.Errorf("libusb set HID report descriptor failed: %w", err)
	}
	return nil
}

func (c *HIDController) sendHIDEvent(event []byte) error {
	if err := c.ensureDevice(); err != nil {
		return err
	}
	_, err := c.device.Control(
		64, // ENDPOINT_OUT | REQUEST_TYPE_VENDOR
		57, // ACCESSORY_SEND_HID_EVENT
		ACCESSORY_ID,
		0,
		event,
	)
	if err != nil {
		return fmt.Errorf("libusb send HID event failed: %w", err)
	}
	return nil
}

func (c *HIDController) Open() error {
	if err := c.registerHID(); err != nil {
		return err
	}
	if err := c.setHIDReportDescription(); err != nil {
		_ = c.unregisterHID()
		return err
	}
	return nil
}

func (c *HIDController) Send(data []byte) {
	if err := c.sendHIDEvent(data); err != nil {
		log.Warnf("failed to send HID event: %v", err)
	}
}

func (c *HIDController) Close() error {
	if c == nil {
		return nil
	}
	if err := c.unregisterHID(); err != nil {
		// Device may already be gone (e.g. cable hiccup); do not crash on best-effort cleanup.
		log.Warnf("failed to unregister HID: %v", err)
	}
	if c.device != nil {
		if err := c.device.Close(); err != nil {
			return err
		}
		c.device = nil
	}
	if c.usbContext != nil {
		err := c.usbContext.Close()
		c.usbContext = nil
		return err
	}
	return nil
}

func (c *HIDController) Preprocess(rawEvents common.RawVirtualEvents, turnRight bool, calc stage.JudgeLinePositionCalculator) []common.ViscousEventItem {
	width, height := float64(c.dc.Height), float64(c.dc.Width)
	x1, x2, yy := calc(width, height)
	dx := x2 - x1
	mapper := func(x, y float64) (int, int) {
		return crinterp(height-yy, height-yy+dx, y, 0, height),
			crinterp(x1, x2, x, 0, width)
	}
	if turnRight {
		mapper = func(x, y float64) (int, int) {
			ix, iy := crinterp(height-yy, height-yy+dx, y, 0, height),
				crinterp(x1, x2, x, 0, width)
			return c.dc.Width - ix, c.dc.Height - iy
		}
	}

	result := []common.ViscousEventItem{}
	currentFingers := make([]PointerStatus, 10)
	pointerSlotMap := map[int]int{}
	droppedPointers := map[int]bool{}
	overflowWarned := false

	allocSlot := func() int {
		for i, status := range currentFingers {
			if !status.OnScreen {
				return i
			}
		}
		return -1
	}

	for _, events := range rawEvents {
		for _, event := range events.Events {
			if event.PointerID < 0 {
				log.Fatalf("invalid pointer id: %d", event.PointerID)
			}
			x, y := mapper(event.X, event.Y)
			action, ok := common.NormalizeTouchAction(event.Action)
			if !ok {
				log.Fatalf("unknown touch action: %d\n", event.Action)
			}

			slot, mapped := pointerSlotMap[event.PointerID]
			if !mapped {
				slot = -1
			}

			switch action {
			case common.TouchDown:
				if slot == -1 {
					slot = allocSlot()
					if slot == -1 {
						droppedPointers[event.PointerID] = true
						if !overflowWarned {
							overflowWarned = true
							log.Warn("HID backend supports at most 10 simultaneous pointers; extra pointers will be dropped")
						}
						continue
					}
					pointerSlotMap[event.PointerID] = slot
				}
				status := currentFingers[slot]
				if status.OnScreen {
					log.Fatalf("pointer `%d` is already on screen", event.PointerID)
				}
				status.OnScreen = true
				status.X = x
				status.Y = y
				currentFingers[slot] = status
			case common.TouchMove:
				if droppedPointers[event.PointerID] {
					continue
				}
				if slot == -1 {
					continue
				}
				status := currentFingers[slot]
				if !status.OnScreen {
					continue
				}
				status.X = x
				status.Y = y
				currentFingers[slot] = status
			case common.TouchUp:
				if droppedPointers[event.PointerID] {
					delete(droppedPointers, event.PointerID)
					continue
				}
				if slot == -1 {
					continue
				}
				status := currentFingers[slot]
				if !status.OnScreen {
					continue
				}
				status.OnScreen = false
				status.X = x
				status.Y = y
				currentFingers[slot] = status
				delete(pointerSlotMap, event.PointerID)
			}
		}
		result = append(result, common.ViscousEventItem{
			Timestamp: events.Timestamp,
			Data:      genHIDEventData(currentFingers),
		})
	}
	return result
}

func FindHIDDevices() []string {
	result := []string{}

	ctx := gousb.NewContext()
	defer ctx.Close()

	devs, _ := ctx.OpenDevices(func(desc *gousb.DeviceDesc) bool {
		if desc.Class != gousb.ClassPerInterface || desc.SubClass != gousb.ClassPerInterface {
			return false
		}

		return true
	})

	for _, dev := range devs {
		serial, err := dev.SerialNumber()
		if err == nil && serial != "" {
			result = append(result, serial)
		}

		err = dev.Close()
		if err != nil {
			log.Warnf("failed to close HID device handle while scanning: %v", err)
		}
	}

	return result
}

type Controller interface {
	Send(data []byte)
}
