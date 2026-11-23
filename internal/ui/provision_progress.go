package ui

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	clr "github.com/datamitsu/datamitsu/internal/color"
	"github.com/datamitsu/datamitsu/internal/provision"
	"github.com/datamitsu/datamitsu/internal/term"
	"github.com/datamitsu/datamitsu/internal/uievent"
	"github.com/mattn/go-runewidth"
	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
)

const transferEstimateTTL = 3 * time.Second

type transferEstimate struct {
	at     time.Time
	detail string
}

type provisionRow struct {
	bar             *mpb.Bar
	name            string
	priority        int
	detail          atomic.Value // string; read by mpb's render goroutine
	estimate        atomic.Pointer[transferEstimate]
	sampleAt        time.Time
	sampleBytes     int64
	bytesPerSecond  float64
	lastAt          time.Time
	lastPercent     int
	waitingReported bool
	phase           uievent.Type
	determinate     bool
	owners          []string
	waitOwner       string
	waitUpdatedAt   int64
}

// RenderProvisionQueue keeps pending work in one transient row below active rows.
func (d *Display) RenderProvisionQueue(detail string) {
	if d.mode != term.Interactive || Quiet() || Muted() || d.quiet {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closing {
		return
	}
	// Provisioning events require a nonempty OpID; the queue is display-local.
	const queueID = ""
	row := d.provision[queueID]
	if detail == "" {
		if row != nil {
			row.bar.Abort(true)
			delete(d.provision, queueID)
			d.bars--
			d.drainIdleProgressLocked()
		}
		return
	}
	detail = "┃ " + detail
	if row != nil {
		row.detail.Store(detail)
		return
	}
	p := d.ensureProg()
	if p == nil {
		return
	}
	row = &provisionRow{
		bar:             nil,
		name:            "",
		priority:        0,
		detail:          atomic.Value{},
		estimate:        atomic.Pointer[transferEstimate]{},
		sampleAt:        time.Time{},
		sampleBytes:     0,
		bytesPerSecond:  0,
		lastAt:          time.Time{},
		lastPercent:     0,
		waitingReported: false,
		phase:           "",
		determinate:     false,
		owners:          nil,
		waitOwner:       "",
		waitUpdatedAt:   0,
	}
	row.detail.Store(detail)
	row.bar = p.New(0, mpb.SpinnerStyle(" "), mpb.BarWidth(1), mpb.BarPriority(math.MaxInt), mpb.PrependDecorators(decor.Any(func(s decor.Statistics) string {
		label, _ := row.detail.Load().(string)
		return runewidth.Truncate(label, max(0, s.AvailableWidth-1), "…")
	})))
	if d.provision == nil {
		d.provision = make(map[string]*provisionRow)
	}
	d.provision[queueID] = row
	d.bars++
}

func (d *Display) detachProvisionRowLocked(id string, completed bool) {
	row := d.provision[id]
	delete(d.provision, id)
	if row == nil || row.bar == nil {
		return
	}
	for _, other := range d.provision {
		if other == row {
			return
		}
	}
	if completed {
		row.bar.SetTotal(-1, true)
	} else {
		row.bar.Abort(true)
	}
	d.bars--
}

// RenderProvisionEvent renders a provisioning event for humans only. It does not
// forward the event to the typed event sink; emitters remain the sole publisher.
func (d *Display) RenderProvisionEvent(event uievent.Event) {
	d.RenderInstallProgress(provision.InstallProgress{
		Owners:         nil,
		WaitOwner:      "",
		WaitUpdatedAt:  0,
		WaitBytesDone:  0,
		WaitBytesTotal: 0,
		Event:          event,
	})
}

// RenderInstallProgress uses private lease ownership to correlate visible work.
func (d *Display) RenderInstallProgress(progress provision.InstallProgress) {
	event := progress.Event
	if event.OpID == "" || (event.Type != uievent.TypeDownload && event.Type != uievent.TypeInstall && event.Type != uievent.TypePrepare) {
		return
	}
	if Quiet() || Muted() || d.quiet {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closing {
		return
	}
	d.renderInstallProgressLocked(progress)
}

func (d *Display) rememberHiddenWait(progress provision.InstallProgress) {
	if d.hiddenWaits == nil {
		d.hiddenWaits = make(map[string]provision.InstallProgress)
	}
	d.hiddenWaits[progress.OpID] = progress
}

func (d *Display) renderInstallProgressLocked(progress provision.InstallProgress) {
	event := progress.Event
	terminal := event.Status == uievent.StatusDone || event.Status == uievent.StatusFail || event.Status == uievent.StatusSkip
	row := d.provision[event.OpID]
	if terminal {
		delete(d.hiddenWaits, event.OpID)
		delete(d.waitSnapshots, event.OpID)
		d.detachProvisionRowLocked(event.OpID, event.Status == uievent.StatusDone)
		for _, wait := range d.hiddenWaits {
			d.renderInstallProgressLocked(wait)
		}
		d.drainIdleProgressLocked()
		return
	}
	if d.mode == term.Interactive {
		delete(d.hiddenWaits, event.OpID)
		if progress.WaitOwner != "" {
			if d.waitSnapshots == nil {
				d.waitSnapshots = make(map[string]provision.InstallProgress)
			}
			for _, previous := range d.waitSnapshots {
				if previous.WaitOwner == progress.WaitOwner && previous.WaitUpdatedAt > progress.WaitUpdatedAt {
					progress = previous
					progress.OpID = event.OpID
				}
			}
			for id, previous := range d.waitSnapshots {
				if previous.WaitOwner == progress.WaitOwner {
					latest := progress
					latest.OpID = id
					d.waitSnapshots[id] = latest
					if _, hidden := d.hiddenWaits[id]; hidden {
						d.hiddenWaits[id] = latest
					}
				}
			}
			event = progress.Event
			d.waitSnapshots[event.OpID] = progress
		} else {
			delete(d.waitSnapshots, event.OpID)
		}
		if row != nil && row.waitOwner != progress.WaitOwner {
			d.detachProvisionRowLocked(event.OpID, false)
			row = nil
		}
		var shared *provisionRow
		for id, other := range d.provision {
			if progress.WaitOwner != "" && slices.Contains(other.owners, progress.WaitOwner) {
				d.detachProvisionRowLocked(event.OpID, false)
				d.rememberHiddenWait(progress)
				return
			}
			if other.waitOwner != "" && slices.Contains(progress.Owners, other.waitOwner) {
				d.detachProvisionRowLocked(id, false)
				d.rememberHiddenWait(d.waitSnapshots[id])
			} else if progress.WaitOwner != "" && other.waitOwner == progress.WaitOwner {
				shared = other
			}
		}
		if shared != nil && shared != row {
			d.detachProvisionRowLocked(event.OpID, false)
			d.provision[event.OpID] = shared
			row = shared
		}
		if row != nil && progress.WaitUpdatedAt != 0 && progress.WaitUpdatedAt < row.waitUpdatedAt {
			return
		}
		if progress.WaitBytesTotal > 0 {
			event.Type = uievent.TypeDownload
			event.BytesDone, event.BytesTotal = progress.WaitBytesDone, progress.WaitBytesTotal
		}
	}
	if row != nil {
		row.waitUpdatedAt = progress.WaitUpdatedAt
	}
	isNew := row == nil
	name := provisionName(event)
	if isNew {
		row = &provisionRow{
			bar:             nil,
			detail:          atomic.Value{},
			estimate:        atomic.Pointer[transferEstimate]{},
			sampleAt:        time.Time{},
			sampleBytes:     0,
			bytesPerSecond:  0,
			lastPercent:     0,
			waitingReported: false,
			phase:           "",
			determinate:     false,
			owners:          nil,
			waitOwner:       "",
			waitUpdatedAt:   0,
			name:            name, priority: d.provisionNext, lastAt: now(),
		}
		row.waitUpdatedAt = progress.WaitUpdatedAt
		d.provisionNext++
		row.detail.Store("")
		if row.name == "" {
			row.name = "Preparing"
		}
		if d.provision == nil {
			d.provision = make(map[string]*provisionRow)
		}
		d.provision[event.OpID] = row
	}
	row.owners, row.waitOwner = progress.Owners, progress.WaitOwner
	phaseChanged := !isNew && row.phase != event.Type
	determinate := event.Type == uievent.TypeDownload && event.BytesTotal > 0
	sizeChanged := !isNew && determinate != row.determinate
	nameChanged := name != "" && row.name != name
	if isNew || phaseChanged || sizeChanged || nameChanged {
		if phaseChanged || sizeChanged {
			row.detail.Store("")
			row.lastPercent = 0
			row.lastAt = now()
			row.sampleAt = time.Time{}
			row.bytesPerSecond = 0
			row.estimate.Store(nil)
		}
		if nameChanged {
			row.name = name
		}
		if d.mode == term.Interactive {
			d.replaceProvisionVisualLocked(row, event)
		} else if (isNew || phaseChanged) && !strings.HasPrefix(event.Msg, "Waiting for installation:") {
			d.writeProvisionLineLocked(row.name)
		}
		row.phase = event.Type
		row.determinate = determinate
	}
	if event.Type == uievent.TypeDownload {
		if d.mode == term.Interactive && progress.WaitOwner == "" {
			row.observeTransfer(event, now())
		}
		if event.BytesTotal > 0 && row.bar != nil {
			row.bar.SetTotal(event.BytesTotal, false)
			row.bar.SetCurrent(event.BytesDone)
		}
		if d.mode != term.Interactive {
			d.maybeProvisionMilestoneLocked(row, event.BytesDone, event.Percent)
		}
		return
	}
	detail := event.Msg
	waiting := strings.HasPrefix(detail, "Waiting for installation:")
	if waiting {
		detail = strings.TrimSpace(strings.TrimPrefix(detail, "Waiting for installation:"))
		if event.Name != "" {
			detail = strings.TrimSpace(strings.TrimPrefix(detail, event.Name))
		}
	}
	switch detail {
	case "publish":
		detail = "finalizing install"
	case "(publish)":
		detail = "(finalizing install)"
	}
	if event.Index > 0 && event.Total > 0 {
		detail = fmt.Sprintf("%s (%d/%d)", detail, event.Index, event.Total)
	} else if event.Index > 0 {
		detail = fmt.Sprintf("%s (%d)", detail, event.Index)
	}
	if waiting && d.mode == term.Interactive {
		var received, total int64
		if n, err := fmt.Sscanf(detail, "(download %d/%d bytes)", &received, &total); n == 2 && err == nil && total > 0 && received >= 0 && received <= total {
			detail = fmt.Sprintf("download %.0f%%", math.Floor(100*float64(received)/float64(total)))
		}
		if detail == "" {
			detail = "Waiting"
		} else {
			detail = "Waiting: " + detail
		}
	}
	detail = strings.TrimSpace(detail)
	row.detail.Store(detail)
	if strings.HasPrefix(event.Msg, "Waiting for installation:") && d.mode != term.Interactive && !row.waitingReported {
		row.waitingReported = true
		line := row.name
		if detail != "" {
			line += " " + detail
		}
		d.writeProvisionLineLocked(line)
		return
	}
	if d.mode != term.Interactive && event.Status == uievent.StatusProgress {
		d.maybeProvisionMilestoneLocked(row, 0, 0)
	}
}

func (row *provisionRow) observeTransfer(event uievent.Event, at time.Time) {
	if row.sampleAt.IsZero() || event.BytesDone < row.sampleBytes {
		row.sampleAt, row.sampleBytes = at, event.BytesDone
		row.bytesPerSecond = 0
		row.estimate.Store(nil)
		return
	}
	elapsed := at.Sub(row.sampleAt)
	if elapsed < 250*time.Millisecond || event.BytesDone == row.sampleBytes {
		return
	}
	rate := float64(event.BytesDone-row.sampleBytes) / elapsed.Seconds()
	if row.bytesPerSecond != 0 && elapsed < transferEstimateTTL {
		rate = .65*row.bytesPerSecond + .35*rate
	}
	row.sampleAt, row.sampleBytes, row.bytesPerSecond = at, event.BytesDone, rate
	detail := fmt.Sprintf("% .1f/s", decor.SizeB1024(rate))
	if remaining := event.BytesTotal - event.BytesDone; remaining > 0 {
		seconds := math.Ceil(float64(remaining) / rate)
		if seconds < (24 * time.Hour).Seconds() {
			eta := time.Duration(seconds) * time.Second
			if eta >= time.Hour {
				minutes := int(eta.Round(time.Minute) / time.Minute)
				detail += fmt.Sprintf(" ~%dh%02dm", minutes/60, minutes%60)
			} else {
				detail += " ~" + eta.String()
			}
		}
	}
	row.estimate.Store(&transferEstimate{at: at, detail: detail})
}

func (row *provisionRow) transferDetail(at time.Time) string {
	estimate := row.estimate.Load()
	if estimate == nil || at.Sub(estimate.at) >= transferEstimateTTL {
		return ""
	}
	return estimate.detail
}

func (d *Display) replaceProvisionVisualLocked(row *provisionRow, event uievent.Event) {
	if row.bar != nil {
		row.bar.Abort(true)
		row.bar = nil
		d.bars--
	}
	p := d.ensureProg()
	if p == nil {
		return
	}
	d.bars++
	label := row.name
	if event.Type == uievent.TypeDownload && event.Name != "" {
		label = strings.TrimPrefix(event.Name, "binary ")
	}
	if event.Type == uievent.TypeInstall && event.Name != "" {
		label = strings.TrimPrefix(strings.TrimPrefix(event.Name, "binary "), "app ")
	}
	const (
		maxNameWidth   = 32
		barWidth       = 16
		minBarWidth    = 8
		percentWidth   = 7
		byteWidth      = 11
		barGap         = 2
		separatorWidth = 3
	)
	waiting := row.waitOwner != "" || strings.HasPrefix(event.Msg, "Waiting for installation:")
	name := decor.Any(func(s decor.Statistics) string {
		w := min(maxNameWidth, max(0, s.AvailableWidth-5))
		if waiting {
			w = min(w, max(0, s.AvailableWidth-13))
		}
		return runewidth.FillRight(runewidth.Truncate("┃ "+label, w, "…"), w)
	})
	if event.Type == uievent.TypeDownload && event.BytesTotal > 0 {
		artifact := strings.TrimPrefix(event.Name, "binary ")
		waiting := row.waitOwner != ""
		const estimateWidth = 23
		withEstimate := false // mpb renders prefix and suffix in order on the same bar goroutine.
		prefix := decor.Any(func(s decor.Statistics) string {
			w := min(maxNameWidth, max(5, s.AvailableWidth-percentWidth-barGap-minBarWidth))
			withEstimate = s.AvailableWidth-w-percentWidth-barGap-minBarWidth >= 2*byteWidth+separatorWidth+estimateWidth+2
			label := artifact
			if waiting {
				artifactName := strings.TrimPrefix(artifact, "runtime ")
				marker := "Waiting: "
				if w-4 < len(marker)+runewidth.StringWidth(artifactName) {
					marker = "~ "
				}
				label = marker + artifactName
			}
			text := "┃ " + runewidth.FillRight(runewidth.Truncate(label, w-4, "…"), w-4) + "  "
			text += fmt.Sprintf(" %3.0f%%  ", math.Floor(100*float64(s.Current)/float64(max(1, s.Total))))
			budget := s.AvailableWidth - w - percentWidth - barGap - minBarWidth
			if budget >= byteWidth {
				text += fmt.Sprintf("%*s", byteWidth, fmt.Sprintf("% .2f", decor.SizeB1024(s.Current)))
			}
			if budget >= 2*byteWidth+separatorWidth {
				text += fmt.Sprintf(" / %*s", byteWidth, fmt.Sprintf("% .2f", decor.SizeB1024(s.Total)))
			}
			return text + "  "
		})
		prefix = decor.Meta(prefix, func(text string) string {
			if i := strings.LastIndexByte(text, '%'); i >= 3 {
				return text[:i-3] + clr.Cyan(text[i-3:i+1]) + text[i+1:]
			}
			return text
		})
		estimate := decor.Any(func(s decor.Statistics) string {
			if !withEstimate {
				return ""
			}
			if waiting {
				return strings.Repeat(" ", estimateWidth)
			}
			return "  " + runewidth.FillRight(runewidth.Truncate(row.transferDetail(now()), estimateWidth-2, "…"), estimateWidth-2)
		})
		row.bar = p.AddBar(0, mpb.BarPriority(row.priority), mpb.BarWidth(barWidth), mpb.BarRemoveOnComplete(), mpb.PrependDecorators(prefix), mpb.AppendDecorators(estimate))
		row.bar.SetTotal(event.BytesTotal, false)
		row.bar.SetCurrent(event.BytesDone)
		return
	}
	row.bar = p.AddSpinner(0, mpb.BarPriority(row.priority), mpb.BarWidth(1), mpb.BarRemoveOnComplete(), mpb.PrependDecorators(
		name,
		decor.Any(func(s decor.Statistics) string {
			detail, _ := row.detail.Load().(string)
			if event.Type == uievent.TypeDownload {
				detail = row.transferDetail(now())
			}
			if detail != "" {
				return runewidth.Truncate("  "+detail, max(0, s.AvailableWidth-3), "…")
			}
			return ""
		}),
	))
}

func provisionName(event uievent.Event) string {
	if event.Type == uievent.TypeDownload {
		return "Downloading " + event.Name
	}
	if event.Type == uievent.TypePrepare {
		return "Preparing " + event.Name
	}
	if strings.HasPrefix(event.Msg, "Waiting for installation:") {
		if event.Name != "" {
			return "Waiting for installation: " + event.Name
		}
		return "Waiting for installation"
	}
	if event.Name != "" {
		return "Installing " + event.Name
	}
	return ""
}

func (d *Display) maybeProvisionMilestoneLocked(row *provisionRow, bytesDone int64, percent int) {
	n := now()
	if percent > 0 && percent < 100 {
		bucket := percent / 10
		if bucket <= row.lastPercent {
			return
		}
		row.lastPercent = bucket
	} else if n.Sub(row.lastAt) < plainMinInterval {
		return
	}
	row.lastAt = n
	detail, _ := row.detail.Load().(string)
	switch {
	case detail != "":
		d.writeProvisionLineLocked(row.name + ": " + detail)
	case percent > 0:
		d.writeProvisionLineLocked(fmt.Sprintf("%s: %d%%", row.name, percent))
	case bytesDone > 0:
		d.writeProvisionLineLocked(fmt.Sprintf("%s: %d bytes", row.name, bytesDone))
	default:
		d.writeProvisionLineLocked(row.name)
	}
}

func (d *Display) writeProvisionLineLocked(text string) {
	_, _ = fmt.Fprintln(d.lineWriterLocked(d.err), text)
}
