// Copyright (C) 2024, 2025, 2026 kvarenzn
// SPDX-License-Identifier: GPL-3.0-or-later

package scores

import (
	"cmp"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/kvarenzn/ssm/log"
	"github.com/kvarenzn/ssm/utils"
)

const (
	ChannelBackgroundMusic     = "01"
	ChannelTimeSignature       = "02"
	ChannelBPMChange           = "03"
	ChannelBackgroundAnimation = "04"
	ChannelPoorBitmapChange    = "06"
	ChannelLayer               = "07"
	ChannelExtendedBPM         = "08"
	ChannelStop                = "09"

	ChannelNoteTrack1 = "16"
	ChannelNoteTrack2 = "11"
	ChannelNoteTrack3 = "12"
	ChannelNoteTrack4 = "13"
	ChannelNoteTrack5 = "14"
	ChannelNoteTrack6 = "15"
	ChannelNoteTrack7 = "18"

	ChannelSpecialTrack1 = "36"
	ChannelSpecialTrack2 = "31"
	ChannelSpecialTrack3 = "32"
	ChannelSpecialTrack4 = "33"
	ChannelSpecialTrack5 = "34"
	ChannelSpecialTrack6 = "35"
	ChannelSpecialTrack7 = "38"

	ChannelHoldTrack1 = "56"
	ChannelHoldTrack2 = "51"
	ChannelHoldTrack3 = "52"
	ChannelHoldTrack4 = "53"
	ChannelHoldTrack5 = "54"
	ChannelHoldTrack6 = "55"
	ChannelHoldTrack7 = "58"
)

var TRACKS_MAP = map[string]int{
	ChannelNoteTrack1:    0,
	ChannelNoteTrack2:    1,
	ChannelNoteTrack3:    2,
	ChannelNoteTrack4:    3,
	ChannelNoteTrack5:    4,
	ChannelNoteTrack6:    5,
	ChannelNoteTrack7:    6,
	ChannelSpecialTrack1: 0,
	ChannelSpecialTrack2: 1,
	ChannelSpecialTrack3: 2,
	ChannelSpecialTrack4: 3,
	ChannelSpecialTrack5: 4,
	ChannelSpecialTrack6: 5,
	ChannelSpecialTrack7: 6,
	ChannelHoldTrack1:    0,
	ChannelHoldTrack2:    1,
	ChannelHoldTrack3:    2,
	ChannelHoldTrack4:    3,
	ChannelHoldTrack5:    4,
	ChannelHoldTrack6:    5,
	ChannelHoldTrack7:    6,
}

var simpleTracks = []string{
	ChannelNoteTrack1,
	ChannelNoteTrack2,
	ChannelNoteTrack3,
	ChannelNoteTrack4,
	ChannelNoteTrack5,
	ChannelNoteTrack6,
	ChannelNoteTrack7,
}

type BasicNoteType byte

const (
	NoteTypeNote BasicNoteType = iota
	NoteTypeFlick
	NoteTypeSlideA
	NoteTypeSlideB
	NoteTypeSlideEndA
	NoteTypeSlideEndFlickA
	NoteTypeSlideEndB
	NoteTypeSlideEndFlickB
	NoteTypeFlickLeft
	NoteTypeFlickRight
	NoteTypeAddLongDirFlick
	NoteTypeAddSlideDirFlick
	NoteTypeContBezierFrontA
	NoteTypeContBezierFrontB
	NoteTypeContBezierBackA
	NoteTypeContBezierBackB
	NoteTypeLongEndDirFlickLeft
	NoteTypeLongEndDirFlickRight
	NoteTypeSlideEndDirFlickLeftA
	NoteTypeSlideEndDirFlickLeftB
	NoteTypeSlideEndDirFlickRightA
	NoteTypeSlideEndDirFlickRightB
)

