// Package sysinfo gathers best-effort, informational machine health data
// (chip, memory, battery, OS version) by shelling out to fixed-path macOS
// system binaries. It never returns an error and never panics: every
// sub-probe failure just leaves the corresponding *Known flag false.
package sysinfo

import "context"

// Info holds best-effort machine health data. Every field is optional;
// zero/false/empty values mean "unknown" and must be rendered as such,
// never treated as a real zero.
type Info struct {
	ChipModel   string // e.g. "Apple M2 Pro"; "" = unknown
	ChipKnown   bool
	TotalMemory int64 // bytes; 0 = unknown
	MemoryKnown bool

	IsLaptop      bool // false = desktop OR indeterminate (fail closed)
	HostTypeKnown bool

	BatteryPercent  int // 0-100; only meaningful if BatteryKnown
	BatteryKnown    bool
	CycleCount      int // >= 0; only meaningful if CycleCountKnown
	CycleCountKnown bool

	OSName         string // e.g. "macOS Tahoe"; marketing name omitted if unmapped
	OSVersion      string // e.g. "26.5.2"
	OSBuild        string // e.g. "25F84"
	OSVersionKnown bool
}

// Gather collects machine health info using short-timeout subprocess calls.
// It is intended to be invoked from a background goroutine (e.g. a tea.Cmd),
// never synchronously on a UI thread, since each probe may take up to a
// couple of seconds to time out.
func Gather(ctx context.Context) Info {
	info := Info{}

	if model, ok := gatherChipModel(ctx); ok {
		info.ChipModel, info.ChipKnown = model, true
	}

	if mem, ok := gatherTotalMemory(ctx); ok {
		info.TotalMemory, info.MemoryKnown = mem, true
	}

	bat := gatherBattery(ctx)
	info.IsLaptop = bat.isLaptop
	info.HostTypeKnown = bat.hostTypeKnown
	info.BatteryPercent = bat.percent
	info.BatteryKnown = bat.percentKnown
	info.CycleCount = bat.cycles
	info.CycleCountKnown = bat.cyclesKnown

	if osInfo, ok := gatherOSVersion(ctx); ok {
		info.OSName = osInfo.name
		info.OSVersion = osInfo.version
		info.OSBuild = osInfo.build
		info.OSVersionKnown = true
	}

	return info
}
