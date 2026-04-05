// Copyright (C) 2024, 2025 kvarenzn
// SPDX-License-Identifier: GPL-3.0-or-later

package scores

import (
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
	NoteTypeSlideMiddle
	NoteTypeSlideEndFlickLeftA
	NoteTypeSlideEndFlickRightA
	NoteTypeSlideEndFlickLeftB
	NoteTypeSlideEndFlickRightB
)

func isSlideEnd(n BasicNoteType) bool {
	switch n {
	case NoteTypeSlideEndA, NoteTypeSlideEndFlickA,
		NoteTypeSlideEndFlickLeftA, NoteTypeSlideEndFlickRightA,
		NoteTypeSlideEndB, NoteTypeSlideEndFlickB,
		NoteTypeSlideEndFlickLeftB, NoteTypeSlideEndFlickRightB:
		return true
	}
	return false
}

var wavNoteTypeMap = map[string]BasicNoteType{
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
	"slide_b.wav":                 NoteTypeSlideB,
	"slide_b_skill.wav":           NoteTypeSlideB,
	"slide_b_fever.wav":           NoteTypeSlideB,
	"skill_slide_b.wav":           NoteTypeSlideB,
	"slide_end_b.wav":             NoteTypeSlideEndB,
	"slide_end_flick_b.wav":       NoteTypeSlideEndFlickB,
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
	"cont_bezier_back_a.wav":      NoteTypeSlideA,
	"cont_bezier_back_b.wav":      NoteTypeSlideB,
	"add_slide_dir_flick.wav":     NoteTypeSlideMiddle,
	"slide_end_dir_flick_l_a.wav": NoteTypeSlideEndFlickLeftA,
	"slide_end_dir_flick_r_a.wav": NoteTypeSlideEndFlickRightA,
	"slide_end_dir_flick_l_b.wav": NoteTypeSlideEndFlickLeftB,
	"slide_end_dir_flick_r_b.wav": NoteTypeSlideEndFlickRightB,
	"add_long_dir_flick.wav":      NoteTypeFlick,
	"long_end_dir_flick_l.wav":    NoteTypeFlickLeft,
	"long_end_dir_flick_r.wav":    NoteTypeFlickRight,
	"cont_bezier_front_a.wav":     NoteTypeSlideA,
	"cont_bezier_front_b.wav":     NoteTypeSlideB,
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
	case NoteTypeSlideMiddle:
		return "Slide Middle"
	case NoteTypeSlideEndFlickLeftA:
		return "Slide End Flick Left A"
	case NoteTypeSlideEndFlickRightA:
		return "Slide End Flick Right A"
	case NoteTypeSlideEndFlickLeftB:
		return "Slide End Flick Left B"
	case NoteTypeSlideEndFlickRightB:
		return "Slide End Flick Right B"
	default:
		return "Unknown"
	}
}

func (n BasicNoteType) NoteType() BasicNoteType { return n }

func (n BasicNoteType) Mark() string {
	switch n {
	case NoteTypeSlideA, NoteTypeSlideEndA, NoteTypeSlideEndFlickA,
		NoteTypeSlideEndFlickLeftA, NoteTypeSlideEndFlickRightA:
		return "a"
	case NoteTypeSlideB, NoteTypeSlideEndB, NoteTypeSlideEndFlickB,
		NoteTypeSlideEndFlickLeftB, NoteTypeSlideEndFlickRightB:
		return "b"
	default:
		return ""
	}
}

func (n BasicNoteType) Offset() float64 { return 0.0 }

type SpecialSlideNoteType struct {
	mark   string
	offset float64
}