var wavNoteTypeMap map[string]BasicNoteType = map[string]BasicNoteType{
	"":                            NoteTypeNote,
	"bd.wav":                      NoteTypeNote,
	"flick.wav":                   NoteTypeFlick,
	"無音_flick.wav":                NoteTypeFlick,
	"skill.wav":                   NoteTypeNote,
	"slide_a.wav":                 NoteTypeSlideA,
	"slide_a_skill.wav":           NoteTypeSlideA,
	"slide_a_fever.wav":           NoteTypeSlideA,
	"skill_slide_a.wav":           NoteTypeSlideA,
	"slide_end_a.wav":             NoteTypeSlideEndA,
	"slide_end_flick_a.wav":       NoteTypeSlideEndFlickA,
	"slide_end_dir_flick_l_a.wav": NoteTypeSlideEndDirFlickLeftA,
	"slide_end_dir_flick_r_a.wav": NoteTypeSlideEndDirFlickRightA,
	"slide_b.wav":                 NoteTypeSlideB,
	"slide_b_skill.wav":           NoteTypeSlideB,
	"slide_b_fever.wav":           NoteTypeSlideB,
	"skill_slide_b.wav":           NoteTypeSlideB,
	"slide_end_b.wav":             NoteTypeSlideEndB,
	"slide_end_flick_b.wav":       NoteTypeSlideEndFlickB,
	"slide_end_dir_flick_l_b.wav": NoteTypeSlideEndDirFlickLeftB,
	"slide_end_dir_flick_r_b.wav": NoteTypeSlideEndDirFlickRightB,
	"fever_note.wav":              NoteTypeNote,
	"fever_note_flick.wav":        NoteTypeFlick,
	"fever_note_slide_a.wav":      NoteTypeSlideA,
	"fever_note_slide_end_a.wav":  NoteTypeSlideEndA,
	"fever_note_slide_b.wav":      NoteTypeSlideB,
	"fever_note_slide_end_b.wav":  NoteTypeSlideEndB,
	"fever_slide_a.wav":           NoteTypeSlideA,
	"fever_slide_end_a.wav":       NoteTypeSlideEndA,
	"fever_slide_b.wav":           NoteTypeSlideB,
	"fever_slide_end_b.wav":       NoteTypeSlideEndB,
	"directional_fl_l.wav":        NoteTypeFlickLeft,
	"directional_fl_r.wav":        NoteTypeFlickRight,
	"add_long_dir_flick.wav":      NoteTypeAddLongDirFlick,
	"add_slide_dir_flick.wav":     NoteTypeAddSlideDirFlick,
	"cont_bezier_front_a.wav":     NoteTypeContBezierFrontA,
	"cont_bezier_front_b.wav":     NoteTypeContBezierFrontB,
	"cont_bezier_back_a.wav":      NoteTypeContBezierBackA,
	"cont_bezier_back_b.wav":      NoteTypeContBezierBackB,
	"long_end_dir_flick_l.wav":    NoteTypeLongEndDirFlickLeft,
	"long_end_dir_flick_r.wav":    NoteTypeLongEndDirFlickRight,
}

type NoteType interface {
	String() string
	NoteType() BasicNoteType
	Mark() string
	Offset() float64
}

func (n BasicNoteType) String() string {
	switch n {
	case NoteTypeNote:
		return "Tap"
	case NoteTypeFlick:
		return "Flick"
	case NoteTypeSlideA:
		return "Slide A"
	case NoteTypeSlideEndA:
		return "Slide End A"
	case NoteTypeSlideEndFlickA:
		return "Slide End Flick A"
	case NoteTypeSlideB:
		return "Slide B"
	case NoteTypeSlideEndB:
		return "Slide End B"
	case NoteTypeSlideEndFlickB:
		return "Slide End Flick B"
	default:
		return "Unknown"
	}
}

func (n BasicNoteType) NoteType() BasicNoteType {
	return n
}

func (n BasicNoteType) Mark() string {
	switch n {
	case NoteTypeSlideA, NoteTypeSlideEndA, NoteTypeSlideEndFlickA:
		return "a"
	case NoteTypeSlideB, NoteTypeSlideEndB, NoteTypeSlideEndFlickB:
		return "b"
	default:
		return ""
	}
}

func (n BasicNoteType) Offset() float64 {
	return 0.0
}

