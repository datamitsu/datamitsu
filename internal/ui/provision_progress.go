package ui

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/datamitsu/datamitsu/internal/term"
	"github.com/datamitsu/datamitsu/internal/uievent"
	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
)

type provisionRow struct {
	bar             *mpb.Bar
	name            string
	detail          atomic.Value // string; read by mpb's render goroutine
	lastAt          time.Time
	lastPercent     int
	waitingReported bool
	phase           uievent.Type
	determinate     bool
}

// RenderProvisionEvent renders a provisioning event for humans only. It does not
// forward the event to the typed event sink; emitters remain the sole publisher.
func (d *Display) RenderProvisionEvent(event uievent.Event) {
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
	terminal := event.Status == uievent.StatusDone || event.Status == uievent.StatusFail || event.Status == uievent.StatusSkip
	row := d.provision[event.OpID]
	if terminal {
		if row != nil {
			if row.bar != nil {
				if event.Status == uievent.StatusDone {
					row.bar.SetTotal(-1, true)
				} else {
					row.bar.Abort(true)
				}
				d.bars--
			}
			delete(d.provision, event.OpID)
		}
		return
	}
	isNew := row == nil
	if isNew {
		row = &provisionRow{name: provisionName(event), lastAt: now()}
		row.detail.Store("")
		if row.name == "" {
			row.name = "Preparing"
		}
		if d.provision == nil {
			d.provision = make(map[string]*provisionRow)
		}
		d.provision[event.OpID] = row
	}
	phaseChanged := !isNew && row.phase != event.Type
	knownSizeUpgrade := !isNew && event.Type == uievent.TypeDownload && event.BytesTotal > 0 && !row.determinate
	if isNew || phaseChanged || knownSizeUpgrade {
		if phaseChanged {
			row.detail.Store("")
			row.lastPercent = 0
			row.lastAt = now()
		}
		if !isNew {
			if name := provisionName(event); name != "" {
				row.name = name
			}
		}
		if d.mode == term.Interactive {
			d.replaceProvisionVisualLocked(row, event)
		} else if (isNew || phaseChanged) && !strings.HasPrefix(event.Msg, "Waiting for installation:") {
			d.writeProvisionLineLocked(row.name)
		}
		row.phase = event.Type
		row.determinate = event.Type == uievent.TypeDownload && event.BytesTotal > 0
	}
	if event.Type == uievent.TypeDownload {
		if event.BytesTotal > 0 && row.bar != nil {
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
		row.name = "Waiting for installation"
		if event.Name != "" {
			row.name += ": " + event.Name
		}
		detail = strings.TrimSpace(strings.TrimPrefix(detail, "Waiting for installation:"))
		if event.Name != "" {
			detail = strings.TrimSpace(strings.TrimPrefix(detail, event.Name))
		}
	} else if event.Name != "" && (row.name == "Preparing" || row.name == "Waiting for installation") {
		row.name = provisionName(event)
	}
	if event.Index > 0 && event.Total > 0 {
		detail = fmt.Sprintf("%s (%d/%d)", detail, event.Index, event.Total)
	} else if event.Index > 0 {
		detail = fmt.Sprintf("%s (%d)", detail, event.Index)
	}
	if waiting && d.mode == term.Interactive && detail != "" {
		detail = "Waiting: " + detail
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
	if event.Type == uievent.TypeDownload && event.BytesTotal > 0 {
		row.bar = p.AddBar(event.BytesTotal, mpb.BarRemoveOnComplete(), mpb.PrependDecorators(
			decor.Name(row.name, decor.WC{W: 24, C: decor.DSyncSpaceR}),
			decor.CountersKibiByte("% .2f / % .2f", decor.WC{W: 25, C: decor.DSyncSpaceR}),
		), mpb.AppendDecorators(decor.NewPercentage(" %3.0f ", decor.WCSyncSpace)))
		row.bar.SetCurrent(event.BytesDone)
		return
	}
	row.bar = p.AddSpinner(0, mpb.BarWidth(1), mpb.BarRemoveOnComplete(), mpb.AppendDecorators(
		decor.Name(" "+row.name, decor.WC{W: spinnerNameWidth, C: decor.DindentRight}),
		decor.Any(func(decor.Statistics) string {
			detail, _ := row.detail.Load().(string)
			return detail
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
	if d.prog != nil && d.bars > 0 {
		_, _ = fmt.Fprintln(d.prog, text)
		return
	}
	_, _ = fmt.Fprintln(d.err, text)
}