func NewSpecialSlideNoteType(name string) (SpecialSlideNoteType, error) {
	re := regexp.MustCompile(`slide_(.)_(L|R)S(\d\d)\.wav`)
	subs := re.FindStringSubmatch(name)
	if len(subs) < 4 {
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
	return SpecialSlideNoteType{mark: mark, offset: offset}, nil
}

func (n SpecialSlideNoteType) String() string { return fmt.Sprintf("Slide Special %s", n.mark) }

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

func (n SpecialSlideNoteType) Mark() string    { return n.mark }
func (n SpecialSlideNoteType) Offset() float64 { return n.offset }

func NoteTypeOf(wav string) (NoteType, error) {
	if t, ok := wavNoteTypeMap[wav]; ok {
		return t, nil
	}
	if note, err := NewSpecialSlideNoteType(wav); err == nil {
		return note, nil
	}
	return NoteTypeNote, fmt.Errorf("unknown wav: %s", wav)
}

type bmsBPMEvent struct {
	Tick float64
	BPM  float64
}

type bmsTargetKind uint8

const (
	bmsTargetTap bmsTargetKind = iota
	bmsTargetFlick
	bmsTargetSlideStart
	bmsTargetSlideTick
	bmsTargetSlideEnd
	bmsTargetUnknown
)

type bmsTarget struct {
	Channel           string
	Kind              bmsTargetKind
	NoteType          NoteType
	Extra             int
	MergedDirectional bool
}

func (t *bmsTarget) IsSlide() bool {
	return t.Kind == bmsTargetSlideStart || t.Kind == bmsTargetSlideTick || t.Kind == bmsTargetSlideEnd
}

func (t *bmsTarget) IsTap() bool {
	return t.Kind == bmsTargetTap
}

func classifyBmsTarget(noteType NoteType) bmsTargetKind {
	switch noteType.NoteType() {
	case NoteTypeNote:
		return bmsTargetTap
	case NoteTypeFlick, NoteTypeFlickLeft, NoteTypeFlickRight:
		return bmsTargetFlick
	case NoteTypeSlideA, NoteTypeSlideB:
		return bmsTargetSlideStart
	case NoteTypeSlideMiddle:
		return bmsTargetSlideTick
	case NoteTypeSlideEndA, NoteTypeSlideEndB,
		NoteTypeSlideEndFlickA, NoteTypeSlideEndFlickB,
		NoteTypeSlideEndFlickLeftA, NoteTypeSlideEndFlickRightA,
		NoteTypeSlideEndFlickLeftB, NoteTypeSlideEndFlickRightB:
		return bmsTargetSlideEnd
	default:
		return bmsTargetUnknown
	}
}

type bmsEventsPack struct {
	bpmEvents []float64
	Targets   []*bmsTarget
}

// slideEndFlickAngle returns the angle for SlideEndFlick types. Returns -1 if not applicable.
func slideEndFlickAngle(n BasicNoteType) int {
	switch n {
	case NoteTypeSlideEndFlickA, NoteTypeSlideEndFlickB:
		return 90
	case NoteTypeSlideEndFlickLeftA, NoteTypeSlideEndFlickLeftB:
		return 180
	case NoteTypeSlideEndFlickRightA, NoteTypeSlideEndFlickRightB:
		return 0
	}
	return -1
}

func ParseBMS(chartText string) Chart {
	const barLength = 4
	const FIELD_BEGIN = "*----------------------"
	const HEADER_BEGIN = "*---------------------- HEADER FIELD"
	const EXPANSION_BEGIN = "*---------------------- EXPANSION FIELD"
	headerTag := regexp.MustCompile(`^#([0-9A-Z]+) (.*)$`)
	extendedHeaderTag := regexp.MustCompile(`^#([0-9A-Z]+) (.*)$`)
	newline := regexp.MustCompile(`\r?\n`)

	bpm := 130.0
	wavs := map[string]string{}
	extendedBPM := map[string]float64{}

	lines := newline.Split(chartText, -1)

	for !strings.Contains(lines[0], HEADER_BEGIN) {
		lines = lines[1:]
	}
	lines = lines[1:]

	for ; !strings.Contains(lines[0], FIELD_BEGIN); lines = lines[1:] {
		subs := headerTag.FindStringSubmatch(lines[0])
		if len(subs) == 0 {
			continue
		}
		key, value := subs[1], subs[2]
		switch key {
		case "PLAYER", "GENRE", "TITLE", "ARTIST", "PLAYLEVEL", "STAGEFILE", "RANK", "LNTYPE", "BGM":
		case "BPM":
			var err error
			bpm, err = strconv.ParseFloat(value, 64)
			if err != nil {
				log.Fatalf("failed to parse value of #BPM(%s), err: %+v", value, err)
			}
		default:
			if strings.HasPrefix(key, "WAV") {
				wavs[key[3:]] = value
			} else if strings.HasPrefix(key, "BPM") {
				v, err := strconv.ParseFloat(value, 64)
				if err != nil {
					log.Fatalf("failed to parse value of #BPM%s(%s), err: %+v", key[3:], value, err)
				}
				extendedBPM[key[3:]] = v
			} else {
				log.Warnf("unknown command in HEADER FIELD: %s: %s", key, value)
			}
		}
	}

	if strings.Contains(lines[0], EXPANSION_BEGIN) {
		for ; !strings.Contains(lines[0], FIELD_BEGIN); lines = lines[1:] {
			subs := extendedHeaderTag.FindStringSubmatch(lines[0])
			if len(subs) == 0 {
				continue
			}
			key, value := subs[1], subs[2]
			switch key {
			case "BGM":
			default:
				log.Warnf("unknown command in EXPANSION FIELD: %s: %s", key, value)
			}
		}
	}

	lines = lines[1:]

	finalEvents := []*star{}
	rawEvents := map[float64]*bmsEventsPack{}
	type directionalFlickTarget struct {
		track int
		kind  BasicNoteType
	}
	directionalFlickTargets := map[float64][]directionalFlickTarget{}

	for len(lines) != 0 {
		line := lines[0]
		lines = lines[1:]

		events, _, err := parseDataLine(line)
		if err == errInvalidDataLineFormat {
			continue
		} else if err != nil {
			log.Fatalf("Failed to parse line %s: %s", line, err)
		}

		for _, ev := range events {
			tick := ev.Tick()
			channel := ev.Common.Channel

			if _, ok := rawEvents[tick]; !ok {
				rawEvents[tick] = &bmsEventsPack{}
			}

			switch channel {
			case ChannelBackgroundMusic:
				// do nothing
			case ChannelBPMChange:
				value, err := strconv.ParseInt(ev.Type, 16, 64)
				if err != nil {
					log.Fatalf("failed to parse value of bpm(%s), err: %+v", ev.Type, err)
				}
				rawEvents[tick].bpmEvents = append(rawEvents[tick].bpmEvents, float64(value))
			case ChannelExtendedBPM:
				rawEvents[tick].bpmEvents = append(rawEvents[tick].bpmEvents, extendedBPM[ev.Type])
			default:
				wav, ok := wavs[ev.Type]
				if !ok {
					rawEvents[tick].Targets = append(rawEvents[tick].Targets, &bmsTarget{
						Channel:  channel,
						Kind:     bmsTargetTap,
						NoteType: NoteTypeNote,
					})
					continue
				}

				noteType, err := NoteTypeOf(wav)
				if err != nil {
					log.Warnf("failed to get note type: %+v, treated as normal tap", err)
					rawEvents[tick].Targets = append(rawEvents[tick].Targets, &bmsTarget{
						Channel:  channel,
						Kind:     bmsTargetTap,
						NoteType: NoteTypeNote,
					})
					continue
				}

				rawEvents[tick].Targets = append(rawEvents[tick].Targets, &bmsTarget{
					Channel:  channel,
					Kind:     classifyBmsTarget(noteType),
					NoteType: noteType,
				})

				if noteType == NoteTypeFlickLeft || noteType == NoteTypeFlickRight {
					directionalFlickTargets[tick] = append(directionalFlickTargets[tick], directionalFlickTarget{
						track: TRACKS_MAP[channel],
						kind:  noteType.NoteType(),
					})
				}
			}
		}
	}

	for tick, targets := range directionalFlickTargets {
		leftCounts := [7]int{}
		rightCounts := [7]int{}
		for _, t := range targets {
			switch t.kind {
			case NoteTypeFlickLeft:
				leftCounts[t.track]++
			case NoteTypeFlickRight:
				rightCounts[t.track]++
			}
		}

		newEvents := []*bmsTarget{}
		extractRuns := func(counts *[7]int, noteType BasicNoteType, reverse bool) {
			for {
				start := -1
				length := 0
				hasAny := false
				if reverse {
					for i := 6; i >= -1; i-- {
						occupied := i >= 0 && counts[i] > 0
						if occupied {
							hasAny = true
							if start == -1 {
								start = i
								length = 1
							} else {
								length++
							}
						} else if start != -1 {
							newEvents = append(newEvents, &bmsTarget{
								Channel:           simpleTracks[start],
								Kind:              bmsTargetFlick,
								NoteType:          noteType,
								Extra:             length,
								MergedDirectional: true,
							})
							for j := start; j > start-length; j-- {
								counts[j]--
							}
							start = -1
							length = 0
						}
					}
				} else {
					for i := 0; i <= 7; i++ {
						occupied := i < 7 && counts[i] > 0
						if occupied {
							hasAny = true
							if start == -1 {
								start = i
								length = 1
							} else {
								length++
							}
						} else if start != -1 {
							newEvents = append(newEvents, &bmsTarget{
								Channel:           simpleTracks[start],
								Kind:              bmsTargetFlick,
								NoteType:          noteType,
								Extra:             length,
								MergedDirectional: true,
							})
							for j := start; j < start+length; j++ {
								counts[j]--
							}
							start = -1
							length = 0
						}
					}
				}

				if !hasAny {
					return
				}
			}
		}

		extractRuns(&rightCounts, NoteTypeFlickRight, false)
		extractRuns(&leftCounts, NoteTypeFlickLeft, true)

		rawEvents[tick].Targets = append(rawEvents[tick].Targets, newEvents...)
	}

	ticks := utils.SortedKeysOf(rawEvents)

	holdTracks := [7]float64{math.NaN(), math.NaN(), math.NaN(), math.NaN(), math.NaN(), math.NaN(), math.NaN()}
	type slideState struct {
		current  *star
		lastTick float64
	}

	activeSlides := map[string][]*slideState{
		"a": {},
		"b": {},
	}
	secStart := 0.0
	tickStart := 0.0
	bpmEvents := []*bmsBPMEvent{}

	pickNearestSlide := func(mark string, trackID float64) (int, *slideState) {
		states := activeSlides[mark]
		if len(states) == 0 {
			return -1, nil
		}

		bestIdx := -1
		bestDist := math.Inf(1)
		for i, st := range states {
			d := math.Abs(st.current.track - trackID)
			if d < bestDist {
				bestDist = d
				bestIdx = i
			}
		}

		if bestIdx == -1 {
			return -1, nil
		}

		return bestIdx, states[bestIdx]
	}

	appendOrStartSlide := func(mark string, sec, trackID, width float64, tick float64) {
		idx, st := pickNearestSlide(mark, trackID)
		if st == nil {
			head := newStar(sec, trackID, width).markAsHead()
			activeSlides[mark] = append(activeSlides[mark], &slideState{current: head, lastTick: tick})
			return
		}

		st.current = newStar(sec, trackID, width).chainsAfter(st.current)
		st.lastTick = tick
		activeSlides[mark][idx] = st
	}

	appendSlideMiddle := func(mark string, sec, trackID, width float64, tick float64) {
		idx, st := pickNearestSlide(mark, trackID)
		if st == nil {
			return
		}

		st.current = newStar(sec, trackID, width).chainsAfter(st.current)
		st.lastTick = tick
		activeSlides[mark][idx] = st
	}

	endSlide := func(mark string, sec, trackID, width float64, angle int, tick float64) {
		idx, st := pickNearestSlide(mark, trackID)
		if st == nil {
			return
		}

		end := newStar(sec, trackID, width).chainsAfter(st.current)
		if angle >= 0 {
			end = end.flickToIfOk(true, angle)
		}
		finalEvents = append(finalEvents, end.markAsEnd())
		activeSlides[mark] = slices.Delete(activeSlides[mark], idx, idx+1)
	}

	resolveDirectionalFlickSpan := func(trackID float64, noteType NoteType, extra int) (float64, float64) {
		span := max(extra, 1)
		if span == 1 {
			return trackID, 1.0 / 6
		}

		half := (float64(span) - 1) / 12
		switch noteType {
		case NoteTypeFlickRight:
			left := trackID
			right := trackID + 2*half
			return (left + right) / 2, float64(span) / 6
		case NoteTypeFlickLeft:
			right := trackID
			left := trackID - 2*half
			return (left + right) / 2, float64(span) / 6
		default:
			return trackID, 1.0 / 6
		}
	}

	for _, tick := range ticks {
		pack := rawEvents[tick]
		for _, bpmValue := range pack.bpmEvents {
			bpmEvents = append(bpmEvents, &bmsBPMEvent{Tick: tick, BPM: bpmValue})
			secStart += barLength * 60 / bpm * (tick - tickStart)
			tickStart = tick
			bpm = bpmValue
		}

		taps := []*bmsTarget{}
		slideStarts := []*bmsTarget{}
		slideMids := []*bmsTarget{}
		slideEnds := []*bmsTarget{}
		flicks := []*bmsTarget{}
		others := []*bmsTarget{}

		for _, ev := range pack.Targets {
			switch ev.Kind {
			case bmsTargetSlideStart:
				slideStarts = append(slideStarts, ev)
			case bmsTargetSlideTick:
				slideMids = append(slideMids, ev)
			case bmsTargetTap:
				taps = append(taps, ev)
			case bmsTargetFlick:
				flicks = append(flicks, ev)
			case bmsTargetSlideEnd:
				slideEnds = append(slideEnds, ev)
			default:
				others = append(others, ev)
			}
		}

		orderedTargets := make([]*bmsTarget, 0, len(pack.Targets))
		orderedTargets = append(orderedTargets, slideStarts...)
		orderedTargets = append(orderedTargets, slideMids...)
		orderedTargets = append(orderedTargets, taps...)
		orderedTargets = append(orderedTargets, flicks...)
		orderedTargets = append(orderedTargets, slideEnds...)
		orderedTargets = append(orderedTargets, others...)

		mergedLeftCover := [7]bool{}
		mergedRightCover := [7]bool{}
		for _, ev := range orderedTargets {
			if !ev.MergedDirectional {
				continue
			}
			start, ok := TRACKS_MAP[ev.Channel]
			if !ok {
				continue
			}
			length := max(ev.Extra, 1)
			switch ev.NoteType {
			case NoteTypeFlickRight:
				for i := start; i < min(start+length, 7); i++ {
					mergedRightCover[i] = true
				}
			case NoteTypeFlickLeft:
				for i := start; i > max(start-length, -1); i-- {
					mergedLeftCover[i] = true
				}
			}
		}

		for _, ev := range orderedTargets {
			switch ev.Channel {
			case ChannelNoteTrack1, ChannelNoteTrack2, ChannelNoteTrack3, ChannelNoteTrack4, ChannelNoteTrack5, ChannelNoteTrack6, ChannelNoteTrack7:
				trackID := float64(TRACKS_MAP[ev.Channel]) / 6
				sec := secStart + barLength*60/bpm*(tick-tickStart)
				switch ev.NoteType {
				case NoteTypeNote:
					finalEvents = append(finalEvents,
						newStar(sec, trackID, 1.0/6).markAsTap())
				case NoteTypeFlick:
					finalEvents = append(finalEvents,
						newStar(sec, trackID, 1.0/6).markAsTap().flickToIfOk(true, 90))
				case NoteTypeFlickLeft:
					if !ev.MergedDirectional && mergedLeftCover[TRACKS_MAP[ev.Channel]] {
						continue
					}
					flickTrack, flickWidth := resolveDirectionalFlickSpan(trackID, ev.NoteType, ev.Extra)
					finalEvents = append(finalEvents,
						newStar(sec, flickTrack, flickWidth).markAsTap().flickToIfOk(true, 180))
				case NoteTypeFlickRight:
					if !ev.MergedDirectional && mergedRightCover[TRACKS_MAP[ev.Channel]] {
						continue
					}
					flickTrack, flickWidth := resolveDirectionalFlickSpan(trackID, ev.NoteType, ev.Extra)
					finalEvents = append(finalEvents,
						newStar(sec, flickTrack, flickWidth).markAsTap().flickToIfOk(true, 0))
				case NoteTypeSlideA:
					appendOrStartSlide("a", sec, trackID, 1.0/6, tick)
				case NoteTypeSlideB:
					appendOrStartSlide("b", sec, trackID, 1.0/6, tick)
				case NoteTypeSlideMiddle:
					appendSlideMiddle("a", sec, trackID, 1.0/6, tick)
					appendSlideMiddle("b", sec, trackID, 1.0/6, tick)
				case NoteTypeSlideEndA:
					endSlide("a", sec, trackID, 1.0/6, -1, tick)
				case NoteTypeSlideEndB:
					endSlide("b", sec, trackID, 1.0/6, -1, tick)
				case NoteTypeSlideEndFlickA, NoteTypeSlideEndFlickLeftA, NoteTypeSlideEndFlickRightA:
					angle := slideEndFlickAngle(ev.NoteType.(BasicNoteType))
					endSlide("a", sec, trackID, 1.0/6, angle, tick)
				case NoteTypeSlideEndFlickB, NoteTypeSlideEndFlickLeftB, NoteTypeSlideEndFlickRightB:
					angle := slideEndFlickAngle(ev.NoteType.(BasicNoteType))
					endSlide("b", sec, trackID, 1.0/6, angle, tick)
				default:
					log.Warnf("unknown note type %s on note track\n", ev.NoteType)
				}

			case ChannelHoldTrack1, ChannelHoldTrack2, ChannelHoldTrack3, ChannelHoldTrack4, ChannelHoldTrack5, ChannelHoldTrack6, ChannelHoldTrack7:
				trackID := TRACKS_MAP[ev.Channel]
				trackX := float64(trackID) / 6
				sec := secStart + 240.0/bpm*(tick-tickStart)
				switch ev.NoteType {
				case NoteTypeNote:
					startSec := holdTracks[trackID]
					if math.IsNaN(startSec) {
						holdTracks[trackID] = sec
					} else {
						finalEvents = append(finalEvents,
							newStar(sec, trackX, 1.0/6).chainsAfter(
								newStar(startSec, trackX, 1.0/6).markAsTap().markAsHead(),
							).markAsEnd())
						holdTracks[trackID] = math.NaN()
					}
				case NoteTypeFlick, NoteTypeFlickLeft, NoteTypeFlickRight:
					startSec := holdTracks[trackID]
					if math.IsNaN(startSec) {
						log.Fatalf("no hold start data on track %d", trackID)
					}
					angle := 90
					if ev.NoteType == NoteTypeFlickLeft {
						angle = 180
					} else if ev.NoteType == NoteTypeFlickRight {
						angle = 0
					}
					finalEvents = append(finalEvents,
						newStar(sec, trackX, 1.0/6).chainsAfter(
							newStar(startSec, trackX, 1.0/6).markAsTap().markAsHead(),
						).flickToIfOk(true, angle).markAsEnd())
					holdTracks[trackID] = math.NaN()
				default:
					log.Warnf("unknown note type %s on hold track\n", ev.NoteType)
				}

			case ChannelSpecialTrack1, ChannelSpecialTrack2, ChannelSpecialTrack3, ChannelSpecialTrack4, ChannelSpecialTrack5, ChannelSpecialTrack6, ChannelSpecialTrack7:
				trackID := float64(TRACKS_MAP[ev.Channel]) / 6
				sec := secStart + 240.0/bpm*(tick-tickStart)
				switch nt := ev.NoteType.(type) {
				case SpecialSlideNoteType:
					switch nt.mark {
					case "a":
						appendOrStartSlide("a", sec, trackID+nt.offset/6, 1.0/6, tick)
					case "b":
						appendOrStartSlide("b", sec, trackID+nt.offset/6, 1.0/6, tick)
					default:
						log.Warnf("unknown mark %s\n", nt.mark)
					}
				case BasicNoteType:
					switch nt {
					case NoteTypeSlideA:
						appendOrStartSlide("a", sec, trackID, 1.0/6, tick)
					case NoteTypeSlideB:
						appendOrStartSlide("b", sec, trackID, 1.0/6, tick)
					case NoteTypeSlideMiddle:
						appendSlideMiddle("a", sec, trackID, 1.0/6, tick)
						appendSlideMiddle("b", sec, trackID, 1.0/6, tick)
					case NoteTypeSlideEndA:
						endSlide("a", sec, trackID, 1.0/6, -1, tick)
					case NoteTypeSlideEndB:
						endSlide("b", sec, trackID, 1.0/6, -1, tick)
					case NoteTypeSlideEndFlickA, NoteTypeSlideEndFlickLeftA, NoteTypeSlideEndFlickRightA:
						angle := slideEndFlickAngle(nt)
						endSlide("a", sec, trackID, 1.0/6, angle, tick)
					case NoteTypeSlideEndFlickB, NoteTypeSlideEndFlickLeftB, NoteTypeSlideEndFlickRightB:
						angle := slideEndFlickAngle(nt)
						endSlide("b", sec, trackID, 1.0/6, angle, tick)
					default:
						log.Warnf("%s should not appear on special channel %s (tick = %f)", ev.NoteType, ev.Channel, tick)
					}
				default:
					log.Warnf("%s should not appear on special channel %s (tick = %f)", ev.NoteType, ev.Channel, tick)
				}
			}
		}
	}

	for _, st := range activeSlides["a"] {
		finalEvents = append(finalEvents, st.current.markAsEnd())
	}
	for _, st := range activeSlides["b"] {
		finalEvents = append(finalEvents, st.current.markAsEnd())
	}

	return finalEvents
}