type SpecialSlideNoteType struct {
	mark   string
	offset float64
}

func NewSpecialSlideNoteType(name string) (SpecialSlideNoteType, error) {
	re := regexp.MustCompile(`slide_(.)_(L|R)S(\d\d)\.wav`)
	subs := re.FindStringSubmatch(name)
	if len(subs) < 3 {
		return SpecialSlideNoteType{}, fmt.Errorf("not a special slide note type")
	}
	mark := subs[1]
	direction := subs[2]
	rawOffset := subs[3]

	offInt, err := strconv.ParseInt(rawOffset, 10, 64)
	if err != nil {
		log.Fatalf("parse rawOffset(%s) failed: %s", rawOffset, err)
	}
	offset := float64(offInt) / 100.0

	if direction == "L" {
		offset = -offset
	}

	return SpecialSlideNoteType{
		mark:   mark,
		offset: offset,
	}, nil
}

func (n SpecialSlideNoteType) String() string {
	return fmt.Sprintf("Slide Special %s", n.mark)
}

func (n SpecialSlideNoteType) NoteType() BasicNoteType {
	switch n.mark {
	case "a":
		return NoteTypeSlideA
	case "b":
		return NoteTypeSlideB
	default:
		return NoteTypeNote
	}
}

func (n SpecialSlideNoteType) Mark() string {
	return n.mark
}

func (n SpecialSlideNoteType) Offset() float64 {
	return n.offset
}

func noteTypeOf(wav string) (NoteType, error) {
	basicType, ok := wavNoteTypeMap[wav]
	if ok {
		return basicType, nil
	}

	note, err := NewSpecialSlideNoteType(wav)
	if err == nil {
		return note, nil
	}

	return NoteTypeNote, fmt.Errorf("unknown wav: %s", wav)
}

func ParseBMS(chartText string) Chart {
	const barLength = 4
	const FIELD_BEGIN = "*----------------------"
	const HEADER_BEGIN = "*---------------------- HEADER FIELD"
	const EXPANSION_BEGIN = "*---------------------- EXPANSION FIELD"
	const MAIN_DATA_BEGIN = "*---------------------- MAIN DATA FIELD"
	headerTag := regexp.MustCompile(`^#([0-9A-Z]+) (.*)$`)
	extendedHeaderTag := regexp.MustCompile(`^#([0-9A-Z]+) (.*)$`)
	newline := regexp.MustCompile(`\r?\n`)

	rawBpmEvents := map[float64]float64{}

	wavs := map[string]string{}
	extendedBPM := map[string]float64{}

	lines := newline.Split(chartText, -1)

	// drop anything before header
	for !strings.Contains(lines[0], HEADER_BEGIN) {
		lines = lines[1:]
	}

	lines = lines[1:]

	// HEADER FIELD
	for ; !strings.Contains(lines[0], FIELD_BEGIN); lines = lines[1:] {
		subs := headerTag.FindStringSubmatch(lines[0])
		if len(subs) == 0 {
			continue
		}

		key := subs[1]
		value := subs[2]

		switch key {
		case "PLAYER":
		case "GENRE":
		case "TITLE":
		case "ARTIST":
		case "PLAYLEVEL":
		case "STAGEFILE":
		case "RANK":
		case "LNTYPE":
		case "BPM":
			bpm, err := strconv.ParseFloat(value, 64)
			if err != nil {
				log.Fatalf("failed to parse value of #BPM(%s), err: %+v", value, err)
			}
			rawBpmEvents[0] = bpm // tick = 0时，bpm为初始bpm
		case "BGM":
		default:
			if strings.HasPrefix(key, "WAV") {
				point := key[3:]
				wavs[point] = value
			} else if strings.HasPrefix(key, "BPM") {
				point := key[3:]
				bpm, err := strconv.ParseFloat(value, 64)
				if err != nil {
					log.Fatalf("failed to parse value of #BPM%s(%s), err: %+v", point, value, err)
				}
				extendedBPM[point] = bpm
			} else {
				log.Warnf("unknown command in HEADER FIELD: %s: %s", key, value)
			}
		}
	}

	// EXPANSION FIELD
	if strings.Contains(lines[0], EXPANSION_BEGIN) {
		for ; !strings.Contains(lines[0], FIELD_BEGIN); lines = lines[1:] {
			subs := extendedHeaderTag.FindStringSubmatch(lines[0])
			if len(subs) == 0 {
				continue
			}

			key := subs[1]
			value := subs[2]
			switch key {
			case "BGM":
			default:
				log.Warnf("unknown command in EXPANSION FIELD: %s: %s", key, value)
			}
		}
	}

	// MAIN DATA FILED
	lines = lines[1:]

	type rawNoteEvent struct {
		channel string
		wav     string
	}
	rawNoteEvents := map[float64][]*rawNoteEvent{}

	// 第一步：统计所有BPM事件，同时收集所有音符的wav数据，但不进行任何处理
	for lineNumber := 0; len(lines) != 0; lineNumber++ {
		line := lines[0]
		lines = lines[1:]

		events, _, err := parseDataLine(line)
		if err == errInvalidDataLineFormat {
			continue
		} else if err != nil {
			log.Fatalf("Failed to parse line #%d %s: %s", lineNumber, line, err)
		}

		for _, ev := range events {
			tick := ev.Tick()
			channel := ev.Common.Channel

			switch channel {
			case ChannelBackgroundMusic:
				// do nothing
			case ChannelBPMChange:
				value, err := strconv.ParseInt(ev.Type, 16, 64)
				if err != nil {
					log.Fatalf("Failed to parse value of line #%d bpm(%s), err: %+v", lineNumber, ev.Type, err)
				}

				rawBpmEvents[tick] = float64(value)
			case ChannelExtendedBPM:
				rawBpmEvents[tick] = extendedBPM[ev.Type]
			default:
				if _, ok := rawNoteEvents[tick]; !ok {
					rawNoteEvents[tick] = nil
				}

				wav, ok := wavs[ev.Type]
				if !ok {
					rawNoteEvents[tick] = append(rawNoteEvents[tick], &rawNoteEvent{
						channel: channel,
					})
					continue
				}

				rawNoteEvents[tick] = append(rawNoteEvents[tick], &rawNoteEvent{
					channel: channel,
					wav:     wav,
				})
			}
		}
	}

	// 第二步：统计所有bpm事件，建立tick -> seconds转换表
	bpmTicks := utils.SortedKeysOf(rawBpmEvents)
	type bpmEvent struct {
		tick    float64
		bpm     float64
		seconds float64
	}
	bpmTable := []*bpmEvent{}

	lastTick := 0.0
	secStart := 0.0
	lastBpm := rawBpmEvents[0]
	for _, tick := range bpmTicks {
		secStart += barLength * 60 / lastBpm * (tick - lastTick)
		bpm := rawBpmEvents[tick]
		bpmTable = append(bpmTable, &bpmEvent{
			tick:    tick,
			bpm:     bpm,
			seconds: secStart,
		})

		lastTick = tick
		lastBpm = bpm
	}

	secondsOf := func(tick float64) float64 {
		idx, found := slices.BinarySearchFunc(bpmTable, tick, func(e *bpmEvent, t float64) int {
			return cmp.Compare(e.tick, t)
		})
		if !found {
			idx--
		}
		bpmInfo := bpmTable[idx]
		return bpmInfo.seconds + barLength*60/bpmInfo.bpm*(tick-bpmInfo.tick)
	}

	// 第三步：将rawNoteEvents初步转换为parsedNoteEvents
	// 主要是为了将wav解析到音符类型，以及将tick转换为seconds
	// 在这一步后，时间单位将统一为秒
	type parsedNoteEvent struct {
		channel  string
		noteType NoteType
		aux      int
	}
	parsedNoteEvents := map[float64][]*parsedNoteEvent{}
	directionalFlickSeconds := map[float64][]byte{}
	noteTicks := utils.SortedKeysOf(rawNoteEvents)
	noteSeconds := []float64{}
	for _, tick := range noteTicks {
		evs := rawNoteEvents[tick]
		seconds := secondsOf(tick)
		noteSeconds = append(noteSeconds, seconds)
		parsedEvents := []*parsedNoteEvent{}
		for _, ev := range evs {
			noteType, err := noteTypeOf(ev.wav)
			if err != nil {
				log.Warnf("Unknown wav at channel %s, time: %s: %+v", ev.channel, utils.FormatSeconds(seconds), err)
				noteType = NoteTypeNote
			}
			parsedEvents = append(parsedEvents, &parsedNoteEvent{
				channel:  ev.channel,
				noteType: noteType,
			})

			// 收集带方向的滑动音符信息，以便在下一步中将其合并
			if noteType == NoteTypeFlickLeft || noteType == NoteTypeFlickRight {
				if _, ok := directionalFlickSeconds[seconds]; !ok {
					directionalFlickSeconds[seconds] = make([]byte, 7)
				}
				v := directionalFlickSeconds[seconds]
				if noteType == NoteTypeFlickLeft {
					v[TRACKS_MAP[ev.channel]] = '<'
				} else {
					v[TRACKS_MAP[ev.channel]] = '>'
				}
			}
		}
		parsedNoteEvents[seconds] = parsedEvents
	}

	// 第四步：合并相邻的同一方向的滑动按键为一个，比如>>>可以视作一个滑动长度为3的滑键
	for seconds, v := range directionalFlickSeconds {
		start := -1
		length := 0
		newParsedEvents := []*parsedNoteEvent{}
		for i, c := range append(v, 0) {
			if c == '>' {
				if start == -1 {
					start = i
					length = 1
				} else {
					length++
				}
			} else {
				if start != -1 {
					newParsedEvents = append(newParsedEvents, &parsedNoteEvent{
						channel:  simpleTracks[start],
						noteType: NoteTypeFlickRight,
						aux:      length,
					})
					start = -1
					length = 0
				}
			}
		}

		rev := append([]byte{0}, v...)
		for i := 6; i >= -1; i-- {
			c := rev[i+1]
			if c == '<' {
				if start == -1 {
					start = i
					length = 1
				} else {
					length++
				}
			} else {
				if start != -1 {
					newParsedEvents = append(newParsedEvents, &parsedNoteEvent{
						channel:  simpleTracks[start],
						noteType: NoteTypeFlickLeft,
						aux:      length,
					})
					start = -1
					length = 0
				}
			}
		}

		for _, ev := range parsedNoteEvents[seconds] {
			if ev.noteType != NoteTypeFlickLeft && ev.noteType != NoteTypeFlickRight {
				newParsedEvents = append(newParsedEvents, ev)
			}
		}

		parsedNoteEvents[seconds] = newParsedEvents
	}

	// 第五步：将每个音符事件转换为手法规划器支持的结构（star）
	finalEvents := []*star{}
	holdTracks := [7]float64{math.NaN(), math.NaN(), math.NaN(), math.NaN(), math.NaN(), math.NaN(), math.NaN()}
	var slideA, slideB *star

	for _, sec := range noteSeconds {
		events := parsedNoteEvents[sec]
		slices.SortFunc(events, func(a, b *parsedNoteEvent) int {
			return -cmp.Compare(a.noteType.NoteType(), b.noteType.NoteType())
		})

		for _, ev := range events {
			switch ev.channel {
			case ChannelNoteTrack1, ChannelNoteTrack2, ChannelNoteTrack3, ChannelNoteTrack4, ChannelNoteTrack5, ChannelNoteTrack6, ChannelNoteTrack7:
				trackID := float64(TRACKS_MAP[ev.channel]) / 6
				switch ev.noteType {
				// normal note
				case NoteTypeNote:
					finalEvents = append(
						finalEvents,
						newStar(sec, trackID, 1.0/6).
							markAsTap())
				// flick note
				case NoteTypeFlick:
					finalEvents = append(
						finalEvents,
						newStar(sec, trackID, 1.0/6).
							markAsTap().
							flickToIfOk(true, 90))
				case NoteTypeFlickLeft:
					finalEvents = append(
						finalEvents,
						newStar(sec, trackID, 1.0/6).
							markAsTap().
							flickToIfOk(true, 180))
				case NoteTypeFlickRight:
					finalEvents = append(
						finalEvents,
						newStar(sec, trackID, 1.0/6).
							markAsTap().
							flickToIfOk(true, 0))
				// slide a
				case NoteTypeSlideA:
					if slideA == nil {
						slideA = newStar(sec, trackID, 1.0/6).
							markAsTap().
							markAsHead()
					} else {
						slideA = newStar(sec, trackID, 1.0/6).
							chainsAfter(slideA)
					}
				case NoteTypeSlideEndA:
					finalEvents = append(
						finalEvents,
						newStar(sec, trackID, 1.0/6).
							chainsAfter(slideA).
							markAsEnd())
					slideA = nil
				case NoteTypeSlideEndFlickA:
					finalEvents = append(
						finalEvents,
						newStar(sec, trackID, 1.0/6).
							chainsAfter(slideA).
							flickToIfOk(true, 90).
							markAsEnd())
					slideA = nil
				case NoteTypeSlideEndDirFlickLeftA:
					finalEvents = append(
						finalEvents,
						newStar(sec, trackID, 1.0/6).
							chainsAfter(slideA).
							flickToIfOk(true, 180).
							markAsEnd())
					slideA = nil
				case NoteTypeSlideEndDirFlickRightA:
					finalEvents = append(
						finalEvents,
						newStar(sec, trackID, 1.0/6).
							chainsAfter(slideA).
							flickToIfOk(true, 0).
							markAsEnd())
					slideA = nil
				// slide b
				case NoteTypeSlideB:
					if slideB == nil {
						slideB = newStar(sec, trackID, 1.0/6).
							markAsTap().
							markAsHead()
					} else {
						slideB = newStar(sec, trackID, 1.0/6).
							chainsAfter(slideB)
					}
				case NoteTypeSlideEndB:
					finalEvents = append(
						finalEvents,
						newStar(sec, trackID, 1.0/6).
							chainsAfter(slideB).
							markAsEnd())
					slideB = nil
				case NoteTypeSlideEndFlickB:
					finalEvents = append(
						finalEvents,
						newStar(sec, trackID, 1.0/6).
							chainsAfter(slideB).
							flickToIfOk(true, 90).
							markAsEnd())
					slideB = nil
				case NoteTypeSlideEndDirFlickLeftB:
					finalEvents = append(
						finalEvents,
						newStar(sec, trackID, 1.0/6).
							chainsAfter(slideB).
							flickToIfOk(true, 180).
							markAsEnd())
					slideB = nil
				case NoteTypeSlideEndDirFlickRightB:
					finalEvents = append(
						finalEvents,
						newStar(sec, trackID, 1.0/6).
							chainsAfter(slideB).
							flickToIfOk(true, 0).
							markAsEnd())
					slideB = nil

				case NoteTypeAddLongDirFlick:
					// do nothing
				case NoteTypeAddSlideDirFlick:
					// do nothing

				case NoteTypeContBezierFrontA:
				case NoteTypeContBezierFrontB:
				case NoteTypeContBezierBackA:
				case NoteTypeContBezierBackB:

				// unknown
				default:
					log.Warnf("unknown note type %s on note track %d\n", ev.noteType, trackID)
				}
			case ChannelHoldTrack1, ChannelHoldTrack2, ChannelHoldTrack3, ChannelHoldTrack4, ChannelHoldTrack5, ChannelHoldTrack6, ChannelHoldTrack7:
				trackID := TRACKS_MAP[ev.channel]
				trackX := float64(trackID) / 6
				switch ev.noteType {
				case NoteTypeNote:
					startTick := holdTracks[trackID]
					if math.IsNaN(startTick) {
						holdTracks[trackID] = sec
					} else {
						finalEvents = append(
							finalEvents,
							newStar(sec, trackX, 1.0/6).
								chainsAfter(
									newStar(startTick, trackX, 1.0/6).
										markAsTap().
										markAsHead(),
								).
								markAsEnd())
						holdTracks[trackID] = math.NaN()
					}
				case NoteTypeFlick:
					startTick := holdTracks[trackID]
					if math.IsNaN(startTick) {
						log.Fatalf("no hold start data on track %d", trackID)
					}
					finalEvents = append(
						finalEvents,
						newStar(sec, trackX, 1.0/6).
							chainsAfter(
								newStar(startTick, trackX, 1.0/6).
									markAsTap().
									markAsHead(),
							).
							flickToIfOk(true, 90).
							markAsEnd())
					holdTracks[trackID] = math.NaN()
				case NoteTypeLongEndDirFlickLeft:
					startTick := holdTracks[trackID]
					if math.IsNaN(startTick) {
						log.Fatalf("no hold start data on track %d", trackID)
					}
					finalEvents = append(
						finalEvents,
						newStar(sec, trackX, 1.0/6).
							chainsAfter(
								newStar(startTick, trackX, 1.0/6).
									markAsTap().
									markAsHead(),
							).
							flickToIfOk(true, 180).
							markAsEnd())
					holdTracks[trackID] = math.NaN()
				case NoteTypeLongEndDirFlickRight:
					startTick := holdTracks[trackID]
					if math.IsNaN(startTick) {
						log.Fatalf("no hold start data on track %d", trackID)
					}
					finalEvents = append(
						finalEvents,
						newStar(sec, trackX, 1.0/6).
							chainsAfter(
								newStar(startTick, trackX, 1.0/6).
									markAsTap().
									markAsHead(),
							).
							flickToIfOk(true, 0).
							markAsEnd())
					holdTracks[trackID] = math.NaN()
				default:
					log.Warnf("unknown note type %s at track %d, time %f s\n", ev.noteType, trackID, sec)
				}
			case ChannelSpecialTrack1, ChannelSpecialTrack2, ChannelSpecialTrack3, ChannelSpecialTrack4, ChannelSpecialTrack5, ChannelSpecialTrack6, ChannelSpecialTrack7:
				trackID := float64(TRACKS_MAP[ev.channel]) / 6
				switch nt := ev.noteType.(type) {
				case SpecialSlideNoteType:
					switch nt.mark {
					case "a":
						if slideA == nil {
							slideA = newStar(sec, trackID+nt.offset/6, 1.0/6).
								markAsTap().
								markAsHead()
						} else {
							slideA = newStar(sec, trackID+nt.offset/6, 1.0/6).
								chainsAfter(slideA)
						}
					case "b":
						if slideB == nil {
							slideB = newStar(sec, trackID+nt.offset/6, 1.0/6).
								markAsTap().
								markAsHead()
						} else {
							slideB = newStar(sec, trackID+nt.offset/6, 1.0/6).
								chainsAfter(slideB)
						}
					default:
						log.Warnf("unknown mark %s\n", nt.mark)
					}
				case BasicNoteType:
					switch nt {
					case NoteTypeSlideA:
						if slideA == nil {
							slideA = newStar(sec, trackID, 1.0/6).
								markAsTap().
								markAsHead()
						} else {
							slideA = newStar(sec, trackID, 1.0/6).
								chainsAfter(slideA)
						}
					case NoteTypeSlideB:
						if slideB == nil {
							slideB = newStar(sec, trackID, 1.0/6).
								markAsTap().
								markAsHead()
						} else {
							slideB = newStar(sec, trackID, 1.0/6).
								chainsAfter(slideB)
						}
					default:
						log.Warnf("%s should not appear at channel %d, time %f s", ev.noteType, ev.channel, sec)
					}
				default:
					log.Warnf("%s should not appear at channel %d, time %f s", ev.noteType, ev.channel, sec)
				}
			}
		}
	}

	if slideA != nil {
		finalEvents = append(finalEvents, slideA.markAsEnd())
		slideA = nil
	}

	if slideB != nil {
		finalEvents = append(finalEvents, slideB.markAsEnd())
		slideB = nil
	}

	return finalEvents
}